package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// Every shipped onboarding profile must survive the same loader the daemon uses.
func TestShippedDefaultProfilesLoad(t *testing.T) {
	data, err := os.ReadFile("../../scripts/profiles/defaults.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "defaults.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	profiles, err := LoadProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 6 {
		t.Fatalf("expected six documented profiles, got %d", len(profiles))
	}
	for name, profile := range profiles {
		if err := validateProfileForProvisioning(profile); err != nil {
			t.Errorf("shipped profile %s cannot provision: %v", name, err)
		}
	}
}
