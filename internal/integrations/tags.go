package integrations

import (
	"fmt"
	"strings"
)

// maxTagLength bounds a single sandbox tag. Tags are matched in memory and
// persisted as a comma-joined string, so an unbounded length is both a store
// concern and a proxy-matching cost.
const maxTagLength = 64

// NormalizeTags validates, normalizes, and deduplicates tag values, returning
// them in stable first-seen order (review M6).
//
// Normalization policy:
//   - Each value is trimmed of surrounding whitespace and lowercased. Matching
//     is case-insensitive, so both sandbox tags and integration tag selectors
//     are stored and compared in lowercase to make "Prod" and "prod" attach
//     the same integration.
//   - Empty values (after trimming) are dropped silently.
//   - A tag may not contain a comma (the persistence delimiter), internal
//     whitespace, control characters, or exceed maxTagLength runes. An invalid
//     value yields an error identifying the offender.
//
// The returned slice is deduplicated preserving first-seen order so output is
// deterministic regardless of input ordering.
func NormalizeTags(tags []string) ([]string, error) {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		norm, err := normalizeTag(raw)
		if err != nil {
			return nil, err
		}
		if norm == "" {
			continue
		}
		if seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	return out, nil
}

// normalizeTag trims, lowercases, and validates a single tag value. An empty
// result (after trimming) is valid and returned as "".
func normalizeTag(raw string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(raw))
	if t == "" {
		return "", nil
	}
	if len(t) > maxTagLength {
		return "", fmt.Errorf("invalid tag %q: length exceeds %d characters", raw, maxTagLength)
	}
	for _, r := range t {
		switch {
		case r == ',':
			return "", fmt.Errorf("invalid tag %q: commas are not allowed", raw)
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f':
			return "", fmt.Errorf("invalid tag %q: internal whitespace is not allowed", raw)
		case r < 0x20 || r == 0x7f:
			return "", fmt.Errorf("invalid tag %q: control characters are not allowed", raw)
		}
	}
	return t, nil
}

// JoinTags joins normalized tags with the storage delimiter. It is the inverse
// of parseTags for values that have passed NormalizeTags.
func JoinTags(tags []string) string {
	return strings.Join(tags, ",")
}

// NormalizeTagSelector normalizes a single integration tag selector so that
// tag-mode attachment matches sandbox tags case-insensitively (review M6).
// It returns the empty string for empty input (caller treats as invalid).
func NormalizeTagSelector(selector string) (string, error) {
	norm, err := normalizeTag(selector)
	if err != nil {
		return "", err
	}
	if norm == "" {
		return "", fmt.Errorf("tag selector is empty")
	}
	return norm, nil
}
