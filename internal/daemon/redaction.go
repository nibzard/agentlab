package daemon

import (
	"regexp"
	"strings"
	"sync"
)

const redactedValue = "[REDACTED]"

var defaultRedactionKeys = []string{
	"token",
	"access_token",
	"refresh_token",
	"bootstrap_token",
	"artifact_token",
	"openai_api_key",
	"anthropic_api_key",
	"claude_api_key",
	"github_token",
	"gitlab_token",
	"bitbucket_token",
	"gitea_token",
	"git_token",
	"ssh_private_key",
	"private_key",
}

type redactPattern struct {
	re   *regexp.Regexp
	repl string
}

// Redactor scrubs sensitive values and key/value pairs from log lines.
type Redactor struct {
	mu       sync.RWMutex
	keySet   map[string]struct{}
	keys     []string
	valSet   map[string]struct{}
	values   []string
	patterns []redactPattern
}

// NewRedactor builds a redactor with defaults and optional extra keys.
func NewRedactor(extraKeys []string) *Redactor {
	r := &Redactor{
		keySet: make(map[string]struct{}),
		valSet: make(map[string]struct{}),
	}
	r.AddKeys(defaultRedactionKeys...)
	r.AddKeys(extraKeys...)
	return r
}

// AddKeys registers additional sensitive keys for redaction.
func (r *Redactor) AddKeys(keys ...string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	for _, key := range keys {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "" {
			continue
		}
		if _, ok := r.keySet[normalized]; ok {
			continue
		}
		r.keySet[normalized] = struct{}{}
		r.keys = append(r.keys, normalized)
		changed = true
	}
	if changed {
		r.patterns = buildKeyPatterns(r.keys)
	}
}

// AddValues registers literal sensitive values for redaction.
func (r *Redactor) AddValues(values ...string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || len(trimmed) < 6 {
			continue
		}
		if _, ok := r.valSet[trimmed]; ok {
			continue
		}
		r.valSet[trimmed] = struct{}{}
		r.values = append(r.values, trimmed)
	}
}

// Redact returns a scrubbed copy of input.
func (r *Redactor) Redact(input string) string {
	if r == nil || input == "" {
		return input
	}
	r.mu.RLock()
	values := append([]string(nil), r.values...)
	patterns := append([]redactPattern(nil), r.patterns...)
	r.mu.RUnlock()

	output := input
	for _, value := range values {
		output = strings.ReplaceAll(output, value, redactedValue)
	}
	for _, pattern := range patterns {
		output = pattern.re.ReplaceAllString(output, pattern.repl)
	}
	return output
}

func buildKeyPatterns(keys []string) []redactPattern {
	var patterns []redactPattern
	for _, key := range keys {
		escaped := regexp.QuoteMeta(key)
		patterns = append(patterns,
			redactPattern{
				re:   regexp.MustCompile(`(?i)("` + escaped + `"\s*:\s*")([^"]*)(")`),
				repl: `$1` + redactedValue + `$3`,
			},
			redactPattern{
				re:   regexp.MustCompile(`(?i)(\b` + escaped + `\b\s*=\s*")([^"]*)(")`),
				repl: `$1` + redactedValue + `$3`,
			},
			redactPattern{
				re:   regexp.MustCompile(`(?i)(\b` + escaped + `\b\s*=\s*')([^']*)(')`),
				repl: `$1` + redactedValue + `$3`,
			},
			redactPattern{
				re:   regexp.MustCompile(`(?i)(\b` + escaped + `\b\s*=\s*)([^\s"']+)`),
				repl: `$1` + redactedValue,
			},
			redactPattern{
				re:   regexp.MustCompile(`(?i)(\b` + escaped + `\b\s*:\s*)([^\s"']+)`),
				repl: `$1` + redactedValue,
			},
		)
	}
	return patterns
}

func envKeys(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		keys = append(keys, trimmed)
	}
	return keys
}
