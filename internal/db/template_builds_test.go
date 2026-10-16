package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	tb "github.com/agentlab/agentlab/internal/templatebuild"
)

func buildRecord(n int) (tb.Build, string) {
	p := tb.Normalize(tb.Plan{Name: "test", Storage: "local", RequestID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n)}, tb.Limits{}).Plan
	raw, _ := json.Marshal(p)
	now := time.Now().UTC()
	return tb.Build{ID: fmt.Sprintf("tb_%d", n), State: "QUEUED", Plan: p, CreatedAt: now, UpdatedAt: now}, string(raw)
}
func TestTemplateSubmissionIdempotencyAndSharedReservation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	b, raw := buildRecord(1)
	limits := tb.Limits{}.Defaults()
	got, duplicate, e := s.SubmitTemplateBuild(ctx, b, raw, map[int]bool{9000: true}, limits)
	if e != nil || duplicate || got.VMID != 9001 {
		t.Fatalf("submit: %+v %v %v", got, duplicate, e)
	}
	again, duplicate, e := s.SubmitTemplateBuild(ctx, b, raw, nil, limits)
	if e != nil || !duplicate || again.ID != got.ID || again.VMID != got.VMID {
		t.Fatal("lost response retry created another build")
	}
	if _, _, e = s.SubmitTemplateBuild(ctx, b, raw+" ", nil, limits); !errors.Is(e, ErrTemplateIdempotency) {
		t.Fatalf("changed input accepted: %v", e)
	}
	if e = s.CreateSandbox(ctx, models.Sandbox{VMID: got.VMID, Name: "collision", Profile: "test", State: models.SandboxRequested}); e == nil {
		t.Fatal("sandbox took reserved template VMID")
	}
	if e = s.CreateSandbox(ctx, models.Sandbox{VMID: got.SmokeVMID, Name: "smoke-collision", Profile: "test", State: models.SandboxRequested}); e == nil {
		t.Fatal("sandbox took reserved smoke VMID")
	}
	if e = s.CreateSandbox(ctx, models.Sandbox{VMID: 9003, Name: "existing", Profile: "test", State: models.SandboxRequested}); e != nil {
		t.Fatal(e)
	}
	b, raw = buildRecord(2)
	got, _, e = s.SubmitTemplateBuild(ctx, b, raw, map[int]bool{9000: true}, limits)
	if e != nil || got.VMID != 9004 {
		t.Fatalf("sandbox collision: %+v %v", got, e)
	}
}
func TestConcurrentSandboxTemplateReservation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	b, raw := buildRecord(1)
	b.Plan.VMID = 9000
	limits := tb.Limits{}.Defaults()
	var wg sync.WaitGroup
	wg.Add(2)
	var templateErr, sandboxErr error
	go func() { defer wg.Done(); _, _, templateErr = s.SubmitTemplateBuild(ctx, b, raw, nil, limits) }()
	go func() {
		defer wg.Done()
		sandboxErr = s.CreateSandbox(ctx, models.Sandbox{VMID: 9000, Name: "racer", Profile: "test", State: models.SandboxRequested})
	}()
	wg.Wait()
	if (templateErr == nil) == (sandboxErr == nil) {
		t.Fatalf("expected exactly one winner: template=%v sandbox=%v", templateErr, sandboxErr)
	}
}
func TestTemplateTransitionsQueueAndCursor(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	limits := tb.Limits{}.Defaults()
	limits.Pending = 1
	b, raw := buildRecord(1)
	b, _, e := s.SubmitTemplateBuild(ctx, b, raw, nil, limits)
	if e != nil {
		t.Fatal(e)
	}
	other, raw := buildRecord(2)
	if _, _, e = s.SubmitTemplateBuild(ctx, other, raw, nil, limits); !errors.Is(e, ErrTemplateQueueFull) {
		t.Fatal(e)
	}
	bad := b
	bad.State = "SUCCEEDED"
	if e = s.SaveTemplateBuild(ctx, bad); e == nil {
		t.Fatal("queued success accepted")
	}
	b.State = "RUNNING"
	if e = s.SaveTemplateBuild(ctx, b); e != nil {
		t.Fatal(e)
	}
	for _, msg := range []string{"one", "two", "three"} {
		if e = s.AppendTemplateLog(ctx, b.ID, "download", msg); e != nil {
			t.Fatal(e)
		}
	}
	ls, e := s.TemplateLogs(ctx, b.ID, 1, 1)
	if e != nil || len(ls.Records) != 1 || ls.Next != 2 || ls.Records[0].Message != "two" {
		t.Fatalf("cursor: %+v %v", ls, e)
	}
	b.State = "FAILED"
	if e = s.SaveTemplateBuild(ctx, b); e != nil {
		t.Fatal(e)
	}
	b.State = "RUNNING"
	if e = s.SaveTemplateBuild(ctx, b); e == nil {
		t.Fatal("terminal build restarted")
	}
	if ms, e := s.TemplateManifests(ctx, 0, 100); e != nil || len(ms) != 0 {
		t.Fatal("failed build became selectable")
	}
}

func TestTemplateLogImportPreservesSourceCursorAndTime(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	b, request := buildRecord(1)
	b, _, err := s.SubmitTemplateBuild(ctx, b, request, nil, tb.Limits{}.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 10, 15, 10, 11, 12, 123456789, time.FixedZone("worker-zone", 3600))
	records := []tb.Log{{Sequence: 1, Timestamp: timestamp, Stage: "download", Message: "first"}, {Sequence: 2, Timestamp: timestamp.Add(time.Second), Stage: "customize", Message: "second"}}
	if err = s.ImportTemplateLogs(ctx, b.ID, records); err != nil {
		t.Fatal(err)
	}
	if err = s.ImportTemplateLogs(ctx, b.ID, records); err != nil {
		t.Fatal(err)
	}
	got, err := s.TemplateLogs(ctx, b.ID, 0, 100)
	if err != nil || len(got.Records) != 2 || got.Next != 2 || !got.Records[0].Timestamp.Equal(timestamp) || got.Records[0].Timestamp.Location() != time.UTC {
		t.Fatalf("lost source log identity: %+v %v", got, err)
	}
	gap := []tb.Log{{Sequence: 3, Timestamp: timestamp, Message: "third"}, {Sequence: 5, Timestamp: timestamp, Message: "gap"}}
	if err = s.ImportTemplateLogs(ctx, b.ID, gap); err == nil {
		t.Fatal("accepted missing source record")
	}
	got, err = s.TemplateLogs(ctx, b.ID, 2, 100)
	if err != nil || len(got.Records) != 0 || got.Next != 2 {
		t.Fatal("failed batch advanced cursor")
	}
}

func TestTemplateLogImportAuditsStagesSkippedBetweenPolls(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	b, request := buildRecord(1)
	b, _, err := s.SubmitTemplateBuild(ctx, b, request, nil, tb.Limits{}.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	records := []tb.Log{{Sequence: 1, Timestamp: now, Stage: "preflight", Message: "stage started"}, {Sequence: 2, Timestamp: now, Stage: "download", Message: "stage started"}, {Sequence: 3, Timestamp: now, Stage: "download", Message: "download complete"}, {Sequence: 4, Timestamp: now, Stage: "customize", Message: "stage started"}}
	for i := 0; i < 2; i++ {
		if err = s.ImportTemplateLogs(ctx, b.ID, records); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE kind='template.build.stage_changed'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("stage audit omitted or duplicated transitions: %d", count)
	}
}

func TestTemplateWorkerClaimIsExclusiveAcrossConnections(t *testing.T) {
	s := openTestStore(t)
	other, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx := context.Background()
	var builds []tb.Build
	for i := 1; i <= 2; i++ {
		b, request := buildRecord(i)
		b, _, err = s.SubmitTemplateBuild(ctx, b, request, nil, tb.Limits{}.Defaults())
		if err != nil {
			t.Fatal(err)
		}
		builds = append(builds, b)
	}
	ready := make(chan struct{})
	results := make(chan struct {
		b       tb.Build
		claimed bool
		err     error
	}, 2)
	for i, store := range []*Store{s, other} {
		go func(store *Store, id string) {
			<-ready
			b, claimed, err := store.ClaimTemplateBuild(ctx, id)
			results <- struct {
				b       tb.Build
				claimed bool
				err     error
			}{b, claimed, err}
		}(store, builds[i].ID)
	}
	close(ready)
	winners := 0
	winner := ""
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.claimed {
			winners++
			winner = result.b.ID
			if result.b.State != "RUNNING" || result.b.StartedAt == nil {
				t.Fatal("claimed record lacks durable running metadata")
			}
		}
	}
	if winners != 1 {
		t.Fatalf("worker slots claimed: %d", winners)
	}
	running, err := s.TemplateBuilds(ctx, "RUNNING", 0, 10)
	if err != nil || len(running) != 1 || running[0].ID != winner {
		t.Fatalf("worker slot not durable: %+v %v", running, err)
	}
	if _, claimed, err := other.ClaimTemplateBuild(ctx, winner); err != nil || claimed {
		t.Fatalf("running worker claimed twice: %v %v", claimed, err)
	}
	running[0].State = "FAILED"
	if err = s.SaveTemplateBuild(ctx, running[0]); err != nil {
		t.Fatal(err)
	}
	for _, b := range builds {
		if b.ID == winner {
			continue
		}
		if _, claimed, err := other.ClaimTemplateBuild(ctx, b.ID); err != nil || !claimed {
			t.Fatalf("terminal build did not release worker slot: %v %v", claimed, err)
		}
	}
}
func TestTemplateWorkerClaimDoesNotReviveCancelledBuild(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	b, request := buildRecord(1)
	b, _, err := s.SubmitTemplateBuild(ctx, b, request, nil, tb.Limits{}.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	b.State = "CANCELLED"
	b.CancelRequested = true
	if err = s.SaveTemplateBuild(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := s.ClaimTemplateBuild(ctx, b.ID); err != nil || claimed {
		t.Fatalf("cancelled build acquired worker: %v %v", claimed, err)
	}
}

func TestTemplateProgressPreservesConcurrentCancellationAndLaunch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	b, request := buildRecord(1)
	b, _, err := s.SubmitTemplateBuild(ctx, b, request, nil, tb.Limits{}.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	stale, claimed, err := s.ClaimTemplateBuild(ctx, b.ID)
	if err != nil || !claimed {
		t.Fatal(err)
	}
	current := stale
	current.CancelRequested = true
	if err = s.SaveTemplateBuild(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkTemplateWorkerLaunched(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	stale.Stage = "download"
	if err = s.SaveTemplateBuild(ctx, stale); err != nil {
		t.Fatal(err)
	}
	got, err := s.TemplateBuild(ctx, b.ID)
	if err != nil || !got.CancelRequested || !got.WorkerLaunched || got.Stage != "download" {
		t.Fatalf("progress erased durable metadata: %+v %v", got, err)
	}
}
