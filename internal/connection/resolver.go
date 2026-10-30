package connection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Overrides distinguishes an omitted value from an explicit empty value.
// Socket selects local trust only when explicitly supplied by the caller.
type Overrides struct {
	Endpoint          *string
	Token             *string
	Socket            *string
	AllowInsecureHTTP *bool
}

type ResolutionError struct{ Code, Requirement, Action string }

func (e *ResolutionError) Error() string { return e.Action }
func (e *ResolutionError) ErrorMetadata() map[string]any {
	return map[string]any{"state": "blocked", "code": e.Code, "requirement": e.Requirement, "action": e.Action, "retryable": false}
}

type PrepareOptions struct {
	Root            string
	Context         string
	Environment     Overrides
	DefaultSocket   string
	AllowUnselected bool
	// Legacy can preserve an interface's established permission checks.
	Legacy func() (Settings, error)
}

// Selection captures public context metadata once. Credential loading happens
// after all interface flags have been parsed, and cannot select another context.
type Selection struct {
	store         ContextStore
	bound         *Context
	saved         Settings
	name          string
	environment   Overrides
	socket        string
	requireTarget bool
}

type Resolved struct {
	Settings
	SocketPath        string
	ContextName       string
	CredentialBinding *CredentialBinding `json:"-"`
}

// CredentialBinding identifies the saved source of a resolved credential.
// It is absent for explicit credentials, legacy files, and local sockets.
type CredentialBinding struct {
	Root    string
	Context Context
}

func EnvironmentOverrides() Overrides {
	result := Overrides{}
	if value := strings.TrimSpace(os.Getenv("AGENTLAB_ENDPOINT")); value != "" {
		result.Endpoint = &value
	}
	if value := strings.TrimSpace(os.Getenv("AGENTLAB_TOKEN")); value != "" {
		result.Token = &value
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENTLAB_ALLOW_INSECURE_HTTP"))) {
	case "", "0", "false", "no", "off":
	default:
		allowed := true
		result.AllowInsecureHTTP = &allowed
	}
	return result
}

func Prepare(ctx context.Context, options PrepareOptions) (*Selection, error) {
	if !filepath.IsAbs(options.Root) {
		return nil, &ResolutionError{Code: "invalid_config_directory", Requirement: "configuration_directory", Action: "select an absolute configuration directory before resolving a connection"}
	}
	selection := &Selection{store: ContextStore{Root: options.Root}, environment: options.Environment, socket: options.DefaultSocket}
	if selection.socket == "" {
		selection.socket = "/run/agentlab/agentlabd.sock"
	}
	item, name, found, err := selection.store.LookupContext(ctx, options.Context, options.AllowUnselected)
	if err != nil {
		return nil, err
	}
	selection.name = name
	if found {
		selection.bound = &item
		selection.saved = Settings{Endpoint: item.Endpoint, ServerIdentity: item.ServerIdentity, AllowInsecureHTTP: item.AllowInsecureHTTP, JumpHost: item.JumpHost, JumpUser: item.JumpUser}
		return selection, nil
	}
	// An existing index with no selected context must not restore legacy data.
	if _, err := os.Lstat(filepath.Join(options.Root, "contexts.json")); err == nil {
		selection.requireTarget = true
		return selection, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	loader := options.Legacy
	if loader == nil {
		loader = func() (Settings, error) { return readLegacySettings(options.Root) }
	}
	selection.saved, err = loader()
	if err != nil {
		return nil, err
	}
	return selection, nil
}

func readLegacySettings(root string) (Settings, error) {
	var cfg Settings
	data, err := os.ReadFile(filepath.Join(root, "client.json"))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Settings{}, &ResolutionError{Code: "invalid_connection_settings", Requirement: "connection_settings", Action: "restore valid legacy connection settings before retrying"}
	}
	return cfg, nil
}

// Preview resolves the public target and explicit credentials only. It never
// opens a context credential store. Resolve completes that same selection.
func (s *Selection) Preview(overrides Overrides) (Resolved, error) {
	return s.resolve(context.Background(), overrides, false)
}
func (s *Selection) Resolve(ctx context.Context, overrides Overrides) (Resolved, error) {
	return s.resolve(ctx, overrides, true)
}

func (s *Selection) resolve(ctx context.Context, overrides Overrides, credentials bool) (Resolved, error) {
	result := Resolved{Settings: s.saved, ContextName: s.name, SocketPath: s.socket}
	result.Token = ""
	if overrides.Endpoint != nil && strings.TrimSpace(*overrides.Endpoint) != "" && overrides.Socket != nil {
		return Resolved{}, &ResolutionError{Code: "connection_conflict", Requirement: "one_connection_transport", Action: "select either --endpoint or --socket for this command"}
	}
	endpoint := s.saved.Endpoint
	if s.environment.Endpoint != nil {
		endpoint = *s.environment.Endpoint
	}
	if overrides.Endpoint != nil {
		endpoint = *overrides.Endpoint
	}
	if overrides.Socket != nil {
		result.Settings = Settings{}
		result.SocketPath = strings.TrimSpace(*overrides.Socket)
		if result.SocketPath == "" {
			result.SocketPath = "/run/agentlab/agentlabd.sock"
		}
		return result, nil
	}
	var err error
	endpoint, err = NormalizeEndpoint(endpoint)
	if err != nil {
		if credentials {
			return Resolved{}, err
		}
		endpoint = strings.TrimSpace(s.saved.Endpoint)
		if s.environment.Endpoint != nil {
			endpoint = *s.environment.Endpoint
		}
		if overrides.Endpoint != nil {
			endpoint = *overrides.Endpoint
		}
	}
	if credentials && s.requireTarget && endpoint == "" {
		return Resolved{}, &ResolutionError{Code: "context_required", Requirement: "server_context", Action: "select a context with agentlab context use NAME, or supply an explicit connection target"}
	}
	if overrides.Endpoint != nil && endpoint == "" {
		return Resolved{}, &ResolutionError{Code: "connection_target_required", Requirement: "connection_target", Action: "provide a non-empty --endpoint, or select a local --socket explicitly"}
	}
	savedEndpoint, err := NormalizeEndpoint(s.saved.Endpoint)
	same := err == nil && endpoint == savedEndpoint
	if !same {
		result.Settings = Settings{}
	}
	result.Endpoint = endpoint
	if s.environment.AllowInsecureHTTP != nil {
		result.AllowInsecureHTTP = *s.environment.AllowInsecureHTTP
	}
	if overrides.AllowInsecureHTTP != nil {
		result.AllowInsecureHTTP = *overrides.AllowInsecureHTTP
	}
	if s.environment.Token != nil {
		result.Token = strings.TrimSpace(*s.environment.Token)
	}
	if overrides.Token != nil {
		result.Token = strings.TrimSpace(*overrides.Token)
	}
	if credentials && same && endpoint != "" && s.environment.Token == nil && overrides.Token == nil {
		cfg := s.saved
		if s.bound != nil {
			cfg, err = s.store.LoadBoundContext(ctx, *s.bound)
			if err != nil {
				return Resolved{}, err
			}
			if result.AllowInsecureHTTP == s.bound.AllowInsecureHTTP {
				result.CredentialBinding = &CredentialBinding{Root: s.store.Root, Context: *s.bound}
			}
		}
		result.Token = cfg.Token
		result.JumpHost = cfg.JumpHost
		result.JumpUser = cfg.JumpUser
	}
	if endpoint == "" {
		result.Token = ""
		result.ServerIdentity = ""
		result.JumpHost = ""
		result.JumpUser = ""
	}
	if credentials {
		if err := ValidateEndpointPolicy(endpoint, result.AllowInsecureHTTP); err != nil {
			return Resolved{}, err
		}
	}
	return result, nil
}
