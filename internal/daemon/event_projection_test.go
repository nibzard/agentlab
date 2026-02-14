package daemon

import (
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventProjectionReplayOutOfOrderAndDuplicateEvents(t *testing.T) {
	vmid := 1001
	jobID := "job-out-of-order"
	jobPayload := func(status string) any {
		return map[string]any{"status": status}
	}
	now := time.Date(2026, 02, 14, 10, 0, 0, 0, time.UTC)
	ordered := []db.Event{
		eventForProjection(t, 1, now.Add(1*time.Minute), EventKindSandboxState, &vmid, nil, "sandbox requested", map[string]any{
			"from_state": "REQUESTED",
			"to_state":   "PROVISIONING",
		}),
		eventForProjection(t, 2, now.Add(2*time.Minute), EventKindJobCreated, nil, &jobID, "job created", map[string]any{
			"status": "QUEUED",
		}),
		eventForProjection(t, 3, now.Add(3*time.Minute), EventKindJobRunning, nil, &jobID, "job running", map[string]any{
			"status": "RUNNING",
		}),
		eventForProjection(t, 4, now.Add(4*time.Minute), EventKindJobFailed, nil, &jobID, "job failed", jobPayload("FAILED")),
	}
	unsorted := []db.Event{
		ordered[3],
		ordered[1],
		ordered[2],
		ordered[0],
		eventForProjection(t, 3, now.Add(3*time.Minute), EventKindJobRunning, nil, &jobID, "duplicate ignored", map[string]any{
			"status": "RUNNING",
		}),
	}

	left := NewEventProjection()
	left.Replay(unsorted)

	right := NewEventProjection()
	right.Replay(ordered)

	assert.Equal(t, left.JobTimelines, right.JobTimelines)
	assert.Equal(t, left.SandboxHealth, right.SandboxHealth)
	assert.Equal(t, left.RecentFailureDigest, right.RecentFailureDigest)
}

func TestEventProjectionBuildsSandboxAndJobSummaries(t *testing.T) {
	vmid := 2021
	jobID := "job-summary"
	now := time.Date(2026, 02, 14, 11, 0, 0, 0, time.UTC)
	events := []db.Event{
		eventForProjection(t, 10, now.Add(10*time.Second), EventKindSandboxState, &vmid, nil, "", map[string]any{
			"from_state": "REQUESTED",
			"to_state":   stringPtr("PROVISIONING"),
		}),
		eventForProjection(t, 11, now.Add(20*time.Second), EventKindSandboxState, &vmid, nil, "", map[string]any{
			"from_state": "PROVISIONING",
			"to_state":   "READY",
		}),
		eventForProjection(t, 12, now.Add(30*time.Second), EventKindSandboxStartFailed, &vmid, nil, "start failure", map[string]any{
			"duration_ms": 120,
			"error":       "vm start timeout",
		}),
		eventForProjection(t, 13, now.Add(40*time.Second), EventKindJobCreated, nil, &jobID, "created", map[string]any{
			"status": "QUEUED",
		}),
		eventForProjection(t, 14, now.Add(50*time.Second), EventKindJobRunning, nil, &jobID, "running", nil),
		eventForProjection(t, 15, now.Add(60*time.Second), EventKindJobReport, nil, &jobID, "report failed", map[string]any{
			"status": "FAILED",
		}),
		eventForProjection(t, 16, now.Add(70*time.Second), EventKindJobFailed, nil, &jobID, "job failed", map[string]any{
			"status": "FAILED",
		}),
	}

	projection := NewEventProjection()
	projection.Replay(events)

	sandboxSummary, ok := projection.SandboxHealth[vmid]
	require.True(t, ok)
	assert.Equal(t, "READY", sandboxSummary.State)
	assert.False(t, sandboxSummary.Healthy)
	assert.Equal(t, 1, sandboxSummary.FailureCount)
	assert.Equal(t, EventKindSandboxStartFailed, sandboxSummary.LastFailureKind)

	jobSummary, ok := projection.JobTimelines[jobID]
	require.True(t, ok)
	assert.Equal(t, "FAILED", jobSummary.Status)
	assert.Equal(t, 4, jobSummary.EventCount)
	assert.Equal(t, 1, jobSummary.FailureCount)
	assert.Equal(t, string(models.JobFailed), jobSummary.Status)
	assert.NotEmpty(t, jobSummary.StartedAt)
	assert.NotEmpty(t, jobSummary.CompletedAt)

	require.Len(t, projection.RecentFailureDigest, 2)
	assert.Equal(t, EventKindJobFailed, projection.RecentFailureDigest[0].Kind)
	assert.Equal(t, EventKindSandboxStartFailed, projection.RecentFailureDigest[1].Kind)
}

func TestEventProjectionFiltersLatestFailures(t *testing.T) {
	vmid := 3003
	now := time.Date(2026, 02, 14, 12, 0, 0, 0, time.UTC)
	events := []db.Event{
		eventForProjection(t, 1, now.Add(1*time.Second), EventKindJobCreated, nil, stringPtr("job-a"), "created", map[string]any{
			"status": "QUEUED",
		}),
		eventForProjection(t, 2, now.Add(2*time.Second), EventKindJobFailed, nil, stringPtr("job-a"), "failed", map[string]any{
			"status": "FAILED",
			"error":  "bad",
		}),
		eventForProjection(t, 3, now.Add(3*time.Second), EventKindSandboxState, &vmid, nil, "state", map[string]any{
			"from_state": "READY",
			"to_state":   "RUNNING",
		}),
		eventForProjection(t, 4, now.Add(4*time.Second), EventKindSandboxStartFailed, &vmid, nil, "failed", map[string]any{
			"duration_ms": 1,
			"error":       "failed again",
		}),
	}

	projection := NewEventProjection()
	projection.Replay(events)

	require.Len(t, projection.RecentFailureDigest, 2)
	assert.Equal(t, EventKindJobFailed, projection.RecentFailureDigest[0].Kind)
	assert.Equal(t, EventKindSandboxStartFailed, projection.RecentFailureDigest[1].Kind)
}

func eventForProjection(t *testing.T, id int64, ts time.Time, kind EventKind, vmid *int, jobID *string, msg string, payload any) db.Event {
	t.Helper()
	var payloadJSON string
	if payload == nil {
		payloadJSON = `{}`
	} else {
		raw, err := NewEventPayloadForKind(kind, payload)
		require.NoError(t, err)
		payloadJSON = raw
	}
	return db.Event{
		ID:          id,
		Timestamp:   ts,
		Kind:        string(kind),
		SandboxVMID: vmid,
		JobID:       jobID,
		Message:     msg,
		JSON:        payloadJSON,
	}
}

func stringPtr(value string) *string {
	return &value
}
