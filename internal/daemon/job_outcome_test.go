package daemon

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

func TestJobOutcomePreservesMissingHistoricalValues(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"", `null`},
		{`{"status":"COMPLETED"}`, `null`},
		{`{"status":"FAILED","message":"runner stopped","result":{"exit_code":7,"duration_seconds":12.5,"commit":"abc","future":true}}`, `{"exit_code":7,"duration_seconds":12.5,"commit":"abc","message":"runner stopped"}`},
		{`{"status":"COMPLETED","result":{"exit_code":0,"duration_seconds":0}}`, `{"exit_code":0,"duration_seconds":0}`},
		{`{"status":"TIMEOUT","message":"deadline","result":{"exit_code":null,"duration_seconds":-1}}`, `{"message":"deadline"}`},
		{`{"status":"FAILED","result":{"exit_code":"unknown","commit":"abc"}}`, `{"commit":"abc"}`},
	} {
		response := jobToV1(models.Job{Status: models.JobCompleted, ResultJSON: tc.raw})
		got, err := json.Marshal(response.Outcome)
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: %s want %s (%v)", tc.raw, got, tc.want, err)
		}
		if tc.raw != "" && string(response.Result) != tc.raw {
			t.Error("original result changed")
		}
	}
}

func TestWorkspaceRecoverySummaryExposesOwnerWithoutNonce(t *testing.T) {
	workspace := models.Workspace{ID: "workspace", LeaseOwner: "job:fixture", LeaseNonce: "private-nonce", LeaseExpires: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	payload, err := json.Marshal(workspaceToV1(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(payload, []byte(`"lease_owner":"job:fixture"`)) || !bytes.Contains(payload, []byte(`"lease_expires_at":"2030-01-02T03:04:05Z"`)) || bytes.Contains(payload, []byte("nonce")) {
		t.Fatalf("lease summary: %s", payload)
	}
}
