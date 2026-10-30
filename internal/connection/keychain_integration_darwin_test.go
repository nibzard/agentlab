//go:build darwin && integration

package connection

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This uses the current user's default Keychain. It creates only random,
// configuration-scoped records and never unlocks or changes the Keychain.
func TestKeychainNativeLifecycle(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	vault, err := newKeychainVault(ctx, root, runKeychainWorker)
	require.NoError(t, err, "native acceptance needs an accessible, unlocked default Keychain")
	ref, err := newCredentialID()
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, vault.Delete(cleanup, ref))
	})
	data := []byte(`{"token":"native-acceptance-fixture"}`)
	require.NoError(t, vault.Save(ctx, ref, data))
	// Every read uses a new process. No inherited cache can satisfy it.
	reopened, err := newKeychainVault(ctx, root, runKeychainWorker)
	require.NoError(t, err)
	actual, err := reopened.Load(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, data, actual)
	require.Error(t, reopened.Save(ctx, ref, []byte("replacement")))
	actual, err = reopened.Load(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, data, actual)
	other, err := newKeychainVault(ctx, t.TempDir(), runKeychainWorker)
	require.NoError(t, err)
	require.NoError(t, other.Delete(ctx, ref))
	_, err = other.Load(ctx, ref)
	require.Error(t, err)
	require.NoError(t, reopened.Delete(ctx, ref))
	_, err = reopened.Load(ctx, ref)
	require.Error(t, err)
}

type terminatedKeychainVault struct{ CredentialVault }

func (v terminatedKeychainVault) Save(ctx context.Context, ref string, data []byte) error {
	if err := v.CredentialVault.Save(ctx, ref, data); err != nil {
		return err
	}
	os.Exit(42)
	return nil
}
func TestKeychainNativeTerminatedClient(t *testing.T) {
	root := os.Getenv("AGENTLAB_KEYCHAIN_CRASH_ROOT")
	if root == "" {
		t.Skip("native subprocess fixture")
	}
	vault, err := newKeychainVault(context.Background(), root, runKeychainWorker)
	require.NoError(t, err)
	store := ContextStore{Root: root, CredentialMode: "os", Vault: terminatedKeychainVault{CredentialVault: vault}}
	require.NoError(t, store.Put("native", Settings{Endpoint: "https://native-fixture.example", Token: "native-crash-fixture"}))
	t.Fatal("native fixture did not terminate after the Keychain write")
}
func TestKeychainNativeRecoversTerminatedClient(t *testing.T) {
	root := t.TempDir()
	store := ContextStore{Root: root, CredentialMode: "os"}
	// Verify access before starting the process and retain cleanup evidence on failure.
	_, err := newKeychainVault(context.Background(), root, runKeychainWorker)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.CleanupCredentials()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKeychainNativeTerminatedClient$")
	command.Env = append(os.Environ(), "AGENTLAB_KEYCHAIN_CRASH_ROOT="+root)
	output, err := command.CombinedOutput()
	var status *exec.ExitError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 42, status.ExitCode())
	require.NotContains(t, string(output), "native-crash-fixture")
	pending, err := store.PendingCredentials()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "macos-keychain", pending[0].CredentialStorage)
	vault, err := newKeychainVault(context.Background(), root, runKeychainWorker)
	require.NoError(t, err)
	raw, err := vault.Load(context.Background(), pending[0].CredentialRef)
	require.NoError(t, err)
	var record credentialRecord
	require.NoError(t, json.Unmarshal(raw, &record))
	require.Equal(t, pending[0].ID, record.TransactionID)
	require.NoError(t, store.CleanupCredentials())
	assertNoPendingCredentials(t, store)
	_, err = vault.Load(context.Background(), pending[0].CredentialRef)
	require.Error(t, err)
}
