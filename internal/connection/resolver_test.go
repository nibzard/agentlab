package connection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func pointer[T any](v T) *T { return &v }

func resolverFixture(t *testing.T) (ContextStore, *Selection) {
	t.Helper()
	store := ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	if err := store.Put("one", Settings{Endpoint: "https://one.example", Token: "one-secret", ServerIdentity: "one-id", JumpHost: "one-jump", JumpUser: "one-user", AllowInsecureHTTP: true}); err != nil {
		t.Fatal(err)
	}
	selected, err := Prepare(context.Background(), PrepareOptions{Root: store.Root})
	if err != nil {
		t.Fatal(err)
	}
	return store, selected
}

func TestResolverDoesNotReadUnusedCredential(t *testing.T) {
	store, selected := resolverFixture(t)
	items, _, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Root, "credentials", items[0].CredentialRef+".json")); err != nil {
		t.Fatal(err)
	}
	preview, err := selected.Preview(Overrides{})
	if err != nil || preview.Token != "" || preview.Endpoint != "https://one.example" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	for _, tt := range []struct {
		name            string
		override        Overrides
		endpoint, token string
	}{
		{"explicit token", Overrides{Token: pointer("replacement")}, "https://one.example", "replacement"},
		{"environment target", Overrides{Endpoint: pointer("https://replacement.example")}, "https://replacement.example", ""},
		{"local socket", Overrides{Socket: pointer("/tmp/selected.sock")}, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := selected.Resolve(context.Background(), tt.override)
			if err != nil || resolved.Endpoint != tt.endpoint || resolved.Token != tt.token {
				t.Fatalf("resolve: %+v %v", resolved, err)
			}
			if resolved.Endpoint != "https://one.example" && (resolved.ServerIdentity != "" || resolved.JumpHost != "" || resolved.JumpUser != "" || resolved.AllowInsecureHTTP) {
				t.Fatalf("inherited connection binding: %+v", resolved)
			}
			if tt.override.Socket != nil && resolved.SocketPath != *tt.override.Socket {
				t.Fatal("explicit socket ignored")
			}
		})
	}
	if _, err := selected.Resolve(context.Background(), Overrides{}); err == nil {
		t.Fatal("missing saved credential ignored")
	}
}

func TestResolverCapturesContextAndRejectsReplacement(t *testing.T) {
	store, selected := resolverFixture(t)
	if err := store.Put("two", Settings{Endpoint: "https://two.example", Token: "two-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Use("two"); err != nil {
		t.Fatal(err)
	}
	resolved, err := selected.Resolve(context.Background(), Overrides{Endpoint: pointer("https://one.example/")})
	if err != nil || resolved.Token != "one-secret" || resolved.ContextName != "one" {
		t.Fatalf("active switch changed captured selection: %+v %v", resolved, err)
	}
	if err := store.Put("one", Settings{Endpoint: "https://replacement.example", Token: "new-secret"}); err != nil {
		t.Fatal(err)
	}
	_, err = selected.Resolve(context.Background(), Overrides{})
	var changed *ResolutionError
	if !errors.As(err, &changed) || changed.Code != "context_changed" {
		t.Fatalf("context replacement not blocked: %v", err)
	}
}

func TestResolverTargetAndPolicy(t *testing.T) {
	_, selected := resolverFixture(t)
	tests := []struct {
		name      string
		overrides Overrides
		code      string
	}{
		{"conflicting transports", Overrides{Endpoint: pointer("https://two.example"), Socket: pointer("/tmp/local.sock")}, "connection_conflict"},
		{"empty endpoint", Overrides{Endpoint: pointer("")}, "connection_target_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := selected.Resolve(context.Background(), tt.overrides)
			var target *ResolutionError
			if !errors.As(err, &target) || target.Code != tt.code {
				t.Fatalf("unexpected resolution error: %v", err)
			}
		})
	}
	// A saved HTTP acknowledgement applies only to its original endpoint.
	if _, err := selected.Resolve(context.Background(), Overrides{Endpoint: pointer("http://two.example")}); err == nil {
		t.Fatal("new target inherited HTTP acknowledgement")
	}
	if _, err := selected.Resolve(context.Background(), Overrides{Endpoint: pointer("http://two.example"), AllowInsecureHTTP: pointer(true)}); err != nil {
		t.Fatal(err)
	}
}

func TestResolverEnvironmentTokenReplacesUnavailableSavedCredential(t *testing.T) {
	store, _ := resolverFixture(t)
	selected, err := Prepare(context.Background(), PrepareOptions{Root: store.Root, Environment: Overrides{Endpoint: pointer("https://other.example"), Token: pointer("environment-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := selected.Resolve(context.Background(), Overrides{Endpoint: pointer("https://explicit.example")})
	if err != nil || resolved.Endpoint != "https://explicit.example" || resolved.Token != "environment-secret" || resolved.ServerIdentity != "" {
		t.Fatalf("environment precedence: %+v %v", resolved, err)
	}
}

func TestContextRejectsCredentialBindingMismatch(t *testing.T) {
	for _, field := range []string{"endpoint", "server_identity", "credential_reference"} {
		t.Run(field, func(t *testing.T) {
			store, _ := resolverFixture(t)
			items, _, err := store.List()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.Root, "credentials", items[0].CredentialRef+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var record map[string]any
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			record[field] = "wrong-binding"
			data, err = json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cfg, _, _, err := store.Resolve("one")
			var binding *CredentialError
			if !errors.As(err, &binding) || binding.Code != "credential_binding_mismatch" || cfg.Token != "" {
				t.Fatalf("mismatch not blocked: %+v %v", cfg, err)
			}
		})
	}
}

func TestResolverPinsOnlySavedCredentialSources(t *testing.T) {
	store, selected := resolverFixture(t)
	ctx := context.Background()
	resolved, err := selected.Resolve(ctx, Overrides{})
	if err != nil || resolved.CredentialBinding == nil || resolved.CredentialBinding.Root != store.Root || resolved.CredentialBinding.Context.Name != "one" {
		t.Fatalf("saved source was not pinned: %v", err)
	}
	for _, overrides := range []Overrides{{Token: pointer("replacement")}, {Token: pointer("")}, {Socket: pointer("/tmp/other.sock")}, {Endpoint: pointer("https://another.example")}, {AllowInsecureHTTP: pointer(false)}} {
		resolved, err := selected.Resolve(ctx, overrides)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.CredentialBinding != nil {
			t.Fatal("an explicit override retained a refreshable saved source")
		}
	}
	selected, err = Prepare(ctx, PrepareOptions{Root: store.Root, Environment: Overrides{Token: pointer("environment")}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = selected.Resolve(ctx, Overrides{})
	if err != nil || resolved.CredentialBinding != nil {
		t.Fatal("an environment credential retained a saved source")
	}
}
