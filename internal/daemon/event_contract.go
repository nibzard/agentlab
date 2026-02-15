package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentlab/agentlab/internal/db"
)

func NewEventPayloadForKind(kind EventKind, payload any) (string, error) {
	schema, ok := EventCatalog[kind]
	if !ok {
		return "", fmt.Errorf("unknown event kind: %q", kind)
	}
	if payload != nil {
		if err := validatePayload(schema, payload); err != nil {
			return "", err
		}
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal event payload for %s: %w", kind, err)
	}
	envelope := struct {
		Kind           EventKind  `json:"kind"`
		SchemaVersion  int        `json:"schema_version"`
		Stage          EventStage `json:"stage"`
		Payload        any        `json:"payload"`
	}{
		Kind:           kind,
		SchemaVersion:  schema.Schema,
		Stage:          schema.Stage,
		Payload:        mustJSONPayload(encodedPayload),
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshal event envelope for %s: %w", kind, err)
	}
	return string(out), nil
}

type EventRecorder interface {
	RecordEvent(ctx context.Context, kind EventKind, sandboxVMID *int, jobID *string, message string, payloadJSON string) error
}

type storeEventRecorder struct {
	store *db.Store
}

func NewStoreEventRecorder(store *db.Store) EventRecorder {
	return &storeEventRecorder{store: store}
}

func (r *storeEventRecorder) RecordEvent(ctx context.Context, kind EventKind, sandboxVMID *int, jobID *string, message string, payloadJSON string) error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.RecordEvent(ctx, string(kind), sandboxVMID, jobID, strings.TrimSpace(message), payloadJSON)
}

func mustJSONPayload(data []byte) any {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return map[string]any{}
	}
	if strings.EqualFold(trimmed, "null") {
		return map[string]any{}
	}
	return json.RawMessage(data)
}

func parseEventPayload(raw string) (version int, stage EventStage, data json.RawMessage, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, "", nil, false
	}
	var payload struct {
		SchemaVersion int            `json:"schema_version"`
		Stage         EventStage     `json:"stage"`
		Payload       json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err == nil &&
		payload.SchemaVersion > 0 && len(payload.Payload) > 0 {
		return payload.SchemaVersion, payload.Stage, payload.Payload, true
	}
	if !json.Valid([]byte(raw)) {
		return 0, "", nil, false
	}
	if len(raw) > 0 {
		switch raw[0] {
		case '{', '[':
			return 0, "", json.RawMessage(raw), true
		}
	}
	if raw == "{}" || raw == "[]" {
		return 0, "", json.RawMessage(raw), true
	}
	return 0, "", nil, false
}

func parseLegacyEventPayload(raw string) (version int, stage EventStage, data json.RawMessage, ok bool) {
	version, stage, data, ok = parseEventPayload(raw)
	if !ok {
		return 0, "", nil, false
	}
	if version <= 0 {
		if !json.Valid(data) {
			return 0, "", nil, false
		}
	}
	return version, stage, data, true
}

func emitEvent(ctx context.Context, recorder EventRecorder, kind EventKind, sandboxVMID *int, jobID *string, message string, payload any) error {
	if recorder == nil {
		return nil
	}
	payloadJSON, err := NewEventPayloadForKind(kind, payload)
	if err != nil {
		return err
	}
	return recorder.RecordEvent(ctx, kind, sandboxVMID, jobID, message, payloadJSON)
}

func validatePayload(schema EventPayloadSchema, payload any) error {
	if len(schema.Required) == 0 {
		return nil
	}
	if payload == nil {
		return fmt.Errorf("required event fields missing for %s", schema.Kind)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("validate event payload %s: %w", schema.Kind, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("validate event payload %s: %w", schema.Kind, err)
	}
	for _, field := range schema.Required {
		value, ok := decoded[field]
		if !ok {
			return fmt.Errorf("event %s missing required field %s", schema.Kind, field)
		}
		switch typed := value.(type) {
		case nil:
			return fmt.Errorf("event %s missing required field %s", schema.Kind, field)
		case string:
			if strings.TrimSpace(typed) == "" {
				return fmt.Errorf("event %s missing required field %s", schema.Kind, field)
			}
		}
	}
	return nil
}
