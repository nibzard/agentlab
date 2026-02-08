// ABOUTME: Suggestion helpers for commands, profiles, and VMIDs.
// ABOUTME: Provides fuzzy matching and context-aware error hints.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func rankSuggestions(needle string, candidates []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return nil
	}
	seen := make(map[string]struct{}, len(candidates))
	type scored struct {
		value    string
		distance int
		prefix   bool
		contains bool
	}
	var scoredValues []scored
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate)
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		prefix := strings.HasPrefix(lower, needle) || strings.HasPrefix(needle, lower)
		contains := strings.Contains(lower, needle)
		distance := levenshteinDistance(needle, lower)
		if !prefix && !contains && distance > 3 {
			continue
		}
		scoredValues = append(scoredValues, scored{
			value:    value,
			distance: distance,
			prefix:   prefix,
			contains: contains,
		})
	}
	if len(scoredValues) == 0 {
		return nil
	}
	sort.Slice(scoredValues, func(i, j int) bool {
		left, right := scoredValues[i], scoredValues[j]
		if left.prefix != right.prefix {
			return left.prefix
		}
		if left.contains != right.contains {
			return left.contains
		}
		if left.distance != right.distance {
			return left.distance < right.distance
		}
		return left.value < right.value
	})
	if len(scoredValues) > limit {
		scoredValues = scoredValues[:limit]
	}
	out := make([]string, len(scoredValues))
	for i, value := range scoredValues {
		out[i] = value.value
	}
	return out
}

func bestSuggestion(needle string, candidates []string) string {
	matches := rankSuggestions(needle, candidates, 1)
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func formatQuotedList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return strings.Join(quoted, ", ")
}

func nearestVMIDs(target int, sandboxes []sandboxResponse, limit int) []int {
	if limit <= 0 || target <= 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(sandboxes))
	type candidate struct {
		vmid  int
		delta int
	}
	candidates := make([]candidate, 0, len(sandboxes))
	for _, sb := range sandboxes {
		vmid := sb.VMID
		if vmid <= 0 {
			continue
		}
		if _, ok := seen[vmid]; ok {
			continue
		}
		seen[vmid] = struct{}{}
		delta := vmid - target
		if delta < 0 {
			delta = -delta
		}
		candidates = append(candidates, candidate{vmid: vmid, delta: delta})
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.delta != right.delta {
			return left.delta < right.delta
		}
		return left.vmid < right.vmid
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]int, len(candidates))
	for i, candidate := range candidates {
		out[i] = candidate.vmid
	}
	return out
}

func formatVMIDList(vmids []int) string {
	if len(vmids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(vmids))
	for _, vmid := range vmids {
		if vmid <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d", vmid))
	}
	return strings.Join(parts, ", ")
}

func unknownCommandError(command string, candidates []string) error {
	msg := fmt.Sprintf("unknown command %q", command)
	hints := []string{}
	if suggestion := bestSuggestion(command, candidates); suggestion != "" {
		hints = append(hints, fmt.Sprintf("did you mean %q?", suggestion))
	}
	return newCLIError(msg, "agentlab --help", hints...)
}

func unknownSubcommandError(parent, command string, candidates []string) error {
	msg := fmt.Sprintf("unknown %s command %q", parent, command)
	hints := []string{}
	if suggestion := bestSuggestion(command, candidates); suggestion != "" {
		hints = append(hints, fmt.Sprintf("did you mean %q?", suggestion))
	}
	return newCLIError(msg, fmt.Sprintf("agentlab %s --help", parent), hints...)
}

func wrapSandboxNotFound(ctx context.Context, client *apiClient, vmid int, err error) error {
	if err == nil || !isNotFoundError(err, "sandbox") {
		return err
	}
	msg := fmt.Sprintf("sandbox %d not found", vmid)
	hints := []string{}
	if client != nil {
		sandboxes, fetchErr := fetchSandboxes(ctx, client)
		if fetchErr == nil {
			nearest := nearestVMIDs(vmid, sandboxes, 3)
			if len(nearest) > 0 {
				hints = append(hints, fmt.Sprintf("closest VMIDs: %s", formatVMIDList(nearest)))
			}
		}
	}
	return wrapCLIError(err, msg, "agentlab sandbox list", hints...)
}

func wrapJobNotFound(jobID string, err error) error {
	if err == nil || !isNotFoundError(err, "job") {
		return err
	}
	msg := fmt.Sprintf("job %s not found", strings.TrimSpace(jobID))
	return wrapCLIError(err, msg, "agentlab job --help")
}

func wrapWorkspaceNotFound(workspace string, err error) error {
	if err == nil || !isNotFoundError(err, "workspace") {
		return err
	}
	msg := fmt.Sprintf("workspace %s not found", strings.TrimSpace(workspace))
	return wrapCLIError(err, msg, "agentlab workspace list")
}

func wrapUnknownProfileError(ctx context.Context, client *apiClient, profileName string, err error) error {
	if err == nil || !isUnknownProfileError(err) {
		return err
	}
	profileName = strings.TrimSpace(profileName)
	msg := fmt.Sprintf("unknown profile %q", profileName)
	if client != nil {
		profiles, fetchErr := fetchProfiles(ctx, client)
		if fetchErr == nil {
			names := profileNameList(profiles)
			suggestions := rankSuggestions(profileName, names, 3)
			if len(suggestions) == 1 {
				msg = fmt.Sprintf("unknown profile %q (did you mean %q?)", profileName, suggestions[0])
			} else if len(suggestions) > 1 {
				msg = fmt.Sprintf("unknown profile %q. Did you mean one of: %s?", profileName, formatQuotedList(suggestions))
			} else if len(names) > 0 {
				msg = fmt.Sprintf("unknown profile %q. Available profiles: %s", profileName, strings.Join(names, ", "))
			}
		}
	}
	return wrapCLIError(err, msg, "agentlab profile list")
}

func isNotFoundError(err error, target string) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "not found") {
		return false
	}
	if target == "" {
		return true
	}
	return strings.Contains(msg, strings.ToLower(target))
}

func isUnknownProfileError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown profile")
}

func fetchSandboxes(ctx context.Context, client *apiClient) ([]sandboxResponse, error) {
	if client == nil {
		return nil, errors.New("client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/sandboxes", nil)
	if err != nil {
		return nil, err
	}
	var resp sandboxesResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, err
	}
	return resp.Sandboxes, nil
}
