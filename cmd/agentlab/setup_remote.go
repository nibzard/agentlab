package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/operations"
)

type setupOperationError struct {
	Cause  error
	Record operations.Record
}

func (e *setupOperationError) Error() string { return e.Cause.Error() }
func (e *setupOperationError) Unwrap() error { return e.Cause }
func (e *setupOperationError) ErrorMetadata() map[string]any {
	fields := map[string]any{}
	var nested interface{ ErrorMetadata() map[string]any }
	if errors.As(e.Cause, &nested) {
		for key, value := range nested.ErrorMetadata() {
			fields[key] = value
		}
	}
	for key, value := range map[string]any{"operation_id": e.Record.ID, "operation": e.Record, "server_identity": e.Record.ServerIdentity, "cleanup_status": e.Record.Cleanup, "work_stopped": false} {
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return fields
}

type setupRemoteError struct {
	Record       operations.Record
	Code, Action string
}

func (e *setupRemoteError) Error() string { return e.Action }
func (e *setupRemoteError) ErrorMetadata() map[string]any {
	state := e.Record.State
	if e.Code == "setup_connection_pending" || e.Code == "server_identity_mismatch" || e.Code == "setup_cancellation_pending" || e.Code == "setup_observation_unavailable" {
		state = operations.Blocked
	}
	retryable := e.Record.Retryable || e.Code == "setup_observation_timeout" || e.Code == "setup_observation_unavailable" || e.Code == "setup_cancellation_pending"
	return map[string]any{"code": e.Code, "state": state, "operation_id": e.Record.ID, "server_identity": e.Record.ServerIdentity, "cleanup_status": e.Record.Cleanup, "worker": e.Record.Worker, "operation": e.Record, "action": e.Action, "retryable": retryable, "worker_stopped": e.Record.Worker != nil && !e.Record.Worker.Active, "work_stopped": e.Record.Worker != nil && !e.Record.Worker.Active && e.Record.State == operations.Succeeded && e.Record.Cleanup == "verified"}
}

func setupReferencePath(store operations.Store, id string) string {
	return filepath.Join(store.Root, id+".remote.json")
}
func saveSetupReference(store operations.Store, request serverSetupRequest) error {
	path := setupReferencePath(store, request.OperationID)
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(existing, data) {
			return operations.ErrConflict
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(store.Root, ".setup-reference-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Link(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(store.Root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func readSetupReference(store operations.Store, id string) (serverSetupRequest, error) {
	var request serverSetupRequest
	if !setupOperationID.MatchString(id) {
		return request, fmt.Errorf("invalid operation ID")
	}
	path := setupReferencePath(store, id)
	if err := privateSetupPath(path, false); err != nil {
		return request, err
	}
	file, err := os.Open(path)
	if err != nil {
		return request, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return request, fmt.Errorf("invalid setup reference")
	}
	if json.Unmarshal(data, &request) != nil || request.Version != 1 || request.OperationID != id || id != setupScopedOperationID(request.Owner, request.ServerIdentity, request.Key) {
		return request, fmt.Errorf("invalid setup reference")
	}
	record, err := store.Read(id)
	if err != nil {
		return request, err
	}
	normalized, _ := json.Marshal(request.Input)
	hash := sha256.Sum256(normalized)
	if record.InputHash != hex.EncodeToString(hash[:]) || record.ServerIdentity != request.ServerIdentity || record.Context != request.Input.Context || record.Owner != request.Owner {
		return request, fmt.Errorf("setup reference binding changed")
	}
	return request, nil
}
func setupReferenceSSH(request serverSetupRequest) (*bootstrapSSHClient, bootstrapOptions, error) {
	input := request.Input
	options := bootstrapOptions{host: input.Host, sshUser: input.User, sshPort: input.Port, identity: input.Identity}
	ssh, err := newBootstrapSSHClient(options)
	return ssh, options, err
}
func verifyRemoteSetupIdentity(ctx context.Context, ssh *bootstrapSSHClient, expected string) error {
	output, err := ssh.runArgs(ctx, []string{"cat", "/etc/machine-id"}, nil)
	if err != nil {
		return fmt.Errorf("cannot verify setup server identity")
	}
	identity, err := hostidentity.FromMachineID(string(bytes.TrimSpace([]byte(output))))
	if err != nil || identity != expected {
		return fmt.Errorf("setup server identity changed; stop before remote changes")
	}
	return nil
}
func remoteSetupCall(ctx context.Context, ssh *bootstrapSSHClient, options bootstrapOptions, request serverSetupRequest, action string) (operations.Record, error) {
	var record operations.Record
	if err := verifyRemoteSetupIdentity(ctx, ssh, request.ServerIdentity); err != nil {
		return record, err
	}
	output, err := ssh.runArgs(ctx, withSudoArgs(options, remoteSetupWorkerArgs(request, "__setup-worker", action, request.OperationID)), nil)
	if err != nil {
		return record, fmt.Errorf("cannot %s the server setup worker; inspect the same operation", action)
	}
	if json.Unmarshal([]byte(output), &record) != nil || record.Version != 1 || record.ID != request.OperationID || record.ServerIdentity != request.ServerIdentity || record.Owner != request.Owner || record.Context != request.Input.Context {
		return record, fmt.Errorf("invalid server setup observation")
	}
	normalized, _ := json.Marshal(request.Input)
	hash := sha256.Sum256(normalized)
	if record.InputHash != hex.EncodeToString(hash[:]) {
		return record, fmt.Errorf("server setup input binding changed")
	}
	if record.Worker == nil || record.Worker.Kind != "systemd" || record.Worker.Unit != setupUnit(record.ID) {
		return record, fmt.Errorf("missing server setup service observation")
	}
	record.ClientConnection = ""
	record.ClientConnectionVerifiedAt = nil
	return record, nil
}
func saveRemoteSetupObservation(store operations.Store, record *operations.Record) error {
	saved, err := store.SaveObservation(*record)
	if err == nil {
		*record = saved
	}
	return err
}

func setupScriptArgs(script string, args ...string) []string {
	command := []string{"sh", "-c", `script=$(printf '%s' "$1" | base64 --decode); shift; exec /bin/sh -c "$script" sh "$@"`, "sh", base64.StdEncoding.EncodeToString([]byte(script))}
	return append(command, args...)
}

const uploadSetupFileScript = `set -eu
umask 077
for directory in "$1" "$2"; do
 if [ -e "$directory" ] || [ -L "$directory" ]; then
  test -d "$directory" && test ! -L "$directory" && test "$(stat -c %u "$directory")" = "$6" && test "$(stat -c %a "$directory")" = 700
 else mkdir -m 700 -- "$directory"; fi
done
flock -x 9
if [ -e "$3" ] || [ -L "$3" ]; then
 test -f "$3" && test ! -L "$3" && test "$(stat -c %u "$3")" = "$6" && test "$(stat -c %a "$3")" = "$5"
 test "$(sha256sum -- "$3" | cut -d ' ' -f 1)" = "$4"
 cat >/dev/null
else
 temporary=$(mktemp "$2/.upload-XXXXXXXX")
 trap 'rm -f -- "$temporary"' EXIT HUP INT TERM
 cat > "$temporary"
 test "$(sha256sum -- "$temporary" | cut -d ' ' -f 1)" = "$4"
 chmod "$5" "$temporary"
 sync -f "$temporary"
 ln -- "$temporary" "$3"
 sync -f "$2"
fi
` // FD 9 is opened by the wrapper after the private directories exist.

const setupUploadDirectoryScript = `set -eu; umask 077
parent=$(dirname "$1")
if [ -e "$parent" ] || [ -L "$parent" ]; then test -d "$parent" && test ! -L "$parent" && test "$(stat -c %u "$parent")" = "$6" && test $((0$(stat -c %a "$parent") & 0022)) -eq 0; else mkdir -m 755 -- "$parent"; fi
for directory in "$1" "$2"; do
 if [ -e "$directory" ] || [ -L "$directory" ]; then test -d "$directory" && test ! -L "$directory" && test "$(stat -c %u "$directory")" = "$6" && test "$(stat -c %a "$directory")" = 700; else mkdir -m 700 -- "$directory"; fi
done
exec 9>"$2/upload.lock"
` + uploadSetupFileScript

func uploadSetupFile(ctx context.Context, ssh *bootstrapSSHClient, options bootstrapOptions, id, path, name, digest, mode string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	// The directories are validated before opening the upload lock. The body
	// validates them again before any immutable input is published.

	directory := filepath.Join(serverSetupRoot, id)
	_, err = ssh.runArgs(ctx, withSudoArgs(options, setupScriptArgs(setupUploadDirectoryScript, serverSetupRoot, directory, filepath.Join(directory, name), digest, mode, "0")), file)
	return err
}
func stageRemoteSetup(ctx context.Context, ssh *bootstrapSSHClient, options bootstrapOptions, request serverSetupRequest, bundle string) error {
	if err := verifyRemoteSetupIdentity(ctx, ssh, request.ServerIdentity); err != nil {
		return err
	}
	if err := uploadSetupFile(ctx, ssh, options, request.OperationID, request.Input.ClientBinary, "worker", request.Input.ClientSHA256, "700"); err != nil {
		return fmt.Errorf("cannot stage immutable setup worker")
	}
	output, err := ssh.runArgs(ctx, withSudoArgs(options, remoteSetupWorkerArgs(request, "__setup-worker", "probe")), nil)
	var probe struct {
		Version int `json:"setup_worker_protocol"`
	}
	if err != nil || json.Unmarshal([]byte(output), &probe) != nil || probe.Version != 1 {
		return &blockedRequirement{Code: "setup_worker_unsupported", Requirement: "server_setup_worker", Action: "use client and daemon artifacts with server setup worker protocol 1"}
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp("", "agentlab-setup-request-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	if err := uploadSetupFile(ctx, ssh, options, request.OperationID, temporary.Name(), "request.json", hex.EncodeToString(hash[:]), "600"); err != nil {
		return fmt.Errorf("cannot stage immutable setup request")
	}
	if err := uploadSetupFile(ctx, ssh, options, request.OperationID, bundle, "bundle.tgz", request.Input.BundleSHA256, "600"); err != nil {
		return fmt.Errorf("cannot stage immutable setup bundle")
	}
	return nil
}
func waitRemoteSetup(ctx context.Context, store operations.Store, request serverSetupRequest, ssh *bootstrapSSHClient, options bootstrapOptions, wait time.Duration) (operations.Record, error) {
	waitContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var last operations.Record
	for {
		current, err := remoteSetupCall(waitContext, ssh, options, request, "show")
		if err != nil {
			if last.ID == "" {
				last, _ = store.Read(request.OperationID)
			}
			return last, &setupRemoteError{Record: last, Code: "setup_observation_unavailable", Action: "setup can remain active on the server; inspect the same operation before resuming"}
		}
		if err := saveRemoteSetupObservation(store, &current); err != nil {
			return current, err
		}
		last = current
		if current.State == operations.Succeeded {
			return current, nil
		}
		if current.Worker != nil && !current.Worker.Active {
			return last, &setupRemoteError{Record: last, Code: "setup_requires_inspection", Action: "inspect the server operation and retained resources, then resume the same operation"}
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-waitContext.Done():
			timer.Stop()
			return last, &setupRemoteError{Record: last, Code: "setup_observation_timeout", Action: "setup can remain active on the server; use operation show or operation resume with this ID"}
		case <-timer.C:
		}
	}
}
func completeSetupConnection(ctx context.Context, request serverSetupRequest, ssh *bootstrapSSHClient, options bootstrapOptions) error {
	if err := verifyRemoteSetupIdentity(ctx, ssh, request.ServerIdentity); err != nil {
		return err
	}
	output, err := ssh.runArgs(ctx, withSudoArgs(options, remoteSetupWorkerArgs(request, "init", "--json")), nil)
	if err != nil {
		return fmt.Errorf("cannot read the completed server connection")
	}
	endpoint, token, err := parseInitConnect(output)
	if err != nil {
		return err
	}
	client := newAPIClient(clientOptions{Endpoint: endpoint, Token: token}, defaultRequestTimeout)
	if _, err := client.doJSON(ctx, "GET", "/v1/status", nil); err != nil {
		return fmt.Errorf("setup finished, but its remote connection does not authenticate")
	}
	if err := verifySetupIdentity(ctx, client, request.ServerIdentity); err != nil {
		return err
	}
	contexts, err := contextStore()
	if err != nil {
		return err
	}
	_, jumpHost := splitUserHost(request.Input.Host)
	settings := connection.Settings{ServerIdentity: request.ServerIdentity, Endpoint: endpoint, Token: token, AllowInsecureHTTP: true, JumpHost: jumpHost, JumpUser: request.Input.User}
	if err := contexts.PutContext(ctx, request.Input.Context, settings); err != nil {
		return err
	}
	return contexts.Use(request.Input.Context)
}
func executeRemoteSetup(ctx context.Context, store operations.Store, request serverSetupRequest, bundle string, noWait bool, wait time.Duration) (operations.Record, error) {
	local, err := store.Read(request.OperationID)
	if err != nil {
		return local, err
	}
	if time.Now().After(local.KeyExpiresAt) {
		return local, operations.ErrExpired
	}
	ssh, options, err := setupReferenceSSH(request)
	if err != nil {
		return operations.Record{}, err
	}
	if record, err := checkSetupCancellation(ctx, store, request, ssh, options); err != nil {
		return record, err
	}
	// A completed or active server journal needs no new payload upload.
	probe, err := ssh.runArgs(ctx, withSudoArgs(options, shCommand(`if test -f "$1"; then printf present; else printf missing; fi`, filepath.Join(serverSetupRoot, "journal", request.OperationID+".json"))), nil)
	if err != nil {
		return operations.Record{}, err
	}
	if probe != "present" && probe != "missing" {
		return operations.Record{}, fmt.Errorf("invalid server setup journal inspection")
	}
	var record operations.Record
	if probe == "present" {
		record, err = remoteSetupCall(ctx, ssh, options, request, "show")
		if err != nil {
			return record, err
		}
		if record.State == operations.Succeeded {
			if err := saveRemoteSetupObservation(store, &record); err != nil {
				return record, err
			}
			return finishRemoteSetup(ctx, store, request, ssh, options, record)
		}
	}
	if record.Worker == nil || !record.Worker.Active {
		if bundle != "" || probe != "present" {
			if bundle == "" {
				bundle, err = setupResumeBundle(request)
				if err != nil {
					return record, err
				}
				defer os.Remove(bundle)
			}
			if local.Worker == nil {
				planned, saveErr := store.Update(request.OperationID, func(r *operations.Record) error {
					r.Resources = appendSetupResource(r.Resources, operations.Resource{Kind: "setup_staging", ID: filepath.Join(serverSetupRoot, request.OperationID), State: "planned"})
					r.Cleanup = "pending"
					return nil
				})
				if saveErr != nil {
					return planned, saveErr
				}
			}
			if err := stageRemoteSetup(ctx, ssh, options, request, bundle); err != nil {
				return record, err
			}
		}
		if record, err := checkSetupCancellation(ctx, store, request, ssh, options); err != nil {
			return record, err
		}
		record, err = remoteSetupCall(ctx, ssh, options, request, "start")
		if err != nil {
			return record, err
		}
	}
	if err := saveRemoteSetupObservation(store, &record); err != nil {
		return record, err
	}
	if noWait {
		return record, nil
	}
	record, err = waitRemoteSetup(ctx, store, request, ssh, options, wait)
	if err != nil {
		return record, err
	}
	return finishRemoteSetup(ctx, store, request, ssh, options, record)
}

func setupResumeBundle(request serverSetupRequest) (string, error) {
	for _, binary := range []struct{ path, digest string }{{request.Input.ClientBinary, request.Input.ClientSHA256}, {request.Input.DaemonBinary, request.Input.DaemonSHA256}} {
		hash, err := setupFileHash(binary.path)
		if err != nil || hash != binary.digest {
			return "", &blockedRequirement{Code: "setup_payload_required", Requirement: "original_setup_artifacts", Action: "restore the original pinned binaries and assets before resuming setup staging"}
		}
	}
	bundle, err := createBootstrapBundle(request.Input.Assets, bootstrapBinaries{mode: "upload", arch: request.Input.Architecture, agentlab: request.Input.ClientBinary, agentlabd: request.Input.DaemonBinary})
	if err != nil {
		return "", err
	}
	hash, err := setupFileHash(bundle)
	if err != nil || hash != request.Input.BundleSHA256 {
		_ = os.Remove(bundle)
		return "", &blockedRequirement{Code: "setup_payload_changed", Requirement: "original_setup_artifacts", Action: "restore the original pinned setup assets; do not substitute another payload for this operation"}
	}
	return bundle, nil
}

func finishRemoteSetup(ctx context.Context, store operations.Store, request serverSetupRequest, ssh *bootstrapSSHClient, options bootstrapOptions, record operations.Record) (operations.Record, error) {
	if err := completeSetupConnection(ctx, request, ssh, options); err != nil {
		record.ClientConnection = "pending"
		record.ClientConnectionVerifiedAt = nil
		_ = saveRemoteSetupObservation(store, &record)
		var required *operations.Requirement
		if errors.As(err, &required) {
			return record, &setupRemoteError{Record: record, Code: required.Code, Action: required.Action}
		}
		return record, &setupRemoteError{Record: record, Code: "setup_connection_pending", Action: "server setup completed; resume this operation to verify and save its client connection"}
	}
	record.ClientConnection = "verified"
	verifiedAt := time.Now().UTC()
	record.ClientConnectionVerifiedAt = &verifiedAt
	if err := saveRemoteSetupObservation(store, &record); err != nil {
		return record, err
	}
	return record, nil
}

func checkSetupCancellation(ctx context.Context, store operations.Store, request serverSetupRequest, ssh *bootstrapSSHClient, options bootstrapOptions) (operations.Record, error) {
	if _, err := os.Lstat(filepath.Join(store.Root, request.OperationID+".cancel")); errors.Is(err, os.ErrNotExist) {
		return operations.Record{}, nil
	} else if err != nil {
		return operations.Record{}, err
	}
	record, err := remoteSetupCall(ctx, ssh, options, request, "cancel")
	if err != nil {
		local, _ := store.Read(request.OperationID)
		local.Worker = nil
		return local, &setupRemoteError{Record: local, Code: "setup_cancellation_pending", Action: "cancellation is saved locally; inspect the server operation and retained staging files"}
	}
	if err := saveRemoteSetupObservation(store, &record); err != nil {
		return record, err
	}
	return record, &setupRemoteError{Record: record, Code: "setup_cancellation_requested", Action: "inspect cancellation progress and retained resources before submitting another setup request"}
}

const verifySetupWorkerScript = `set -eu
parent=$(dirname "$1")
test -d "$parent" && test ! -L "$parent" && test "$(stat -c %u "$parent")" = "$5" && test $((0$(stat -c %a "$parent") & 0022)) -eq 0
for directory in "$1" "$2"; do
 test -d "$directory" && test ! -L "$directory" && test "$(stat -c %u "$directory")" = "$5" && test "$(stat -c %a "$directory")" = 700
done
test -f "$3" && test ! -L "$3" && test "$(stat -c %u "$3")" = "$5" && test "$(stat -c %a "$3")" = 700
test "$(sha256sum -- "$3" | cut -d ' ' -f 1)" = "$4"
worker="$3"
shift 5
exec "$worker" "$@"
`

func remoteSetupWorkerArgs(request serverSetupRequest, args ...string) []string {
	directory := filepath.Join(serverSetupRoot, request.OperationID)
	values := []string{serverSetupRoot, directory, filepath.Join(directory, "worker"), request.Input.ClientSHA256, "0"}
	values = append(values, args...)
	return setupScriptArgs(verifySetupWorkerScript, values...)
}
