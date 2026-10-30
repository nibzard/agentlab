package operations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createTest(t *testing.T, store Store) Record {
	t.Helper()
	record, err := store.Create(Request{Owner: "user", ServerIdentity: "server-identity", Context: "development", Key: "request-1", Input: map[string]string{"task": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestIdempotencyAndRetention(t *testing.T) {
	store := Store{Root: t.TempDir()}
	record := createTest(t, store)
	duplicate := createTest(t, store)
	if duplicate.ID != record.ID || len(record.Events) != 1 || record.UpdatedAt.IsZero() {
		t.Fatal("create was not durable and idempotent")
	}
	_, err := store.Create(Request{Owner: "user", ServerIdentity: "server-identity", Context: "development", Key: "request-1", Input: "different"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	record.KeyExpiresAt = time.Now().Add(-time.Hour)
	if err := store.save(&record); err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(Request{Owner: "user", ServerIdentity: "server-identity", Context: "development", Key: "request-1", Input: map[string]string{"task": "test"}})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expired key reused: %v", err)
	}
}

func TestResumeReconcilesLostResponse(t *testing.T) {
	store := Store{Root: t.TempDir()}
	record := createTest(t, store)
	creates := 0
	exists := false
	step := Step{Name: "create_resource", Check: func(context.Context, *Record) (bool, error) { return exists, nil }, Apply: func(ctx context.Context, r *Record, checkpoint func() error) error {
		creates++
		exists = true
		r.Resources = append(r.Resources, Resource{Kind: "test", ID: "resource-1", State: "retained"})
		if err := checkpoint(); err != nil {
			return err
		}
		return errors.New("response lost")
	}}
	failed, err := store.Run(context.Background(), record.ID, []Step{step})
	if err == nil || failed.State != Failed {
		t.Fatal("expected interrupted step")
	}
	finished, err := store.Run(context.Background(), record.ID, []Step{step})
	if err != nil || finished.State != Succeeded || creates != 1 {
		t.Fatalf("duplicate create: %d %v", creates, err)
	}
	if len(finished.Resources) != 1 || len(finished.Events) < 5 {
		t.Fatal("lost resources or events")
	}
}

func TestObserveAndCancelDuringRun(t *testing.T) {
	store := Store{Root: t.TempDir()}
	record := createTest(t, store)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	first := Step{Name: "first", Check: func(context.Context, *Record) (bool, error) { return false, nil }, Apply: func(context.Context, *Record, func() error) error {
		close(entered)
		<-release
		return errors.New("interrupted")
	}}
	go func() { _, err := store.Run(context.Background(), record.ID, []Step{first}); done <- err }()
	<-entered
	observed, err := store.Read(record.ID)
	if err != nil || observed.State != Running {
		t.Fatalf("cannot observe active operation: %v", err)
	}
	same := createTest(t, store)
	if same.ID != record.ID {
		t.Fatal("retry changed operation")
	}
	if _, err := store.Run(context.Background(), record.ID, []Step{first}); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent runner: %v", err)
	}
	if err := store.Cancel(record.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	cancelled, err := store.Run(context.Background(), record.ID, []Step{first})
	if err != nil || cancelled.State != Cancelled {
		t.Fatalf("cancel: %v", err)
	}
}

func TestBlockedStateDoesNotPersistDiagnosticSecrets(t *testing.T) {
	store := Store{Root: t.TempDir()}
	record := createTest(t, store)
	blocked, err := store.Run(context.Background(), record.ID, []Step{{Name: "access", Check: func(context.Context, *Record) (bool, error) {
		return false, &Requirement{Code: "authorization_required", Action: "grant setup authority"}
	}, Apply: func(context.Context, *Record, func() error) error { return nil }}})
	if err == nil || blocked.State != Blocked || blocked.ErrorCode != "authorization_required" {
		t.Fatal("missing blocked result")
	}
	data, err := os.ReadFile(filepath.Join(store.Root, record.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"task"`) {
		t.Fatal("raw input persisted instead of input hash")
	}
	if _, err := store.Read("../escape"); err == nil {
		t.Fatal("invalid ID accepted")
	}
}
