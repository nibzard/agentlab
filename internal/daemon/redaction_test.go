package daemon

import (
	"strings"
	"testing"
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

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
