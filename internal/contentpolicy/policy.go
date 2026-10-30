// Package contentpolicy identifies recognized credentials in transferred files.
package contentpolicy

import (
	"path"
	"regexp"
	"strings"
)

var credentialContent = regexp.MustCompile(`(?m)^-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----|(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{30,}|sk-(?:proj-|ant-)?[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|tskey-(?:auth|api)-[A-Za-z0-9-]{20,})|https?://[^/\s"'<>]+:[^/\s"'<>]+@`)
var credentialAssignment = regexp.MustCompile(`(?m)^(?:export[ \t]+)?[A-Z0-9_]*(?:TOKEN|PASSWORD|API_KEY|SECRET|PRIVATE_KEY)[A-Z0-9_]*[ \t]*=[ \t]*["']?[A-Za-z0-9_./+!@%:=?-]{8,}["']?[ \t]*(?:#[^\r\n]*)?$`)

var credentialField = regexp.MustCompile(`(?im)(?:^|[{,\n])[ \t]*["']?(?:password|access_token|refresh_token|api_key|client_secret|private_key|token|auth)["']?[ \t]*[:=][ \t]*["']?[A-Za-z0-9_./+!@%:=?-]{8,}["']?(?:[ \t]*[,}\r\n]|$)`)

// SensitiveContent reports recognized credential patterns in file contents.
func SensitiveContent(name string, data []byte) bool {
	if secretContent(data) {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".json", ".yaml", ".yml", ".toml", ".ini", ".conf", ".config", ".properties":
		return credentialField.Match(data)
	}
	return false
}

func secretContent(data []byte) bool {
	return credentialContent.Match(data) || credentialAssignment.Match(data)
}

// SensitivePath reports paths reserved for credentials or private metadata.
func SensitivePath(name string) bool {
	parts := strings.Split(strings.ToLower(name), "/")
	for _, part := range parts {
		switch part {
		case ".git", ".hg", ".svn", ".ssh", ".aws", ".gnupg", ".kube", ".terraform", ".docker":
			return true
		}
	}
	base := parts[len(parts)-1]
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasPrefix(base, ".env-") {
		return true
	}
	switch base {
	case ".git-credentials", "application_default_credentials.json", "secrets.json", "secrets.yaml", "secrets.yml", "auth.json", ".npmrc", ".pypirc", ".netrc", "_netrc", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "credentials", "credentials.json", "credentials.yaml", "credentials.yml", "service-account.json", "terraform.tfstate", "terraform.tfstate.backup":
		return true
	}
	switch path.Ext(base) {
	case ".pem", ".key", ".p12", ".pfx", ".jks", ".keystore":
		return true
	}
	return false
}
