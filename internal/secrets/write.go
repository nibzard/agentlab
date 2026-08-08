package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Mutate loads the named bundle (or starts a fresh one if it does not yet
// exist), applies fn, normalizes the result, and writes it back to disk using
// the store's encryption policy. It returns the normalized resulting bundle and
// the path that was written.
//
// The write policy mirrors the historical CLI behaviour:
//   - .age: encrypted with age when an age key is configured and AllowPlaintext
//     is false (the secure default);
//   - .yaml/.yml/.json: plaintext, refused unless AllowPlaintext is true;
//   - .sops.*: not supported for writing.
//
// A brand-new bundle whose name has no extension resolves to <name>.age when an
// age key is configured (encrypted) or <name>.yaml otherwise (plaintext, still
// subject to AllowPlaintext).
//
// Both the CLI and the daemon's secrets-write API go through this method so the
// on-disk format and encryption rule have a single implementation.
func (s Store) Mutate(ctx context.Context, name string, fn func(*Bundle) error) (Bundle, string, error) {
	bundle, path, err := s.loadForMutation(ctx, name)
	if err != nil {
		return Bundle{}, "", err
	}
	if err := fn(&bundle); err != nil {
		return Bundle{}, "", err
	}
	bundle = bundle.Normalized()
	if err := s.writeBundle(path, bundle); err != nil {
		return Bundle{}, "", err
	}
	return bundle, path, nil
}

func (s Store) loadForMutation(ctx context.Context, name string) (Bundle, string, error) {
	path, err := s.ResolvePath(name)
	if err != nil {
		// A "not found" bundle is fine for a write: fall back to a new file path.
		if !strings.Contains(err.Error(), "not found") {
			return Bundle{}, "", err
		}
		path, err = s.defaultWritePath(name)
		if err != nil {
			return Bundle{}, "", err
		}
		return Bundle{Version: BundleVersion}, path, nil
	}
	bundle, err := s.Load(ctx, name)
	if err != nil {
		return Bundle{}, "", err
	}
	return bundle, path, nil
}

func (s Store) defaultWritePath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("bundle name is required")
	}
	base := name
	if !filepath.IsAbs(base) && strings.TrimSpace(s.Dir) != "" {
		base = filepath.Join(s.Dir, base)
	}
	if ext := filepath.Ext(base); ext != "" {
		return base, nil
	}
	if strings.TrimSpace(s.AgeKeyPath) != "" && !s.AllowPlaintext {
		return base + ".age", nil
	}
	return base + ".yaml", nil
}

func (s Store) writeBundle(path string, bundle Bundle) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("bundle path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	normalized := bundle.Normalized()
	lower := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(lower, ".age"):
		plaintext, err := MarshalYAML(normalized)
		if err != nil {
			return err
		}
		encrypted, err := EncryptAge(plaintext, s.AgeKeyPath)
		if err != nil {
			return err
		}
		return os.WriteFile(path, encrypted, 0o600)
	case strings.Contains(lower, ".sops."):
		return fmt.Errorf("writing sops bundles is not supported yet; re-save as .age or plaintext")
	case strings.HasSuffix(lower, ".yaml"), strings.HasSuffix(lower, ".yml"):
		if !s.AllowPlaintext {
			return fmt.Errorf("refusing to write plaintext bundle %s without allow-plaintext", path)
		}
		data, err := MarshalYAML(normalized)
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	case strings.HasSuffix(lower, ".json"):
		if !s.AllowPlaintext {
			return fmt.Errorf("refusing to write plaintext bundle %s without allow-plaintext", path)
		}
		data, err := json.MarshalIndent(normalized, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		return os.WriteFile(path, data, 0o600)
	default:
		return fmt.Errorf("unsupported bundle format for %s", path)
	}
}
