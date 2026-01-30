package secrets

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

const BundleVersion = 1

// Bundle describes decrypted secrets content.
type Bundle struct {
	Version  int               `json:"version" yaml:"version"`
	Git      GitBundle         `json:"git,omitempty" yaml:"git,omitempty"`
	Env      map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Claude   ClaudeBundle      `json:"claude,omitempty" yaml:"claude,omitempty"`
	Artifact ArtifactBundle    `json:"artifact,omitempty" yaml:"artifact,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// GitBundle stores git-related credentials.
type GitBundle struct {
	Token         string `json:"token,omitempty" yaml:"token,omitempty"`
	Username      string `json:"username,omitempty" yaml:"username,omitempty"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty" yaml:"ssh_private_key,omitempty"`
	SSHPublicKey  string `json:"ssh_public_key,omitempty" yaml:"ssh_public_key,omitempty"`
	KnownHosts    string `json:"known_hosts,omitempty" yaml:"known_hosts,omitempty"`
}

// ClaudeBundle holds optional Claude Code settings fragments.
type ClaudeBundle struct {
	SettingsJSON string                 `json:"settings_json,omitempty" yaml:"settings_json,omitempty"`
	Settings     map[string]interface{} `json:"settings,omitempty" yaml:"settings,omitempty"`
}

// ArtifactBundle holds artifact upload parameters.
type ArtifactBundle struct {
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Token    string `json:"token,omitempty" yaml:"token,omitempty"`
}

// ClaudeSettingsJSON returns the settings fragment as JSON.
func (b Bundle) ClaudeSettingsJSON() (string, error) {
	if strings.TrimSpace(b.Claude.SettingsJSON) != "" {
		return b.Claude.SettingsJSON, nil
	}
	if len(b.Claude.Settings) == 0 {
		return "", nil
	}
	data, err := json.Marshal(b.Claude.Settings)
	if err != nil {
		return "", fmt.Errorf("marshal claude settings: %w", err)
	}
	return string(data), nil
}

// Store locates and decrypts secrets bundles.
type Store struct {
	Dir            string
	AgeKeyPath     string
	SopsPath       string
	AllowPlaintext bool
	SopsDecrypt    func(ctx context.Context, path string, env []string) ([]byte, error)
}

// Load locates, decrypts, and parses the bundle by name or path.
func (s Store) Load(ctx context.Context, name string) (Bundle, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Bundle{}, errors.New("bundle name is required")
	}
	path, err := s.resolvePath(name)
	if err != nil {
		return Bundle{}, err
	}
	payload, err := s.decrypt(ctx, path)
	if err != nil {
		return Bundle{}, err
	}
	bundle, err := parseBundle(payload)
	if err != nil {
		return Bundle{}, fmt.Errorf("parse bundle %s: %w", path, err)
	}
	return bundle, nil
}

func (s Store) resolvePath(name string) (string, error) {
	candidates := []string{}
	if filepath.IsAbs(name) {
		candidates = append(candidates, name)
	} else {
		if s.Dir != "" {
			candidates = append(candidates, filepath.Join(s.Dir, name))
		}
		candidates = append(candidates, name)
	}
	if filepath.Ext(name) != "" {
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("bundle %s not found", name)
	}
	for _, candidate := range candidates {
		if path, ok := findBundleFile(candidate, s.AllowPlaintext); ok {
			return path, nil
		}
	}
	return "", fmt.Errorf("bundle %s not found", name)
}

func (s Store) decrypt(ctx context.Context, path string) ([]byte, error) {
	lower := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(lower, ".age") {
		return decryptAge(path, s.AgeKeyPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bundle %s: %w", path, err)
	}
	if looksLikeSops(lower, data) {
		return s.decryptSops(ctx, path)
	}
	if s.AllowPlaintext {
		return data, nil
	}
	return nil, fmt.Errorf("bundle %s is not encrypted (.age or sops)", path)
}

func (s Store) decryptSops(ctx context.Context, path string) ([]byte, error) {
	if s.SopsDecrypt != nil {
		return s.SopsDecrypt(ctx, path, s.sopsEnv())
	}
	return decryptSops(ctx, s.sopsPath(), path, s.sopsEnv())
}

func (s Store) sopsPath() string {
	if strings.TrimSpace(s.SopsPath) != "" {
		return s.SopsPath
	}
	return "sops"
}

func (s Store) sopsEnv() []string {
	if strings.TrimSpace(s.AgeKeyPath) == "" {
		return nil
	}
	return []string{"SOPS_AGE_KEY_FILE=" + s.AgeKeyPath}
}

func findBundleFile(base string, allowPlain bool) (string, bool) {
	candidates := []string{
		base + ".age",
		base + ".sops.yaml",
		base + ".sops.yml",
		base + ".sops.json",
	}
	if allowPlain {
		candidates = append(candidates,
			base+".yaml",
			base+".yml",
			base+".json",
		)
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func looksLikeSops(name string, data []byte) bool {
	lower := strings.ToLower(name)
	if strings.Contains(lower, ".sops.") || strings.HasSuffix(lower, ".sops") {
		return true
	}
	if bytes.Contains(data, []byte("\nsops:")) || bytes.Contains(data, []byte("\nsops:\r\n")) {
		return true
	}
	return bytes.Contains(data, []byte(`"sops"`))
}

func parseBundle(data []byte) (Bundle, error) {
	var bundle Bundle
	if err := yaml.Unmarshal(data, &bundle); err != nil {
		return Bundle{}, err
	}
	if bundle.Version == 0 {
		bundle.Version = BundleVersion
	}
	if bundle.Version != BundleVersion {
		return Bundle{}, fmt.Errorf("unsupported bundle version %d", bundle.Version)
	}
	return bundle, nil
}

func decryptAge(path, keyPath string) ([]byte, error) {
	if strings.TrimSpace(keyPath) == "" {
		return nil, errors.New("age key path is required for .age bundles")
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read age key %s: %w", keyPath, err)
	}
	identities, err := parseAgeIdentities(keyData)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open bundle %s: %w", path, err)
	}
	defer file.Close()
	reader, err := age.Decrypt(file, identities...)
	if err != nil {
		return nil, fmt.Errorf("decrypt bundle %s: %w", path, err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read bundle %s: %w", path, err)
	}
	return payload, nil
}

func parseAgeIdentities(data []byte) ([]age.Identity, error) {
	var identities []age.Identity
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			continue
		}
		identity, err := age.ParseX25519Identity(line)
		if err != nil {
			return nil, fmt.Errorf("parse age identity: %w", err)
		}
		identities = append(identities, identity)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read age key: %w", err)
	}
	if len(identities) == 0 {
		return nil, errors.New("no age identities found")
	}
	return identities, nil
}

func decryptSops(ctx context.Context, sopsPath, bundlePath string, extraEnv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, sopsPath, "-d", bundlePath)
	cmd.Env = append(os.Environ(), extraEnv...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("sops decrypt %s: %w: %s", bundlePath, err, errMsg)
		}
		return nil, fmt.Errorf("sops decrypt %s: %w", bundlePath, err)
	}
	return stdout.Bytes(), nil
}
