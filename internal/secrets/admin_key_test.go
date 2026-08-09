package secrets

import (
	"testing"
)

// TestNormalizedKeepsMintOnlyBundle guards the per-VM minting configuration: a
// tailscale section carrying only an admin api key (no shared auth key) must
// survive Normalized() so it round-trips through a write.
func TestNormalizedKeepsMintOnlyBundle(t *testing.T) {
	t.Parallel()
	bundle := Bundle{
		Tailscale: &TailscaleBundle{
			AdminAPIKey: "admin-api-key-fixture",
			Tailnet:     "example.com",
		},
	}
	got := bundle.Normalized()
	if got.Tailscale == nil {
		t.Fatal("mint-only tailscale bundle was normalized away")
	}
	if got.Tailscale.AdminAPIKey != "admin-api-key-fixture" {
		t.Fatalf("admin key = %q", got.Tailscale.AdminAPIKey)
	}
	if got.Tailscale.Tailnet != "example.com" {
		t.Fatalf("tailnet = %q", got.Tailscale.Tailnet)
	}
}

func TestNormalizedDropsEmptyTailscale(t *testing.T) {
	t.Parallel()
	// No auth key, no admin key, no tailnet/tags/template/args → nil.
	bundle := Bundle{Tailscale: &TailscaleBundle{}}
	if got := bundle.Normalized(); got.Tailscale != nil {
		t.Fatalf("expected nil tailscale, got %#v", got.Tailscale)
	}
	// A bare tailnet value alone still counts as configured (operator may set it
	// ahead of the admin key), so it must be retained.
	bundle = Bundle{Tailscale: &TailscaleBundle{Tailnet: "example.com"}}
	if got := bundle.Normalized(); got.Tailscale == nil {
		t.Fatal("expected tailscale retained when only tailnet is set")
	}
}

// TestRedactedScrubsAdminKey ensures the display projection scrubs both the
// shared auth key and the admin api key while leaving non-secret tailnet/tags
// intact.
func TestRedactedScrubsAdminKey(t *testing.T) {
	t.Parallel()
	bundle := Bundle{
		Tailscale: &TailscaleBundle{
			AdminAPIKey: "admin-api-key-fixture",
			AuthKey:     "shared-auth-key-fixture",
			Tailnet:     "example.com",
			Tags:        []string{"tag:agent"},
		},
	}
	redacted := bundle.Redacted()
	if redacted.Tailscale == nil {
		t.Fatal("tailscale nil after redaction")
	}
	if redacted.Tailscale.AdminAPIKey != "[REDACTED]" {
		t.Fatalf("admin key = %q want [REDACTED]", redacted.Tailscale.AdminAPIKey)
	}
	if redacted.Tailscale.AuthKey != "[REDACTED]" {
		t.Fatalf("auth key = %q want [REDACTED]", redacted.Tailscale.AuthKey)
	}
	if redacted.Tailscale.Tailnet != "example.com" {
		t.Fatalf("tailnet = %q want example.com (not secret)", redacted.Tailscale.Tailnet)
	}
	if len(redacted.Tailscale.Tags) != 1 || redacted.Tailscale.Tags[0] != "tag:agent" {
		t.Fatalf("tags altered by redaction: %#v", redacted.Tailscale.Tags)
	}
	// Redacting must not mutate the original bundle.
	if bundle.Tailscale.AdminAPIKey != "admin-api-key-fixture" {
		t.Fatalf("original bundle mutated: admin key = %q", bundle.Tailscale.AdminAPIKey)
	}
}

// TestMintingConfiguredHingesOnAdminKey pins the decision rule: the admin api
// key alone enables minting (the tailnet defaults to Tailscale's "-" wildcard).
func TestMintingConfiguredHingesOnAdminKey(t *testing.T) {
	t.Parallel()
	if (Bundle{Tailscale: &TailscaleBundle{Tailnet: "example.com"}}).TailscaleMintingConfigured() {
		t.Fatal("minting configured without admin key")
	}
	if (Bundle{Tailscale: &TailscaleBundle{AuthKey: "shared-auth-key-fixture"}}).TailscaleMintingConfigured() {
		t.Fatal("minting must not trigger on a shared auth key alone")
	}
	if !(Bundle{Tailscale: &TailscaleBundle{AdminAPIKey: "admin-api-key-fixture"}}).TailscaleMintingConfigured() {
		t.Fatal("expected minting configured with admin key only")
	}
	if (Bundle{}).TailscaleMintingConfigured() {
		t.Fatal("empty bundle reports minting configured")
	}
}

func TestGetTailscaleTailnetDefaultsToWildcard(t *testing.T) {
	t.Parallel()
	if got := (Bundle{}).GetTailscaleTailnet(); got != "-" {
		t.Fatalf("tailnet default = %q want -", got)
	}
	if got := (Bundle{Tailscale: &TailscaleBundle{}}).GetTailscaleTailnet(); got != "-" {
		t.Fatalf("empty tailscale tailnet = %q want -", got)
	}
	if got := (Bundle{Tailscale: &TailscaleBundle{Tailnet: "  acme.com  "}}).GetTailscaleTailnet(); got != "acme.com" {
		t.Fatalf("tailnet = %q want acme.com", got)
	}
}
