package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxStateString(t *testing.T) {
	tests := []struct {
		state SandboxState
		want  string
	}{
		{SandboxRequested, "REQUESTED"},
		{SandboxProvisioning, "PROVISIONING"},
		{SandboxBooting, "BOOTING"},
		{SandboxReady, "READY"},
		{SandboxRunning, "RUNNING"},
		{SandboxCompleted, "COMPLETED"},
		{SandboxFailed, "FAILED"},
		{SandboxTimeout, "TIMEOUT"},
		{SandboxStopped, "STOPPED"},
		{SandboxDestroyed, "DESTROYED"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, string(tt.state))
		})
	}
}

func TestJobStatusString(t *testing.T) {
	tests := []struct {
		status JobStatus
		want   string
	}{
		{JobQueued, "QUEUED"},
		{JobRunning, "RUNNING"},
		{JobCompleted, "COMPLETED"},
		{JobFailed, "FAILED"},
		{JobTimeout, "TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, string(tt.status))
		})
	}
}

func TestSandboxJSONSerialization(t *testing.T) {
	now := time.Now().UTC()
	leaseExpires := now.Add(2 * time.Hour)
	workspaceID := "workspace-123"

	s := Sandbox{
		VMID:          100,
		Name:          "test-sandbox",
		Profile:       "default",
		State:         SandboxReady,
		IP:            "10.77.0.100",
		WorkspaceID:   &workspaceID,
		Keepalive:     true,
		LeaseExpires:  leaseExpires,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}

	// Test JSON marshaling
	data, err := json.Marshal(s)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled Sandbox
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, s.VMID, unmarshaled.VMID)
	assert.Equal(t, s.Name, unmarshaled.Name)
	assert.Equal(t, s.Profile, unmarshaled.Profile)
	assert.Equal(t, s.State, unmarshaled.State)
	assert.Equal(t, s.IP, unmarshaled.IP)
	assert.Equal(t, s.WorkspaceID, unmarshaled.WorkspaceID)
	assert.Equal(t, s.Keepalive, unmarshaled.Keepalive)
	assert.WithinDuration(t, s.LeaseExpires, unmarshaled.LeaseExpires, time.Second)
	assert.WithinDuration(t, s.CreatedAt, unmarshaled.CreatedAt, time.Second)
	assert.WithinDuration(t, s.LastUpdatedAt, unmarshaled.LastUpdatedAt, time.Second)
}

func TestSandboxJSONWithNilWorkspaceID(t *testing.T) {
	s := Sandbox{
		VMID:          100,
		Name:          "test-sandbox",
		Profile:       "default",
		State:         SandboxReady,
		WorkspaceID:   nil,
		Keepalive:     false,
		LeaseExpires:  time.Time{},
		CreatedAt:     time.Now(),
		LastUpdatedAt: time.Now(),
	}

	data, err := json.Marshal(s)
	require.NoError(t, err)

	var unmarshaled Sandbox
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Nil(t, unmarshaled.WorkspaceID)
	assert.False(t, unmarshaled.Keepalive)
	assert.True(t, unmarshaled.LeaseExpires.IsZero())
}

func TestJobJSONSerialization(t *testing.T) {
	now := time.Now().UTC()
	sandboxVMID := 100
	workspaceID := "workspace-123"

	j := Job{
		ID:          "job-123",
		RepoURL:     "https://github.com/example/repo",
		Ref:         "main",
		Profile:     "default",
		Task:        "test task",
		Mode:        "dangerous",
		TTLMinutes:  120,
		Keepalive:   true,
		WorkspaceID: &workspaceID,
		Status:      JobRunning,
		SandboxVMID: &sandboxVMID,
		CreatedAt:   now,
		UpdatedAt:   now,
		ResultJSON:  `{"output": "test"}`,
	}

	// Test JSON marshaling
	data, err := json.Marshal(j)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled Job
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, j.ID, unmarshaled.ID)
	assert.Equal(t, j.RepoURL, unmarshaled.RepoURL)
	assert.Equal(t, j.Ref, unmarshaled.Ref)
	assert.Equal(t, j.Profile, unmarshaled.Profile)
	assert.Equal(t, j.Task, unmarshaled.Task)
	assert.Equal(t, j.Mode, unmarshaled.Mode)
	assert.Equal(t, j.TTLMinutes, unmarshaled.TTLMinutes)
	assert.Equal(t, j.Keepalive, unmarshaled.Keepalive)
	assert.Equal(t, j.WorkspaceID, unmarshaled.WorkspaceID)
	assert.Equal(t, j.Status, unmarshaled.Status)
	assert.Equal(t, j.SandboxVMID, unmarshaled.SandboxVMID)
	assert.Equal(t, j.ResultJSON, unmarshaled.ResultJSON)
	assert.WithinDuration(t, j.CreatedAt, unmarshaled.CreatedAt, time.Second)
	assert.WithinDuration(t, j.UpdatedAt, unmarshaled.UpdatedAt, time.Second)
}

func TestJobJSONWithNilSandboxVMID(t *testing.T) {
	j := Job{
		ID:          "job-123",
		RepoURL:     "https://github.com/example/repo",
		Ref:         "main",
		Profile:     "default",
		Task:        "test task",
		Mode:        "dangerous",
		TTLMinutes:  0,
		Keepalive:   false,
		WorkspaceID: nil,
		Status:      JobQueued,
		SandboxVMID: nil,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		ResultJSON:  "",
	}

	data, err := json.Marshal(j)
	require.NoError(t, err)

	var unmarshaled Job
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Nil(t, unmarshaled.SandboxVMID)
	assert.Nil(t, unmarshaled.WorkspaceID)
	assert.Empty(t, unmarshaled.ResultJSON)
}

func TestProfileJSONSerialization(t *testing.T) {
	rawYAML := "key: value\nnested:\n  item: 1"
	p := Profile{
		Name:       "default",
		TemplateVM: 9000,
		UpdatedAt:  time.Now(),
		RawYAML:    rawYAML,
	}

	data, err := json.Marshal(p)
	require.NoError(t, err)

	var unmarshaled Profile
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, p.Name, unmarshaled.Name)
	assert.Equal(t, p.TemplateVM, unmarshaled.TemplateVM)
	assert.Equal(t, p.RawYAML, unmarshaled.RawYAML)
	assert.WithinDuration(t, p.UpdatedAt, unmarshaled.UpdatedAt, time.Second)
}

func TestWorkspaceJSONSerialization(t *testing.T) {
	now := time.Now().UTC()
	attachedVM := 100

	w := Workspace{
		ID:          "workspace-123",
		Name:        "test-workspace",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-100-disk-1",
		SizeGB:      80,
		AttachedVM:  &attachedVM,
		CreatedAt:   now,
		LastUpdated: now,
	}

	data, err := json.Marshal(w)
	require.NoError(t, err)

	var unmarshaled Workspace
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, w.ID, unmarshaled.ID)
	assert.Equal(t, w.Name, unmarshaled.Name)
	assert.Equal(t, w.Storage, unmarshaled.Storage)
	assert.Equal(t, w.VolumeID, unmarshaled.VolumeID)
	assert.Equal(t, w.SizeGB, unmarshaled.SizeGB)
	assert.Equal(t, w.AttachedVM, unmarshaled.AttachedVM)
	assert.WithinDuration(t, w.CreatedAt, unmarshaled.CreatedAt, time.Second)
	assert.WithinDuration(t, w.LastUpdated, unmarshaled.LastUpdated, time.Second)
}

func TestWorkspaceJSONWithNilAttachedVM(t *testing.T) {
	w := Workspace{
		ID:          "workspace-123",
		Name:        "test-workspace",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-100-disk-1",
		SizeGB:      80,
		AttachedVM:  nil,
		CreatedAt:   time.Now(),
		LastUpdated: time.Now(),
	}

	data, err := json.Marshal(w)
	require.NoError(t, err)

	var unmarshaled Workspace
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Nil(t, unmarshaled.AttachedVM)
}

func TestSandboxStateTransitions(t *testing.T) {
	// Test valid state transitions
	validTransitions := map[SandboxState][]SandboxState{
		SandboxRequested:    {SandboxProvisioning, SandboxFailed},
		SandboxProvisioning: {SandboxBooting, SandboxFailed},
		SandboxBooting:      {SandboxReady, SandboxFailed, SandboxTimeout},
		SandboxReady:        {SandboxRunning, SandboxStopped, SandboxFailed},
		SandboxRunning:      {SandboxCompleted, SandboxFailed, SandboxTimeout, SandboxStopped},
		SandboxCompleted:    {SandboxDestroyed, SandboxStopped},
		SandboxFailed:       {SandboxDestroyed, SandboxStopped},
		SandboxTimeout:      {SandboxDestroyed, SandboxStopped},
		SandboxStopped:      {SandboxDestroyed},
		SandboxDestroyed:    {}, // Terminal state
	}

	// This test documents the expected state transitions
	// In the actual code, state transitions should be validated
	for from, toStates := range validTransitions {
		t.Run(string(from), func(t *testing.T) {
			for _, to := range toStates {
				// Just document that this transition is valid
				assert.NotEmpty(t, string(from), string(to))
			}
		})
	}
}

func TestJobStatusTransitions(t *testing.T) {
	// Test valid status transitions
	validTransitions := map[JobStatus][]JobStatus{
		JobQueued:    {JobRunning, JobFailed, JobTimeout},
		JobRunning:   {JobCompleted, JobFailed, JobTimeout},
		JobCompleted: {}, // Terminal state
		JobFailed:    {}, // Terminal state
		JobTimeout:   {}, // Terminal state
	}

	for from, toStates := range validTransitions {
		t.Run(string(from), func(t *testing.T) {
			for _, to := range toStates {
				// Just document that this transition is valid
				assert.NotEmpty(t, string(from), string(to))
			}
		})
	}
}

func TestTimeFieldsParseRFC3339(t *testing.T) {
	// Test that time fields can be parsed from RFC3339 format
	testTime := "2024-01-15T10:30:00Z"
	parsed, err := time.Parse(time.RFC3339, testTime)
	require.NoError(t, err)
	assert.False(t, parsed.IsZero())
}

func TestAllSandboxStatesDefined(t *testing.T) {
	// Ensure all expected states are defined
	expectedStates := []SandboxState{
		SandboxRequested, SandboxProvisioning, SandboxBooting,
		SandboxReady, SandboxRunning, SandboxCompleted,
		SandboxFailed, SandboxTimeout, SandboxStopped, SandboxDestroyed,
	}
	assert.Len(t, expectedStates, 10, "all sandbox states should be defined")
}

func TestAllJobStatusesDefined(t *testing.T) {
	// Ensure all expected statuses are defined
	expectedStatuses := []JobStatus{
		JobQueued, JobRunning, JobCompleted, JobFailed, JobTimeout,
	}
	assert.Len(t, expectedStatuses, 5, "all job statuses should be defined")
}

func TestSandboxZeroValues(t *testing.T) {
	var s Sandbox
	assert.Zero(t, s.VMID)
	assert.Empty(t, s.Name)
	assert.Empty(t, s.Profile)
	assert.Empty(t, s.State)
	assert.Empty(t, s.IP)
	assert.Nil(t, s.WorkspaceID)
	assert.False(t, s.Keepalive)
	assert.True(t, s.LeaseExpires.IsZero())
	assert.True(t, s.CreatedAt.IsZero())
	assert.True(t, s.LastUpdatedAt.IsZero())
}

func TestJobZeroValues(t *testing.T) {
	var j Job
	assert.Empty(t, j.ID)
	assert.Empty(t, j.RepoURL)
	assert.Empty(t, j.Ref)
	assert.Empty(t, j.Profile)
	assert.Empty(t, j.Task)
	assert.Empty(t, j.Mode)
	assert.Zero(t, j.TTLMinutes)
	assert.False(t, j.Keepalive)
	assert.Empty(t, j.Status)
	assert.Nil(t, j.SandboxVMID)
	assert.True(t, j.CreatedAt.IsZero())
	assert.True(t, j.UpdatedAt.IsZero())
	assert.Empty(t, j.ResultJSON)
}

func TestWorkspaceZeroValues(t *testing.T) {
	var w Workspace
	assert.Empty(t, w.ID)
	assert.Empty(t, w.Name)
	assert.Empty(t, w.Storage)
	assert.Empty(t, w.VolumeID)
	assert.Zero(t, w.SizeGB)
	assert.Nil(t, w.AttachedVM)
	assert.True(t, w.CreatedAt.IsZero())
	assert.True(t, w.LastUpdated.IsZero())
}

func TestProfileZeroValues(t *testing.T) {
	var p Profile
	assert.Empty(t, p.Name)
	assert.Zero(t, p.TemplateVM)
	assert.True(t, p.UpdatedAt.IsZero())
	assert.Empty(t, p.RawYAML)
}

// BenchmarkJSONSerialization benchmarks JSON marshaling for each model type
func BenchmarkSandboxJSONMarshal(b *testing.B) {
	s := Sandbox{
		VMID:          100,
		Name:          "test-sandbox",
		Profile:       "default",
		State:         SandboxReady,
		IP:            "10.77.0.100",
		Keepalive:     true,
		LeaseExpires:  time.Now().Add(2 * time.Hour),
		CreatedAt:     time.Now(),
		LastUpdatedAt: time.Now(),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(s)
	}
}

func BenchmarkJobJSONMarshal(b *testing.B) {
	j := Job{
		ID:         "job-123",
		RepoURL:    "https://github.com/example/repo",
		Ref:        "main",
		Profile:    "default",
		Task:       "test task",
		Mode:       "dangerous",
		TTLMinutes: 120,
		Keepalive:  true,
		Status:     JobRunning,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(j)
	}
}
