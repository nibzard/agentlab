package secrets

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

const redactedValue = "[REDACTED]"

// Normalized returns a copy with canonical defaults and empty sections removed.
func (b Bundle) Normalized() Bundle {
	out := b
	if out.Version == 0 {
		out.Version = BundleVersion
	}
	if len(out.Env) == 0 {
		out.Env = nil
	}
	if len(out.Metadata) == 0 {
		out.Metadata = nil
	}
	if out.SSH != nil && len(out.SSH.Keys) == 0 {
		out.SSH = nil
	}
	if out.Tailscale != nil {
		if strings.TrimSpace(out.Tailscale.AuthKey) == "" &&
			len(out.Tailscale.Tags) == 0 &&
			strings.TrimSpace(out.Tailscale.HostnameTemplate) == "" &&
			len(out.Tailscale.ExtraArgs) == 0 {
			out.Tailscale = nil
		}
	}
	return out
}

// Redacted returns a copy with secret values scrubbed for display.
func (b Bundle) Redacted() Bundle {
	out := b.Normalized()
	if strings.TrimSpace(out.Git.Token) != "" {
		out.Git.Token = redactedValue
	}
	if strings.TrimSpace(out.Git.SSHPrivateKey) != "" {
		out.Git.SSHPrivateKey = redactedValue
	}
	if len(out.Env) > 0 {
		redacted := make(map[string]string, len(out.Env))
		for key := range out.Env {
			redacted[key] = redactedValue
		}
		out.Env = redacted
	}
	if strings.TrimSpace(out.Artifact.Token) != "" {
		out.Artifact.Token = redactedValue
	}
	if out.Tailscale != nil && strings.TrimSpace(out.Tailscale.AuthKey) != "" {
		copyValue := *out.Tailscale
		copyValue.AuthKey = redactedValue
		out.Tailscale = &copyValue
	}
	return out
}

// MarshalYAML encodes a bundle as YAML with stable indentation.
func MarshalYAML(bundle Bundle) ([]byte, error) {
	bundle = bundle.Normalized()
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(bundle); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncryptAge encrypts plaintext using recipients derived from the configured age key.
func EncryptAge(plaintext []byte, keyPath string) ([]byte, error) {
	recipients, err := loadAgeRecipients(keyPath)
	if err != nil {
		return nil, err
	}
	var encrypted bytes.Buffer
	writer, err := age.Encrypt(&encrypted, recipients...)
	if err != nil {
		return nil, fmt.Errorf("encrypt age payload: %w", err)
	}
	if _, err := writer.Write(plaintext); err != nil {
		return nil, fmt.Errorf("write age payload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close age payload: %w", err)
	}
	return encrypted.Bytes(), nil
}

func loadAgeRecipients(keyPath string) ([]age.Recipient, error) {
	if strings.TrimSpace(keyPath) == "" {
		return nil, fmt.Errorf("age key path is required")
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read age key %s: %w", keyPath, err)
	}
	scanner := bytes.NewBuffer(keyData)
	lines := strings.Split(scanner.String(), "\n")
	recipients := make([]age.Recipient, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			continue
		}
		identity, err := age.ParseX25519Identity(line)
		if err != nil {
			return nil, fmt.Errorf("parse age identity: %w", err)
		}
		recipients = append(recipients, identity.Recipient())
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no age recipients found in %s", keyPath)
	}
	return recipients, nil
}
