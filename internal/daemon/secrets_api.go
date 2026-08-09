package daemon

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/agentlab/agentlab/internal/secrets"
)

// errSecretsSSHKeyNotFound is returned by an SSH-key removal mutation when the
// named key is absent. The handler maps it to 404.
var errSecretsSSHKeyNotFound = errors.New("ssh key not found")

// SecretsAPI exposes the secrets bundle (LLM keys, git credentials, SSH keys,
// per-VM Tailscale enrollment) on the control/auth mux so a laptop-resident
// agent can register everything it needs over the remote endpoint before
// spinning up VMs — no local age key required.
//
// Endpoints (all bearer-token-protected remotely, open over the local socket):
//   - GET    /v1/secrets               - redacted bundle view
//   - PUT    /v1/secrets/env           - merge environment variables
//   - DELETE /v1/secrets/env/{key}     - remove one environment variable
//   - PUT    /v1/secrets/git           - merge git credentials
//   - PUT    /v1/secrets/tailscale     - set per-VM Tailscale enrollment
//   - DELETE /v1/secrets/tailscale     - clear Tailscale enrollment
//   - POST   /v1/secrets/ssh-keys      - add an SSH public key
//   - DELETE /v1/secrets/ssh-keys/{name} - remove an SSH public key
//
// Responses never include raw secret values; see V1SecretsView.
type SecretsAPI struct {
	store    secrets.Store
	bundle   string
	redactor *Redactor
	logger   *log.Logger
}

// NewSecretsAPI constructs a SecretsAPI bound to the named bundle (defaulting to
// "default"). The redactor receives any newly-staged secret values so they are
// scrubbed from subsequent log output, mirroring the bootstrap path.
func NewSecretsAPI(store secrets.Store, bundle string, redactor *Redactor, logger *log.Logger) *SecretsAPI {
	bundle = strings.TrimSpace(bundle)
	if bundle == "" {
		bundle = "default"
	}
	if logger == nil {
		logger = log.Default()
	}
	if redactor == nil {
		redactor = NewRedactor(nil)
	}
	return &SecretsAPI{store: store, bundle: bundle, redactor: redactor, logger: logger}
}

// Register mounts the secrets API routes onto the given mux.
func (api *SecretsAPI) Register(mux *http.ServeMux) {
	if mux == nil || api == nil {
		return
	}
	mux.HandleFunc("/v1/secrets", api.handleSecrets)
	mux.HandleFunc("/v1/secrets/env", api.handleEnv)
	mux.HandleFunc("/v1/secrets/env/", api.handleEnvKey)
	mux.HandleFunc("/v1/secrets/git", api.handleGit)
	mux.HandleFunc("/v1/secrets/tailscale", api.handleTailscale)
	mux.HandleFunc("/v1/secrets/ssh-keys", api.handleSSHKeys)
	mux.HandleFunc("/v1/secrets/ssh-keys/", api.handleSSHKeyName)
}

func (api *SecretsAPI) handleSecrets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		bundle, err := api.store.Load(r.Context(), api.bundle)
		if err != nil && !isSecretsNotFound(err) {
			api.logger.Printf("secrets load error: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to load secrets bundle")
			return
		}
		writeJSON(w, http.StatusOK, api.mutationResponse(bundle))
	default:
		writeMethodNotAllowed(w, []string{http.MethodGet})
	}
}

func (api *SecretsAPI) handleEnv(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		var req V1SecretsEnvSetRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if len(req.Env) == 0 {
			writeError(w, http.StatusBadRequest, "env map is required")
			return
		}
		bundle, _, err := api.store.Mutate(r.Context(), api.bundle, func(b *secrets.Bundle) error {
			if b.Env == nil {
				b.Env = map[string]string{}
			}
			for k, v := range req.Env {
				k = strings.TrimSpace(k)
				if k == "" {
					continue
				}
				b.Env[k] = v
			}
			return nil
		})
		if err != nil {
			api.writeMutateError(w, err)
			return
		}
		api.registerSecrets(bundle)
		writeJSON(w, http.StatusOK, api.mutationResponse(bundle))
	default:
		writeMethodNotAllowed(w, []string{http.MethodPut})
	}
}

func (api *SecretsAPI) handleEnvKey(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodDelete:
		key := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/v1/secrets/env/"))
		if key == "" {
			writeError(w, http.StatusBadRequest, "env key is required")
			return
		}
		bundle, _, err := api.store.Mutate(r.Context(), api.bundle, func(b *secrets.Bundle) error {
			if b.Env != nil {
				delete(b.Env, key)
			}
			return nil
		})
		if err != nil {
			api.writeMutateError(w, err)
			return
		}
		api.registerSecrets(bundle)
		writeJSON(w, http.StatusOK, api.mutationResponse(bundle))
	default:
		writeMethodNotAllowed(w, []string{http.MethodDelete})
	}
}

func (api *SecretsAPI) handleGit(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		var req V1SecretsGitSetRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		bundle, _, err := api.store.Mutate(r.Context(), api.bundle, func(b *secrets.Bundle) error {
			if t := strings.TrimSpace(req.Token); t != "" {
				b.Git.Token = t
			}
			if u := strings.TrimSpace(req.Username); u != "" {
				b.Git.Username = u
			}
			if k := strings.TrimSpace(req.SSHPrivateKey); k != "" {
				b.Git.SSHPrivateKey = k
			}
			if k := strings.TrimSpace(req.SSHPublicKey); k != "" {
				b.Git.SSHPublicKey = k
			}
			if h := strings.TrimSpace(req.KnownHosts); h != "" {
				b.Git.KnownHosts = h
			}
			return nil
		})
		if err != nil {
			api.writeMutateError(w, err)
			return
		}
		api.registerSecrets(bundle)
		writeJSON(w, http.StatusOK, api.mutationResponse(bundle))
	default:
		writeMethodNotAllowed(w, []string{http.MethodPut})
	}
}

func (api *SecretsAPI) handleTailscale(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		var req V1SecretsTailscaleSetRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		bundle, _, err := api.store.Mutate(r.Context(), api.bundle, func(b *secrets.Bundle) error {
			if b.Tailscale == nil {
				b.Tailscale = &secrets.TailscaleBundle{}
			}
			if t := strings.TrimSpace(req.AuthKey); t != "" {
				b.Tailscale.AuthKey = t
			}
			if h := strings.TrimSpace(req.HostnameTemplate); h != "" {
				b.Tailscale.HostnameTemplate = h
			}
			if len(req.Tags) > 0 {
				b.Tailscale.Tags = dedupeTailscaleStrings(req.Tags)
			}
			if len(req.ExtraArgs) > 0 {
				b.Tailscale.ExtraArgs = dedupeTailscaleStrings(req.ExtraArgs)
			}
			if k := strings.TrimSpace(req.AdminAPIKey); k != "" {
				b.Tailscale.AdminAPIKey = k
			}
			if t := strings.TrimSpace(req.Tailnet); t != "" {
				b.Tailscale.Tailnet = t
			}
			if strings.TrimSpace(b.Tailscale.AuthKey) == "" && strings.TrimSpace(b.Tailscale.AdminAPIKey) == "" {
				return errors.New("tailscale authkey or admin_api_key is required")
			}
			return nil
		})
		if err != nil {
			api.writeMutateError(w, err)
			return
		}
		api.registerSecrets(bundle)
		writeJSON(w, http.StatusOK, api.mutationResponse(bundle))
	case http.MethodDelete:
		bundle, _, err := api.store.Mutate(r.Context(), api.bundle, func(b *secrets.Bundle) error {
			b.Tailscale = nil
			return nil
		})
		if err != nil {
			api.writeMutateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, api.mutationResponse(bundle))
	default:
		writeMethodNotAllowed(w, []string{http.MethodPut, http.MethodDelete})
	}
}

func (api *SecretsAPI) handleSSHKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req V1SecretsSSHKeyAddRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		record, err := parseSecretsSSHPublicKey(req.Key)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		bundle, _, err := api.store.Mutate(r.Context(), api.bundle, func(b *secrets.Bundle) error {
			if b.SSH == nil {
				b.SSH = &secrets.SSHKeysBundle{Keys: map[string]secrets.SSHPublicKey{}}
			}
			if b.SSH.Keys == nil {
				b.SSH.Keys = map[string]secrets.SSHPublicKey{}
			}
			b.SSH.Keys[name] = record
			return nil
		})
		if err != nil {
			api.writeMutateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, api.mutationResponse(bundle))
	default:
		writeMethodNotAllowed(w, []string{http.MethodPost})
	}
}

func (api *SecretsAPI) handleSSHKeyName(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodDelete:
		name := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/v1/secrets/ssh-keys/"))
		if name == "" {
			writeError(w, http.StatusBadRequest, "ssh key name is required")
			return
		}
		bundle, _, err := api.store.Mutate(r.Context(), api.bundle, func(b *secrets.Bundle) error {
			if b.SSH == nil || len(b.SSH.Keys) == 0 {
				return errSecretsSSHKeyNotFound
			}
			if _, ok := b.SSH.Keys[name]; !ok {
				return errSecretsSSHKeyNotFound
			}
			delete(b.SSH.Keys, name)
			if len(b.SSH.Keys) == 0 {
				b.SSH = nil
			}
			return nil
		})
		if err != nil {
			api.writeMutateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, api.mutationResponse(bundle))
	default:
		writeMethodNotAllowed(w, []string{http.MethodDelete})
	}
}

// mutationResponse builds the standard redacted response for the given bundle.
func (api *SecretsAPI) mutationResponse(bundle secrets.Bundle) V1SecretsMutationResponse {
	path := ""
	if p, err := api.store.ResolvePath(api.bundle); err == nil {
		path = p
	}
	return V1SecretsMutationResponse{
		Bundle:  api.bundle,
		Path:    path,
		Secrets: redactedSecretsView(bundle),
	}
}

// registerSecrets feeds newly-staged env keys and secret values to the redactor
// so they are scrubbed from logs, mirroring bootstrap_api.go.
func (api *SecretsAPI) registerSecrets(bundle secrets.Bundle) {
	if api.redactor == nil {
		return
	}
	api.redactor.AddKeys(envKeys(bundle.Env)...)
	values := []string{bundle.Git.Token, bundle.Git.SSHPrivateKey}
	if authKey := bundle.GetTailscaleAuthKey(); authKey != "" {
		values = append(values, authKey)
	}
	if adminKey := bundle.GetTailscaleAdminAPIKey(); adminKey != "" {
		values = append(values, adminKey)
	}
	api.redactor.AddValues(values...)
}

// writeMutateError maps a Store.Mutate error to an appropriate HTTP status.
// Validation errors from the mutation closure are surfaced verbatim; write and
// load failures are logged and reported generically.
func (api *SecretsAPI) writeMutateError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		writeError(w, http.StatusNotFound, msg)
	case strings.Contains(msg, "is required") || strings.Contains(msg, "must "):
		writeError(w, http.StatusBadRequest, msg)
	case strings.Contains(msg, "allow-plaintext") || strings.Contains(msg, "not supported") || strings.Contains(msg, "unsupported bundle format"):
		api.logger.Printf("secrets mutate error: %v", err)
		writeError(w, http.StatusUnprocessableEntity, msg)
	default:
		api.logger.Printf("secrets mutate error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to update secrets bundle")
	}
}

func isSecretsNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

// redactedSecretsView projects a bundle into a response that exposes key names
// and non-secret metadata while replacing every sensitive value with
// "[REDACTED]". Public SSH keys are not secret and are included verbatim.
func redactedSecretsView(bundle secrets.Bundle) V1SecretsView {
	view := V1SecretsView{}
	if len(bundle.Env) > 0 {
		view.Env = make(map[string]string, len(bundle.Env))
		for k, v := range bundle.Env {
			if strings.TrimSpace(v) == "" {
				view.Env[k] = ""
			} else {
				view.Env[k] = redactedValue
			}
		}
	}
	git := bundle.Git
	if strings.TrimSpace(git.Token) != "" || strings.TrimSpace(git.Username) != "" ||
		strings.TrimSpace(git.SSHPrivateKey) != "" || strings.TrimSpace(git.SSHPublicKey) != "" ||
		strings.TrimSpace(git.KnownHosts) != "" {
		gv := &V1SecretsGitView{
			Username:     git.Username,
			SSHPublicKey: git.SSHPublicKey,
			KnownHosts:   git.KnownHosts,
		}
		if strings.TrimSpace(git.Token) != "" {
			gv.Token = redactedValue
		}
		if strings.TrimSpace(git.SSHPrivateKey) != "" {
			gv.SSHPrivateKey = redactedValue
		}
		view.Git = gv
	}
	if bundle.SSH != nil && len(bundle.SSH.Keys) > 0 {
		view.SSH = make(map[string]V1SecretsSSHKeyView, len(bundle.SSH.Keys))
		for name, k := range bundle.SSH.Keys {
			view.SSH[name] = V1SecretsSSHKeyView{Key: k.Key, Type: k.Type, Comment: k.Comment}
		}
	}
	if bundle.Tailscale != nil {
		view.Tailscale = &V1SecretsTailscaleView{
			HostnameTemplate:      bundle.Tailscale.HostnameTemplate,
			Tags:                  bundle.Tailscale.Tags,
			ExtraArgs:             bundle.Tailscale.ExtraArgs,
			Tailnet:               bundle.Tailscale.Tailnet,
			AuthKeyConfigured:     strings.TrimSpace(bundle.Tailscale.AuthKey) != "",
			AdminAPIKeyConfigured: strings.TrimSpace(bundle.Tailscale.AdminAPIKey) != "",
		}
	}
	return view
}

// parseSecretsSSHPublicKey validates an SSH public key string into a record. It
// mirrors cmd/agentlab.parseSSHPublicKey without the CLI's package boundary.
func parseSecretsSSHPublicKey(value string) (secrets.SSHPublicKey, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return secrets.SSHPublicKey{}, errors.New("ssh public key is required")
	}
	if strings.Contains(value, "\n") {
		return secrets.SSHPublicKey{}, errors.New("ssh public key must be a single line")
	}
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return secrets.SSHPublicKey{}, errors.New("ssh public key must include key type and data")
	}
	record := secrets.SSHPublicKey{
		Key:  value,
		Type: fields[0],
	}
	if len(fields) > 2 {
		record.Comment = strings.Join(fields[2:], " ")
	}
	return record, nil
}

// dedupeTailscaleStrings trims, drops empties, and de-duplicates a slice while
// preserving order, matching the CLI's dedupeNonEmpty helper.
func dedupeTailscaleStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
