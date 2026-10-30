package connection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const keychainWorkerArgument = "__agentlab-keychain-worker-v1"
const keychainWorkerMarker = "AGENTLAB_KEYCHAIN_WORKER_V1"
const maxKeychainMessage = 192 << 10

type keychainRequest struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
	Scope     string `json:"scope"`
	Reference string `json:"reference,omitempty"`
	Data      []byte `json:"data,omitempty"`
}
type keychainResponse struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
	Scope     string `json:"scope"`
	Reference string `json:"reference,omitempty"`
	Status    string `json:"status"`
	Data      []byte `json:"data,omitempty"`
}
type keychainVault struct {
	scope string
	run   func(context.Context, []byte) ([]byte, error)
}

func (keychainVault) Name() string { return "macos-keychain" }

func newKeychainVault(ctx context.Context, root string, run func(context.Context, []byte) ([]byte, error)) (CredentialVault, error) {
	scope, err := credentialConfiguration(root)
	if err != nil {
		return nil, credentialFailure("credential_store_unavailable", "restore the absolute AgentLab configuration directory")
	}
	vault := keychainVault{scope: scope, run: run}
	if _, err := vault.call(ctx, "probe", "", nil); err != nil {
		return nil, err
	}
	return vault, nil
}
func (v keychainVault) Save(ctx context.Context, ref string, data []byte) error {
	_, err := v.call(ctx, "save", ref, data)
	return err
}
func (v keychainVault) Load(ctx context.Context, ref string) ([]byte, error) {
	return v.call(ctx, "load", ref, nil)
}
func (v keychainVault) Delete(ctx context.Context, ref string) error {
	_, err := v.call(ctx, "delete", ref, nil)
	return err
}

func validKeychainRequest(request keychainRequest) bool {
	if request.Version != 1 || len(request.Scope) != 64 || strings.Trim(request.Scope, "0123456789abcdef") != "" {
		return false
	}
	switch request.Operation {
	case "probe":
		return request.Reference == "" && len(request.Data) == 0
	case "save":
		return validCredentialReference(request.Reference) && len(request.Data) > 0 && len(request.Data) <= maxCredentialBytes
	case "load", "delete":
		return validCredentialReference(request.Reference) && len(request.Data) == 0
	default:
		return false
	}
}
func (v keychainVault) call(ctx context.Context, operation, ref string, data []byte) ([]byte, error) {
	request := keychainRequest{Version: 1, Operation: operation, Scope: v.scope, Reference: ref, Data: data}
	if len(data) > maxCredentialBytes {
		return nil, credentialFailure("credential_too_large", "use a connection credential record smaller than 128 KiB")
	}
	if !validKeychainRequest(request) {
		return nil, credentialFailure("invalid_credential_reference", "restore a valid credential reference before retrying")
	}
	work, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := work.Err(); err != nil {
		return nil, credentialFailure("credential_store_unavailable", "retry credential access when the operating system store is available")
	}
	encoded, _ := json.Marshal(request)
	output, err := v.run(work, encoded)
	if err != nil || work.Err() != nil {
		return nil, credentialFailure("credential_store_unavailable", "restore access to the operating system credential store before retrying; no prompt is opened")
	}
	var response keychainResponse
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var extra any
	if len(output) > maxKeychainMessage || decoder.Decode(&response) != nil || decoder.Decode(&extra) != io.EOF || response.Version != 1 || response.Operation != operation || response.Scope != v.scope || response.Reference != ref || len(response.Data) > maxCredentialBytes || (operation != "load" && len(response.Data) != 0) || (response.Status != "ok" && len(response.Data) != 0) {
		return nil, credentialFailure("invalid_credential_record", "restore this context's Keychain record or reconnect; preserve unexpected records")
	}
	switch response.Status {
	case "ok":
		return response.Data, nil
	case "unavailable":
		if operation == "probe" {
			return nil, errCredentialStoreUnavailable
		}
		return nil, credentialFailure("credential_store_unavailable", "restore the Keychain recorded by this context, or reconnect; no file fallback is used")
	case "locked":
		return nil, credentialFailure("credential_store_locked", "unlock the default Keychain through the operating system, then retry; AgentLab does not open a prompt")
	case "denied":
		return nil, credentialFailure("credential_store_denied", "restore this application's Keychain access through the operating system, or reconnect with explicit file storage")
	case "not_found":
		if operation == "delete" {
			return nil, nil
		}
		return nil, credentialFailure("credential_not_found", "restore the credential in its recorded Keychain, or reconnect with a replacement credential")
	case "exists":
		return nil, credentialFailure("credential_reference_exists", "preserve the existing Keychain credential and retry with a new reference")
	case "failed":
		return nil, credentialFailure("credential_store_unavailable", "the Keychain request failed; restore operating system access before retrying")
	case "ambiguous":
		return nil, credentialFailure("ambiguous_credential_reference", "preserve duplicate Keychain records and reconnect with a new reference")
	default:
		return nil, credentialFailure("invalid_credential_record", "restore this context's Keychain record or reconnect; preserve unexpected records")
	}
}

// The worker inherits only the macOS session settings needed by Security.framework.
// Credentials and loader overrides never enter its argument list or environment.
func keychainWorkerCommand(ctx context.Context) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, executable, keychainWorkerArgument)
	command.Env = []string{keychainWorkerMarker + "=1", "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "GOTRACEBACK=none"}
	for _, name := range []string{"HOME", "USER", "LOGNAME", "TMPDIR", "LANG", "LC_ALL", "__CF_USER_TEXT_ENCODING", "SECURITYSESSIONID"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	return command, nil
}

type keychainOutput struct{ buffer bytes.Buffer }

func (b *keychainOutput) Write(data []byte) (int, error) {
	if len(data) > maxKeychainMessage-b.buffer.Len() {
		return 0, errors.New("credential worker response exceeds limit")
	}
	return b.buffer.Write(data)
}
func runKeychainProcess(command *exec.Cmd, input []byte) ([]byte, error) {
	var output keychainOutput
	command.Stdin = bytes.NewReader(input)
	command.Stdout = &output
	command.Stderr = io.Discard
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return nil, errors.New("credential worker did not finish")
	}
	return output.buffer.Bytes(), nil
}
func serveKeychainWorker(input io.Reader, output io.Writer, handle func(keychainRequest) keychainResponse) error {
	data, err := io.ReadAll(io.LimitReader(input, maxKeychainMessage+1))
	if err != nil || len(data) > maxKeychainMessage {
		return errors.New("invalid credential worker request")
	}
	var request keychainRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF || !validKeychainRequest(request) {
		return errors.New("invalid credential worker request")
	}
	response := handle(request)
	return json.NewEncoder(output).Encode(response)
}
