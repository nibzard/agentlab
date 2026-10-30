//go:build !darwin

package connection

import "context"

func openPlatformCredentialVault(ctx context.Context, root string) (CredentialVault, error) {
	return openSecretServiceVault(ctx, root)
}
