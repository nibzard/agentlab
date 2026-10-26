package daemon

import (
	"encoding/json"
	"strings"
)

// V1JobOutcomeResponse projects only fields actually present in a stored report.
// Missing historical runner values remain absent, including a missing exit code.
type V1JobOutcomeResponse struct {
	ExitCode        *int     `json:"exit_code,omitempty"`
	DurationSeconds *float64 `json:"duration_seconds,omitempty"`
	Commit          string   `json:"commit,omitempty"`
	Message         string   `json:"message,omitempty"`
	ReportedAt      string   `json:"reported_at,omitempty"`
}

func jobOutcome(raw string) *V1JobOutcomeResponse {
	var report struct {
		Message    string          `json:"message"`
		ReportedAt string          `json:"reported_at"`
		Result     json.RawMessage `json:"result"`
	}
	if json.Unmarshal([]byte(raw), &report) != nil {
		return nil
	}
	outcome := V1JobOutcomeResponse{Message: report.Message, ReportedAt: report.ReportedAt}
	var runner map[string]json.RawMessage
	if json.Unmarshal(report.Result, &runner) == nil {
		if field, ok := runner["exit_code"]; ok {
			var code *int
			if json.Unmarshal(field, &code) == nil {
				outcome.ExitCode = code
			}
		}
		if field, ok := runner["duration_seconds"]; ok {
			var duration *float64
			if json.Unmarshal(field, &duration) == nil && duration != nil && *duration >= 0 {
				outcome.DurationSeconds = duration
			}
		}
		_ = json.Unmarshal(runner["commit"], &outcome.Commit)
	}
	if outcome.ExitCode == nil && outcome.DurationSeconds == nil && strings.TrimSpace(outcome.Commit) == "" && strings.TrimSpace(outcome.Message) == "" && outcome.ReportedAt == "" {
		return nil
	}
	return &outcome
}
