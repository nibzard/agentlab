package daemon

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRedactorRedactsKeysAndValues(t *testing.T) {
	redactor := NewRedactor([]string{"OPENAI_API_KEY"})
	redactor.AddValues("bootstrap-token-123")

	input := `token=bootstrap-token-123 OPENAI_API_KEY="sk-test" {"token":"bootstrap-token-123"} openai_api_key: sk-test`
	output := redactor.Redact(input)

	if output == input {
		t.Fatalf("expected redaction to modify output")
	}
	if containsAny(output, "bootstrap-token-123", "sk-test") {
		t.Fatalf("expected secrets to be redacted, got: %s", output)
	}
	if !containsAny(output, "token="+redactedValue, "OPENAI_API_KEY=\""+redactedValue+"\"", "openai_api_key: "+redactedValue) {
		t.Fatalf("expected redacted markers, got: %s", output)
	}
}

func TestRedactorNilRedactor(t *testing.T) {
	var r *Redactor

	// Should not panic on nil receiver
	input := "secret token value"
	output := r.Redact(input)

	// Nil redactor returns input unchanged
	assert.Equal(t, input, output)
}

func TestRedactorEmptyInput(t *testing.T) {
	redactor := NewRedactor(nil)
	redactor.AddValues("secret123")

	// Empty string should return empty
	output := redactor.Redact("")
	assert.Equal(t, "", output)
}

func TestRedactorAddKeys(t *testing.T) {
	tests := []struct {
		name         string
		initial      []string
		add          []string
		input        string
		wantRedacted bool
	}{
		{
			name:         "default keys redact common tokens",
			initial:      nil,
			add:          nil,
			input:        "access_token=abc123",
			wantRedacted: true,
		},
		{
			name:         "custom key is redacted",
			initial:      nil,
			add:          []string{"custom_key"},
			input:        "custom_key=value123",
			wantRedacted: true,
		},
		{
			name:         "case insensitive key matching",
			initial:      nil,
			add:          []string{"API_KEY"},
			input:        "api_key=secret",
			wantRedacted: true,
		},
		{
			name:         "key with whitespace is normalized",
			initial:      nil,
			add:          []string{"  key  "},
			input:        "key=value",
			wantRedacted: true,
		},
		{
			name:         "empty key is ignored",
			initial:      nil,
			add:          []string{""},
			input:        "key=value",
			wantRedacted: false,
		},
		{
			name:         "duplicate key is ignored",
			initial:      nil,
			add:          []string{"secret", "secret"},
			input:        "secret=value",
			wantRedacted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redactor := NewRedactor(tt.initial)
			redactor.AddKeys(tt.add...)
			output := redactor.Redact(tt.input)

			hasRedacted := strings.Contains(output, redactedValue)
			assert.Equal(t, tt.wantRedacted, hasRedacted)
		})
	}
}

func TestRedactorAddValues(t *testing.T) {
	tests := []struct {
		name         string
		values       []string
		input        string
		wantRedacted bool
	}{
		{
			name:         "value is redacted",
			values:       []string{"secret123"},
			input:        "my password is secret123",
			wantRedacted: true,
		},
		{
			name:         "short value is ignored",
			values:       []string{"abc"},
			input:        "value is abc",
			wantRedacted: false,
		},
		{
			name:         "whitespace-only value is ignored",
			values:       []string{"   "},
			input:        "some text",
			wantRedacted: false,
		},
		{
			name:         "empty value is ignored",
			values:       []string{""},
			input:        "some text",
			wantRedacted: false,
		},
		{
			name:         "value with whitespace is trimmed",
			values:       []string{"  secret123  "},
			input:        "password is secret123",
			wantRedacted: true,
		},
		{
			name:         "duplicate value is ignored",
			values:       []string{"secret", "secret"},
			input:        "the secret is out",
			wantRedacted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redactor := NewRedactor(nil)
			redactor.AddValues(tt.values...)
			output := redactor.Redact(tt.input)

			hasRedacted := strings.Contains(output, redactedValue)
			assert.Equal(t, tt.wantRedacted, hasRedacted)
		})
	}
}

func TestRedactorMultipleFormats(t *testing.T) {
	redactor := NewRedactor([]string{"password"})

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "JSON format",
			input: `{"password":"secret123"}`,
		},
		{
			name:  "shell assignment with double quotes",
			input: `password="secret123"`,
		},
		{
			name:  "shell assignment with single quotes",
			input: `password='secret123'`,
		},
		{
			name:  "shell assignment without quotes",
			input: `password=secret123`,
		},
		{
			name:  "YAML-like format with colon",
			input: `password: secret123`,
		},
		{
			name:  "case insensitive",
			input: `PASSWORD=secret123`,
		},
		{
			name:  "mixed case",
			input: `PassWord=secret123`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := redactor.Redact(tt.input)
			assert.NotContains(t, output, "secret123")
			assert.Contains(t, output, redactedValue)
		})
	}
}

func TestRedactorConcurrentAccess(t *testing.T) {
	redactor := NewRedactor([]string{"secret"})
	redactor.AddValues("value123")

	var wg sync.WaitGroup
	done := make(chan bool)

	// Start multiple goroutines adding keys and redacting
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				redactor.AddKeys("key" + string(rune(id)))
				redactor.Redact("secret=value123")
			}
		}(i)
	}

	// Wait for all to complete
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success, no deadlock or panic
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent test timed out")
	}
}

func TestRedactorDefaultKeys(t *testing.T) {
	redactor := NewRedactor(nil)

	tests := []struct {
		name         string
		input        string
		wantRedacted bool
	}{
		{
			name:         "token",
			input:        "token=abc123",
			wantRedacted: true,
		},
		{
			name:         "access_token",
			input:        "access_token=xyz",
			wantRedacted: true,
		},
		{
			name:         "refresh_token",
			input:        "refresh_token=xyz",
			wantRedacted: true,
		},
		{
			name:         "bootstrap_token",
			input:        "bootstrap_token=xyz",
			wantRedacted: true,
		},
		{
			name:         "artifact_token",
			input:        "artifact_token=xyz",
			wantRedacted: true,
		},
		{
			name:         "openai_api_key",
			input:        "openai_api_key=sk-xyz",
			wantRedacted: true,
		},
		{
			name:         "anthropic_api_key",
			input:        "anthropic_api_key=sk-ant",
			wantRedacted: true,
		},
		{
			name:         "claude_api_key",
			input:        "claude_api_key=sk-ant",
			wantRedacted: true,
		},
		{
			name:         "github_token",
			input:        "github_token=ghp_xyz",
			wantRedacted: true,
		},
		{
			name:         "ssh_private_key",
			input:        "ssh_private_key=xyz",
			wantRedacted: true,
		},
		{
			name:         "private_key",
			input:        "private_key=xyz",
			wantRedacted: true,
		},
		{
			name:         "non-sensitive key",
			input:        "username=john",
			wantRedacted: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := redactor.Redact(tt.input)
			hasRedacted := strings.Contains(output, redactedValue)
			assert.Equal(t, tt.wantRedacted, hasRedacted)
		})
	}
}

func TestRedactorComplexInput(t *testing.T) {
	redactor := NewRedactor([]string{"api_key", "secret"})
	redactor.AddValues("actual-secret-value")

	input := `Config: api_key="actual-secret-value", db_host=localhost
Env: secret=actual-secret-value, timeout=30s
JSON: {"api_key":"actual-secret-value","secret":"actual-secret-value"}
Shell: api_key=actual-secret-value secret=actual-secret-value`

	output := redactor.Redact(input)

	// The secret value should be completely removed
	assert.NotContains(t, output, "actual-secret-value")

	// But other values should remain
	assert.Contains(t, output, "db_host=localhost")
	assert.Contains(t, output, "timeout=30s")

	// Redaction markers should be present
	assert.Greater(t, strings.Count(output, redactedValue), 3)
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
