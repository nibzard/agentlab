package daemon

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

// TestAllowedTransition_AllEdges is the authoritative table test for the sandbox
// state machine (review test-debt). It is derived directly from allowedTransition
// in sandbox_manager.go and covers both allowed and denied edges, so any drift in
// the transition rules surfaces here. The models package must not reach across to
// this unexported function; the rules are owned here.
func TestAllowedTransition_AllEdges(t *testing.T) {
	allStates := []models.SandboxState{
		models.SandboxRequested, models.SandboxProvisioning, models.SandboxBooting,
		models.SandboxReady, models.SandboxRunning, models.SandboxSuspended,
		models.SandboxCompleted, models.SandboxFailed, models.SandboxTimeout,
		models.SandboxStopped, models.SandboxDestroyed,
	}

	// Allowed edges, transcribed exactly from allowedTransition.
	allowed := map[models.SandboxState]map[models.SandboxState]bool{
		models.SandboxRequested:    {models.SandboxProvisioning: true, models.SandboxTimeout: true, models.SandboxDestroyed: true},
		models.SandboxProvisioning: {models.SandboxBooting: true, models.SandboxTimeout: true, models.SandboxDestroyed: true},
		models.SandboxBooting:      {models.SandboxReady: true, models.SandboxTimeout: true, models.SandboxDestroyed: true},
		models.SandboxReady:        {models.SandboxRunning: true, models.SandboxSuspended: true, models.SandboxStopped: true, models.SandboxTimeout: true, models.SandboxDestroyed: true},
		models.SandboxRunning:      {models.SandboxSuspended: true, models.SandboxCompleted: true, models.SandboxFailed: true, models.SandboxTimeout: true, models.SandboxStopped: true, models.SandboxDestroyed: true},
		models.SandboxSuspended:    {models.SandboxReady: true, models.SandboxRunning: true, models.SandboxStopped: true, models.SandboxTimeout: true, models.SandboxDestroyed: true},
		models.SandboxCompleted:    {models.SandboxStopped: true, models.SandboxDestroyed: true},
		models.SandboxFailed:       {models.SandboxStopped: true, models.SandboxDestroyed: true},
		models.SandboxTimeout:      {models.SandboxStopped: true, models.SandboxDestroyed: true},
		models.SandboxStopped:      {models.SandboxDestroyed: true, models.SandboxBooting: true, models.SandboxReady: true, models.SandboxRunning: true},
		models.SandboxDestroyed:    {},
	}

	for _, from := range allStates {
		t.Run(string(from), func(t *testing.T) {
			expectAllowed := allowed[from]
			if expectAllowed == nil {
				expectAllowed = map[models.SandboxState]bool{}
			}
			for _, to := range allStates {
				got := allowedTransition(from, to)
				want := expectAllowed[to]
				if got != want {
					t.Errorf("allowedTransition(%s, %s) = %v, want %v", from, to, got, want)
				}
			}
			// Sanity: the allStates set above must match the map's keys exactly.
			if len(expectAllowed) > 0 {
				for to := range expectAllowed {
					if !isKnownState(to, allStates) {
						t.Errorf("allowed map references unknown state %q", to)
					}
				}
			}
		})
	}

	// Terminal state invariant: DESTROYED permits no outgoing transition.
	for _, to := range allStates {
		if allowedTransition(models.SandboxDestroyed, to) {
			t.Errorf("DESTROYED must be terminal, but -> %s was allowed", to)
		}
	}
}

func isKnownState(s models.SandboxState, all []models.SandboxState) bool {
	for _, x := range all {
		if x == s {
			return true
		}
	}
	return false
}

// TestTransition_EnforcesStateMachine drives the public Transition API to prove a
// denied edge is rejected with ErrInvalidTransition and an allowed edge commits,
// keeping the table test honest against the real store + manager path.
func TestTransition_EnforcesStateMachine(t *testing.T) {
	store := newTestStore(t)
	mgr := NewSandboxManager(store, nil, log.New(io.Discard, "", 0))

	if err := store.CreateSandbox(t.Context(), models.Sandbox{
		VMID:      9100,
		Name:      "edge-sb",
		Profile:   "default",
		State:     models.SandboxRunning,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	// Denied edge: RUNNING -> PROVISIONING is not allowed.
	if err := mgr.Transition(t.Context(), 9100, models.SandboxProvisioning); err == nil {
		t.Fatal("expected ErrInvalidTransition for RUNNING -> PROVISIONING")
	}

	// Allowed edge: RUNNING -> STOPPED commits.
	if err := mgr.Transition(t.Context(), 9100, models.SandboxStopped); err != nil {
		t.Fatalf("Transition RUNNING -> STOPPED: %v", err)
	}
	sb, err := store.GetSandbox(t.Context(), 9100)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if sb.State != models.SandboxStopped {
		t.Fatalf("state = %s, want STOPPED", sb.State)
	}
}
