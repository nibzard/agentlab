package daemon

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
)

const (
	defaultProjectionRecentFailureLimit = 10
)

type EventProjection struct {
	SandboxHealth       map[int]V1SandboxLifecycleSummary
	JobTimelines        map[string]V1JobTimelineSummary
	RecentFailureDigest []V1FailureDigest
	recentFailureLimit  int
	seenEventIDs        map[int64]struct{}
}

func NewEventProjection() *EventProjection {
	return &EventProjection{
		SandboxHealth:       make(map[int]V1SandboxLifecycleSummary),
		JobTimelines:        make(map[string]V1JobTimelineSummary),
		RecentFailureDigest: make([]V1FailureDigest, 0),
		recentFailureLimit:  defaultProjectionRecentFailureLimit,
		seenEventIDs:        make(map[int64]struct{}),
	}
}

func (p *EventProjection) Replay(events []db.Event) {
	if p == nil {
		return
	}
	ordered := make([]db.Event, len(events))
	copy(ordered, events)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].ID == ordered[j].ID {
			switch {
			case ordered[i].Timestamp.Before(ordered[j].Timestamp):
				return true
			case ordered[j].Timestamp.Before(ordered[i].Timestamp):
				return false
			case ordered[i].Kind < ordered[j].Kind:
				return true
			case ordered[i].Kind > ordered[j].Kind:
				return false
			case ordered[i].Message < ordered[j].Message:
				return true
			default:
				return false
			}
		}
		return ordered[i].ID < ordered[j].ID
	})
	for _, ev := range ordered {
		if _, exists := p.seenEventIDs[ev.ID]; exists {
			continue
		}
		p.applyEvent(ev)
		p.seenEventIDs[ev.ID] = struct{}{}
	}
}

func (p *EventProjection) applyEvent(ev db.Event) {
	if p == nil {
		return
	}
	ts := ev.Timestamp.UTC().Format(time.RFC3339Nano)
	if ts == "" {
		ts = time.Time{}.UTC().Format(time.RFC3339Nano)
	}
	payloadVersion, payloadStage, payloadFields, _ := parseEventProjectionPayload(ev.JSON)
	if payloadFields == nil {
		payloadFields = map[string]any{}
	}

	if ev.SandboxVMID != nil && *ev.SandboxVMID > 0 {
		p.applySandboxProjection(*ev.SandboxVMID, ev, ts, payloadFields)
	}
	if ev.JobID != nil && strings.TrimSpace(*ev.JobID) != "" {
		p.applyJobProjection(*ev.JobID, ev, ts, payloadFields)
	}
	if isFailureProjectionEvent(ev.Kind) {
		p.applyFailureDigest(ev, ts, payloadVersion, string(payloadStage), payloadFields)
	}
}

func (p *EventProjection) applySandboxProjection(vmid int, ev db.Event, ts string, payload map[string]any) {
	summary := p.SandboxHealth[vmid]
	summary.VMID = vmid
	summary.LastEventID = ev.ID
	summary.LastEventKind = strings.TrimSpace(ev.Kind)
	summary.LastEventAt = ts

	switch EventKind(ev.Kind) {
	case EventKindSandboxState:
		toState := strings.ToUpper(strings.TrimSpace(stringFromEventPayload(payload, "to_state")))
		fromState := strings.ToUpper(strings.TrimSpace(stringFromEventPayload(payload, "from_state")))
		state := toState
		if state == "" {
			state = fromState
		}
		if state != "" {
			summary.State = state
			summary.Healthy = isHealthySandboxState(state)
		}
		if state == string(models.SandboxFailed) || state == string(models.SandboxTimeout) {
			summary.FailureCount++
			summary.Healthy = false
			summary.LastFailureAt = ts
			summary.LastFailureKind = strings.TrimSpace(ev.Kind)
			summary.LastFailureMessage = firstNonEmpty(summaryFromPayload(payload, "error"), strings.TrimSpace(ev.Message))
		}
	case EventKindSandboxSLOReady, EventKindSandboxSLOSSHReady, EventKindSandboxSLOSSHFailed:
		// informational timing events, no state transition
	}
	if isFailureProjectionEvent(ev.Kind) {
		summary.FailureCount++
		summary.Healthy = false
		summary.LastFailureAt = ts
		summary.LastFailureKind = strings.TrimSpace(ev.Kind)
		summary.LastFailureMessage = firstNonEmpty(summaryFromPayload(payload, "error"), strings.TrimSpace(ev.Message))
	}
	if summary.State == "" {
		// keep zero value when no state transition event was observed.
	}
	p.SandboxHealth[vmid] = summary
}

func (p *EventProjection) applyJobProjection(jobID string, ev db.Event, ts string, payload map[string]any) {
	summary := p.JobTimelines[jobID]
	summary.JobID = strings.TrimSpace(jobID)
	summary.LastEventID = ev.ID
	summary.LastEventKind = strings.TrimSpace(ev.Kind)
	summary.LastEventAt = ts
	summary.EventCount++
	switch EventKind(ev.Kind) {
	case EventKindJobCreated:
		status := strings.ToUpper(strings.TrimSpace(stringFromEventPayload(payload, "status")))
		summary.Status = firstNonEmpty(status, string(models.JobQueued))
	case EventKindJobRunning:
		summary.Status = string(models.JobRunning)
		if summary.StartedAt == "" {
			summary.StartedAt = ts
		}
	case EventKindJobFailed:
		summary.Status = string(models.JobFailed)
		if summary.CompletedAt == "" {
			summary.CompletedAt = ts
		}
		summary.FailureCount++
	case EventKindJobReport:
		status := strings.ToUpper(strings.TrimSpace(stringFromEventPayload(payload, "status")))
		if status != "" {
			summary.Status = status
			switch status {
			case string(models.JobRunning):
				if summary.StartedAt == "" {
					summary.StartedAt = ts
				}
			case string(models.JobCompleted), string(models.JobFailed), string(models.JobTimeout):
				if summary.CompletedAt == "" {
					summary.CompletedAt = ts
				}
			}
		}
	case EventKindJobSLOStart:
		if summary.StartedAt == "" {
			summary.StartedAt = ts
		}
		if summary.Status == "" {
			summary.Status = string(models.JobRunning)
		}
	}
	if isFailureProjectionEvent(ev.Kind) {
		summary.FailureCount++
		summary.LastFailureAt = ts
		summary.LastFailureKind = strings.TrimSpace(ev.Kind)
		summary.LastFailureMessage = firstNonEmpty(summaryFromPayload(payload, "error"), strings.TrimSpace(ev.Message))
	}
	if summaryFromPayload(payload, "status") == string(models.JobTimeout) {
		summary.Status = string(models.JobTimeout)
	}
	p.JobTimelines[jobID] = summary
}

func (p *EventProjection) applyFailureDigest(ev db.Event, ts string, payloadVersion int, stage string, payload map[string]any) {
	digest := V1FailureDigest{
		EventID:   ev.ID,
		Timestamp: ts,
		Kind:      strings.TrimSpace(ev.Kind),
		Schema:    payloadVersion,
		Stage:     strings.TrimSpace(stage),
	}
	if ev.SandboxVMID != nil {
		vmid := *ev.SandboxVMID
		digest.SandboxVMID = &vmid
	}
	if ev.JobID != nil {
		digest.JobID = *ev.JobID
	}
	digest.Error = firstNonEmpty(summaryFromPayload(payload, "error"), strings.TrimSpace(ev.Message))
	digest.Message = firstNonEmpty(strings.TrimSpace(ev.Message), digest.Error)
	if len(p.RecentFailureDigest) >= p.recentFailureLimit {
		p.RecentFailureDigest = p.RecentFailureDigest[1:]
	}
	p.RecentFailureDigest = append(p.RecentFailureDigest, digest)
}

func parseEventProjectionPayload(raw string) (int, EventStage, map[string]any, bool) {
	version, stage, payloadData, ok := parseLegacyEventPayload(raw)
	if !ok {
		return 0, "", nil, false
	}
	payload := map[string]any{}
	if len(payloadData) == 0 {
		return version, stage, payload, true
	}
	var decoded map[string]any
	if err := json.Unmarshal(payloadData, &decoded); err != nil {
		return version, stage, payload, false
	}
	return version, stage, decoded, true
}

func isFailureProjectionEvent(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return false
	}
	if strings.Contains(kind, ".failed") || strings.Contains(kind, "failed") || strings.Contains(kind, "timeout") {
		return true
	}
	return false
}

func isHealthySandboxState(state string) bool {
	switch models.SandboxState(strings.ToUpper(state)) {
	case models.SandboxRunning, models.SandboxReady, models.SandboxSuspended, models.SandboxStopped, models.SandboxCompleted:
		return true
	default:
		return false
	}
}

func stringFromEventPayload(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}
	switch value := value.(type) {
	case string:
		return value
	default:
		return ""
	}
}

func summaryFromPayload(payload map[string]any, key string) string {
	return strings.TrimSpace(stringFromEventPayload(payload, key))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
