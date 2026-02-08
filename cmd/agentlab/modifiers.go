// ABOUTME: Helpers for resolving sandbox profile modifiers and suggestions.
// ABOUTME: Supports +modifier syntax for sandbox creation.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func parseSandboxModifiers(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	mods := make([]string, 0, len(args))
	for _, arg := range args {
		if !strings.HasPrefix(arg, "+") {
			return nil, fmt.Errorf("unexpected argument %q (modifiers must come after flags and start with '+')", arg)
		}
		mod := strings.TrimSpace(strings.TrimPrefix(arg, "+"))
		if mod == "" {
			return nil, fmt.Errorf("modifier %q is empty", arg)
		}
		mods = append(mods, mod)
	}
	return mods, nil
}

func fetchProfiles(ctx context.Context, client *apiClient) ([]profileResponse, error) {
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/profiles", nil)
	if err != nil {
		return nil, err
	}
	var resp profilesResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, err
	}
	return resp.Profiles, nil
}

func resolveProfileFromModifiers(modifiers []string, profiles []profileResponse) (string, error) {
	normalized := normalizeModifiers(modifiers)
	if len(normalized) == 0 {
		return "", fmt.Errorf("no modifiers provided")
	}
	validMods := validModifiersFromProfiles(profiles)
	if len(validMods) == 0 {
		return "", fmt.Errorf("no modifiers available (no profiles loaded)")
	}
	validSet := make(map[string]struct{}, len(validMods))
	for _, mod := range validMods {
		validSet[mod] = struct{}{}
	}
	var unknown []string
	for _, mod := range normalized {
		if _, ok := validSet[mod]; !ok {
			unknown = append(unknown, mod)
		}
	}
	if len(unknown) > 0 {
		return "", fmt.Errorf("unknown modifier(s) %s. Valid modifiers: %s", formatModifierList(unknown), formatModifierList(validMods))
	}
	resolved := strings.Join(normalized, "-")
	actual, ok := lookupProfileName(resolved, profiles)
	if ok {
		return actual, nil
	}
	profileNames := profileNameList(profiles)
	if suggestion := suggestProfileName(resolved, profileNames); suggestion != "" {
		return "", fmt.Errorf("no profile matches modifiers %s (resolved to %q). Did you mean %q?", formatModifierList(normalized), resolved, suggestion)
	}
	return "", fmt.Errorf("no profile matches modifiers %s (resolved to %q). Available profiles: %s", formatModifierList(normalized), resolved, strings.Join(profileNames, ", "))
}

func validateProfileName(profile string, profiles []profileResponse) (string, error) {
	if profile == "" {
		return "", fmt.Errorf("profile is required")
	}
	actual, ok := lookupProfileName(profile, profiles)
	if ok {
		return actual, nil
	}
	profileNames := profileNameList(profiles)
	if suggestion := suggestProfileName(profile, profileNames); suggestion != "" {
		return "", fmt.Errorf("unknown profile %q (did you mean %q?)", profile, suggestion)
	}
	return "", fmt.Errorf("unknown profile %q. Available profiles: %s", profile, strings.Join(profileNames, ", "))
}

func normalizeModifiers(modifiers []string) []string {
	seen := make(map[string]struct{}, len(modifiers))
	out := make([]string, 0, len(modifiers))
	for _, mod := range modifiers {
		value := strings.ToLower(strings.TrimSpace(mod))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func validModifiersFromProfiles(profiles []profileResponse) []string {
	seen := make(map[string]struct{})
	var mods []string
	for _, profile := range profiles {
		for _, part := range strings.Split(profile.Name, "-") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			mods = append(mods, part)
		}
	}
	sort.Strings(mods)
	return mods
}

func formatModifierList(modifiers []string) string {
	if len(modifiers) == 0 {
		return ""
	}
	prefixed := make([]string, 0, len(modifiers))
	for _, mod := range modifiers {
		value := strings.TrimSpace(mod)
		if value == "" {
			continue
		}
		if !strings.HasPrefix(value, "+") {
			value = "+" + value
		}
		prefixed = append(prefixed, value)
	}
	return strings.Join(prefixed, ", ")
}

func lookupProfileName(name string, profiles []profileResponse) (string, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return "", false
	}
	for _, profile := range profiles {
		if strings.ToLower(profile.Name) == needle {
			return profile.Name, true
		}
	}
	return "", false
}

func profileNameList(profiles []profileResponse) []string {
	if len(profiles) == 0 {
		return nil
	}
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if strings.TrimSpace(profile.Name) == "" {
			continue
		}
		names = append(names, profile.Name)
	}
	sort.Strings(names)
	return names
}

func suggestProfileName(name string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return ""
	}
	best := ""
	bestScore := 0
	for _, candidate := range candidates {
		candLower := strings.ToLower(candidate)
		if strings.HasPrefix(candLower, needle) || strings.HasPrefix(needle, candLower) {
			return candidate
		}
		distance := levenshteinDistance(needle, candLower)
		if best == "" || distance < bestScore {
			best = candidate
			bestScore = distance
		}
	}
	if best != "" && bestScore <= 3 {
		return best
	}
	return ""
}

func levenshteinDistance(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	aRunes := []rune(a)
	bRunes := []rune(b)
	if len(aRunes) == 0 {
		return len(bRunes)
	}
	if len(bRunes) == 0 {
		return len(aRunes)
	}
	prev := make([]int, len(bRunes)+1)
	curr := make([]int, len(bRunes)+1)
	for j := 0; j <= len(bRunes); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(aRunes); i++ {
		curr[0] = i
		for j := 1; j <= len(bRunes); j++ {
			cost := 0
			if aRunes[i-1] != bRunes[j-1] {
				cost = 1
			}
			deletion := prev[j] + 1
			insertion := curr[j-1] + 1
			substitution := prev[j-1] + cost
			curr[j] = minInt(deletion, insertion, substitution)
		}
		prev, curr = curr, prev
	}
	return prev[len(bRunes)]
}

func minInt(values ...int) int {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, value := range values[1:] {
		if value < min {
			min = value
		}
	}
	return min
}
