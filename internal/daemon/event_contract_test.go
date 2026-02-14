package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
)

func TestEventCatalogKindsAreCanonical(t *testing.T) {
	knownDomains := map[EventDomain]struct{}{
		eventDomainArtifact:  {},
		eventDomainExposure:  {},
		eventDomainJob:       {},
		eventDomainRecovery:   {},
		eventDomainSandbox:    {},
		eventDomainWorkspace:  {},
	}
	knownStages := map[EventStage]struct{}{
		EventStageArtifact:  {},
		EventStageExposure:  {},
		EventStageLease:     {},
		EventStageLifecycle: {},
		EventStageNetwork:   {},
		EventStageRecovery:  {},
		EventStageReport:    {},
		EventStageSLO:       {},
		EventStageSnapshot:  {},
	}

	for kind, schema := range EventCatalog {
		if kind == "" {
			t.Fatalf("catalog contains empty kind key")
		}
		if schema.Kind != kind {
			t.Fatalf("schema kind mismatch: key=%q schema=%q", kind, schema.Kind)
		}
		if schema.Domain == "" {
			t.Fatalf("kind %q missing domain", kind)
		}
		if _, ok := knownDomains[schema.Domain]; !ok {
			t.Fatalf("kind %q has unknown domain %q", kind, schema.Domain)
		}
		if _, ok := knownStages[schema.Stage]; !ok {
			t.Fatalf("kind %q has unknown stage %q", kind, schema.Stage)
		}
		if schema.Schema <= 0 {
			t.Fatalf("kind %q has invalid schema %d", kind, schema.Schema)
		}
		if strings.TrimSpace(schema.Description) == "" {
			t.Fatalf("kind %q missing description", kind)
		}

		seen := map[string]struct{}{}
		for _, field := range schema.Required {
			field = strings.TrimSpace(field)
			if field == "" {
				t.Fatalf("kind %q has empty required field", kind)
			}
			if _, exists := seen[field]; exists {
				t.Fatalf("kind %q has duplicate required field %q", kind, field)
			}
			seen[field] = struct{}{}
		}
		seen = map[string]struct{}{}
		for _, field := range schema.Optional {
			field = strings.TrimSpace(field)
			if field == "" {
				t.Fatalf("kind %q has empty optional field", kind)
			}
			if _, exists := seen[field]; exists {
				t.Fatalf("kind %q has duplicate optional field %q", kind, field)
			}
			seen[field] = struct{}{}
		}
	}
}

func TestNewEventPayloadForKindValidatesAndSerializes(t *testing.T) {
	type payload struct {
		FromState  string `json:"from_state"`
		ToState    string `json:"to_state"`
		DurationMS int    `json:"duration_ms"`
	}

	raw, err := NewEventPayloadForKind(EventKindSandboxState, payload{
		FromState:  "READY",
		ToState:    "RUNNING",
		DurationMS: 1234,
	})
	if err != nil {
		t.Fatalf("NewEventPayloadForKind() error = %v", err)
	}

	var parsed struct {
		Kind          EventKind       `json:"kind"`
		SchemaVersion int             `json:"schema_version"`
		Stage         EventStage      `json:"stage"`
		JSONPayload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("unmarshal envelope = %v", err)
	}
	if parsed.Kind != EventKindSandboxState {
		t.Fatalf("kind = %q, want %q", parsed.Kind, EventKindSandboxState)
	}
	if parsed.SchemaVersion != eventContractSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", parsed.SchemaVersion, eventContractSchemaVersion)
	}
	if parsed.Stage != EventStageLifecycle {
		t.Fatalf("stage = %q, want %q", parsed.Stage, EventStageLifecycle)
	}
	var payloadOut payload
	if err := json.Unmarshal(parsed.JSONPayload, &payloadOut); err != nil {
		t.Fatalf("unmarshal payload = %v", err)
	}
	if payloadOut.FromState != "READY" || payloadOut.ToState != "RUNNING" || payloadOut.DurationMS != 1234 {
		t.Fatalf("payload mismatch: %+v", payloadOut)
	}

	if _, err := NewEventPayloadForKind(EventKindSandboxState, payload{
		FromState: "READY",
	}); err == nil {
		t.Fatalf("expected missing required field error")
	}
	if _, err := NewEventPayloadForKind(EventKind("unknown.kind"), map[string]any{}); err == nil {
		t.Fatalf("expected unknown kind error")
	}
}

func TestParseEventPayloadSupportsEnvelopeAndLegacyShapes(t *testing.T) {
	version, stage, payload, ok := parseEventPayload(`{"kind":"job.report","schema_version":1,"stage":"report","payload":{"status":"RUNNING"}}`)
	if !ok {
		t.Fatal("expected envelope payload to parse")
	}
	if version != eventContractSchemaVersion || stage != EventStageReport {
		t.Fatalf("parsed version/stage = %d/%q, want %d/%q", version, stage, eventContractSchemaVersion, EventStageReport)
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil || data["status"] != "RUNNING" {
		t.Fatalf("invalid parsed envelope payload: %#v err=%v", data, err)
	}

	version, stage, payload, ok = parseEventPayload(`{"status":"RUNNING"}`)
	if !ok {
		t.Fatal("expected legacy object payload to parse")
	}
	if version != 0 || stage != "" {
		t.Fatalf("legacy parse should return version 0 and empty stage, got %d %q", version, stage)
	}
	if string(payload) == "" {
		t.Fatal("expected legacy payload bytes")
	}

	if _, _, _, ok = parseEventPayload("invalid-json"); ok {
		t.Fatalf("expected invalid JSON to fail")
	}
}

func TestEventToV1UnwrapsEventPayload(t *testing.T) {
	type reportPayload struct {
		Status string `json:"status"`
	}
	payload, err := NewEventPayloadForKind(EventKindJobReport, reportPayload{Status: "RUNNING"})
	if err != nil {
		t.Fatalf("new payload: %v", err)
	}

	vmid := 1000
	evt := db.Event{
		ID:        1,
		Timestamp: time.Unix(1700000000, 0).UTC(),
		Kind:      string(EventKindJobReport),
		Message:   " job running ",
		SandboxVMID: &vmid,
		JobID:       stringPtr("job_123"),
		JSON:        payload,
	}
	converted := eventToV1(evt)
	if converted.ID != evt.ID {
		t.Fatalf("ID = %d, want %d", converted.ID, evt.ID)
	}
	if converted.Schema != eventContractSchemaVersion {
		t.Fatalf("Schema = %d, want %d", converted.Schema, eventContractSchemaVersion)
	}
	if converted.Stage != string(EventStageReport) {
		t.Fatalf("Stage = %q, want %q", converted.Stage, EventStageReport)
	}
	if converted.Message != "job running " {
		t.Fatalf("Message = %q", converted.Message)
	}
	if converted.SandboxVMID == nil || *converted.SandboxVMID != vmid {
		t.Fatalf("SandboxVMID = %#v", converted.SandboxVMID)
	}
	if converted.JobID != "job_123" {
		t.Fatalf("JobID = %q", converted.JobID)
	}

	var parsed map[string]any
	if err := json.Unmarshal(converted.Payload, &parsed); err != nil || parsed["status"] != "RUNNING" {
		t.Fatalf("invalid converted payload: %#v err=%v", parsed, err)
	}

	legacy := `{"legacy":true}`
	legacyEvent := db.Event{
		ID:        2,
		Timestamp: time.Unix(1700000001, 0).UTC(),
		Kind:      "legacy.custom",
		JSON:      legacy,
	}
	legacyConverted := eventToV1(legacyEvent)
	if legacyConverted.Schema != 0 {
		t.Fatalf("legacy schema should be zero, got %d", legacyConverted.Schema)
	}
	var legacyPayload map[string]bool
	if err := json.Unmarshal(legacyConverted.Payload, &legacyPayload); err != nil || !legacyPayload["legacy"] {
		t.Fatalf("legacy payload parse = %#v err=%v", legacyPayload, err)
	}
}

func stringPtr(value string) *string {
	return &value
}
