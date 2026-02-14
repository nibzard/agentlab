package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func FuzzNormalizeEndpoint(f *testing.F) {
	seeds := []string{
		"",
		"example.com",
		"example.com:8845",
		"http://example.com",
		"http://example.com/",
		"https://example.com:8845",
		" https://example.com ",
		"http://[::1]:8845",
		"http://example.com/api",
		"ftp://example.com",
		"http://example.com?x=1",
		"http://example.com#frag",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got, err := normalizeEndpoint(raw)
		if err != nil {
			return
		}
		if strings.TrimSpace(raw) == "" {
			if got != "" {
				t.Fatalf("expected empty endpoint for blank input, got %q", got)
			}
			return
		}
		if got == "" {
			t.Fatalf("normalized endpoint should not be empty for %q", raw)
		}
		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatalf("normalized endpoint parse failed for %q: %v", got, err)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			t.Fatalf("unexpected scheme %q for %q", parsed.Scheme, got)
		}
		if parsed.Host == "" {
			t.Fatalf("missing host for %q", got)
		}
		if parsed.Path != "" {
			t.Fatalf("unexpected path %q for %q", parsed.Path, got)
		}
		if parsed.RawQuery != "" {
			t.Fatalf("unexpected query %q for %q", parsed.RawQuery, got)
		}
		if parsed.Fragment != "" {
			t.Fatalf("unexpected fragment %q for %q", parsed.Fragment, got)
		}
		if strings.HasSuffix(got, "/") {
			t.Fatalf("unexpected trailing slash for %q", got)
		}
		again, err := normalizeEndpoint(got)
		if err != nil {
			t.Fatalf("normalized endpoint should be idempotent: %v", err)
		}
		if again != got {
			t.Fatalf("expected idempotent normalization, got %q then %q", got, again)
		}
	})
}

func FuzzSlugifyWorkspaceName(f *testing.F) {
	seeds := []string{
		"my-workspace",
		"My Workspace",
		"feature/foo",
		"foo_bar",
		"  ",
		"---",
		"already-slugged",
		"UPPER123",
		"dash--dash",
		"ends-",
		"-starts",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		slug := slugifyWorkspaceName(value)
		if slug == "" {
			return
		}
		if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
			t.Fatalf("slug has leading/trailing dash: %q", slug)
		}
		if strings.Contains(slug, "--") {
			t.Fatalf("slug has consecutive dashes: %q", slug)
		}
		if slug != strings.ToLower(slug) {
			t.Fatalf("slug should be lowercase: %q", slug)
		}
		for _, r := range slug {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			t.Fatalf("slug contains invalid rune %q in %q", r, slug)
		}
		if got := slugifyWorkspaceName(slug); got != slug {
			t.Fatalf("slugify should be idempotent: %q -> %q", slug, got)
		}
	})
}

func FuzzSessionNameFromBranch(f *testing.F) {
	seeds := []string{
		"main",
		"feature/ABC-123",
		"bugfix login",
		"  ",
		"release/v1.2.3",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, branch string) {
		slug := slugifyWorkspaceName(branch)
		name, err := sessionNameFromBranch(branch)
		if slug == "" {
			if err == nil {
				t.Fatalf("expected error for branch %q", branch)
			}
			return
		}
		if err != nil {
			t.Fatalf("unexpected error for branch %q: %v", branch, err)
		}
		expected := "branch-" + slug
		if name != expected {
			t.Fatalf("unexpected session name: got %q want %q", name, expected)
		}
	})
}

func FuzzParseAPIError(f *testing.F) {
	f.Add(400, []byte(`{"error":"boom"}`))
	f.Add(500, []byte(`{}`))
	f.Add(418, []byte("not-json"))
	f.Fuzz(func(t *testing.T, status int, payload []byte) {
		if status < 100 || status > 999 {
			return
		}
	err := parseAPIError(status, payload)
	if err == nil {
		t.Fatalf("expected error for status %d", status)
	}
	var apiErr apiError
	if json.Unmarshal(payload, &apiErr) == nil && apiErr.Error != "" {
		msg := strings.TrimSpace(apiErr.Message)
		if msg == "" {
			msg = strings.TrimSpace(apiErr.Error)
		}
		if msg == "" {
			t.Fatalf("expected message in payload for status %d", status)
		}
		if err.Error() != msg {
			t.Fatalf("expected error %q, got %q", apiErr.Error, err.Error())
		}
		return
	}
	expected := fmt.Sprintf("request failed with status %d", status)
		if err.Error() != expected {
			t.Fatalf("expected error %q, got %q", expected, err.Error())
		}
	})
}
