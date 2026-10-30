// Package operations records resumable client workflows before a daemon exists.
package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

type State string

const (
	Planned   State = "planned"
	Running   State = "running"
	Blocked   State = "blocked"
	Succeeded State = "succeeded"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

type Event struct {
	Cursor int64     `json:"cursor"`
	Time   time.Time `json:"time"`
	Step   string    `json:"step"`
	State  State     `json:"state"`
}
type Resource struct {
	Kind      string     `json:"kind"`
	ID        string     `json:"id"`
	State     string     `json:"state"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}
type WorkerObservation struct {
	ObservedAt time.Time `json:"observed_at"`
	Kind       string    `json:"kind"`
	Unit       string    `json:"unit"`
	State      string    `json:"state"`
	Active     bool      `json:"active"`
	PID        int       `json:"pid,omitempty"`
	ExitStatus *int      `json:"exit_status,omitempty"`
}
type Record struct {
	ClientConnectionVerifiedAt *time.Time         `json:"client_connection_verified_at,omitempty"`
	ClientConnection           string             `json:"client_connection_state,omitempty"`
	Worker                     *WorkerObservation `json:"worker,omitempty"`
	Version                    int                `json:"version"`
	ID                         string             `json:"id"`
	Owner                      string             `json:"owner"`
	Context                    string             `json:"context"`
	ServerIdentity             string             `json:"server_identity"`
	InputHash                  string             `json:"input_hash"`
	State                      State              `json:"state"`
	Step                       string             `json:"step,omitempty"`
	Completed                  []string           `json:"completed"`
	Resources                  []Resource         `json:"resources"`
	CreatedAt                  time.Time          `json:"created_at"`
	UpdatedAt                  time.Time          `json:"updated_at"`
	KeyExpiresAt               time.Time          `json:"key_expires_at"`
	Events                     []Event            `json:"events"`
	ErrorCode                  string             `json:"error_code,omitempty"`
	Action                     string             `json:"required_action,omitempty"`
	Retryable                  bool               `json:"retryable"`
	Cleanup                    string             `json:"cleanup_status"`
}

type Request struct {
	Owner, Context, ServerIdentity, Key string
	Input                               any
}
type Store struct{ Root string }

var validID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var ErrBusy = errors.New("operation already has a running client")
var ErrConflict = errors.New("idempotency key already belongs to different inputs")
var ErrExpired = errors.New("idempotency key expired; inspect existing resources before submitting a new request")

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".operation-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s Store) lock(id string) (*os.File, error) {
	if !validID.MatchString(id) {
		return nil, fmt.Errorf("invalid operation ID")
	}
	if err := os.MkdirAll(s.Root, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(s.Root, id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return file, nil
}
func unlock(file *os.File) { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }

// Read observes one atomic record without waiting for the execution lock.
func (s Store) Read(id string) (Record, error) {
	var record Record
	if !validID.MatchString(id) {
		return record, fmt.Errorf("invalid operation ID")
	}
	data, err := os.ReadFile(filepath.Join(s.Root, id+".json"))
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, err
	}
	if record.Version != 1 || record.ID != id {
		return record, fmt.Errorf("invalid operation record")
	}
	return record, nil
}

func (s Store) save(record *Record) error {
	record.UpdatedAt = time.Now().UTC()
	cursor := int64(len(record.Events) + 1)
	record.Events = append(record.Events, Event{Cursor: cursor, Time: record.UpdatedAt, Step: record.Step, State: record.State})
	return writeJSON(filepath.Join(s.Root, record.ID+".json"), record)
}

// Create deduplicates by owner, verified server identity, and request key.
// Only the input hash is stored. Callers must keep credentials out of metadata.
func (s Store) Create(request Request) (Record, error) {
	var record Record
	if request.Owner == "" || request.ServerIdentity == "" {
		return record, fmt.Errorf("owner and verified server identity are required")
	}
	if request.Key == "" {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return record, err
		}
		request.Key = hex.EncodeToString(buf)
	}
	scope, _ := json.Marshal([]string{request.Owner, request.ServerIdentity, request.Key})
	keyHash := sha256.Sum256(scope)
	id := hex.EncodeToString(keyHash[:16])
	input, err := json.Marshal(request.Input)
	if err != nil {
		return record, err
	}
	hash := sha256.Sum256(input)
	inputHash := hex.EncodeToString(hash[:])
	if existing, readErr := s.Read(id); readErr == nil {
		if existing.InputHash != inputHash || existing.Context != request.Context {
			return existing, ErrConflict
		}
		if time.Now().After(existing.KeyExpiresAt) {
			return existing, ErrExpired
		}
		return existing, nil
	}
	file, err := s.lock(id)
	if err != nil {
		return record, err
	}
	defer unlock(file)
	existing, err := s.Read(id)
	if err == nil {
		if existing.InputHash != inputHash || existing.Context != request.Context {
			return existing, ErrConflict
		}
		if time.Now().After(existing.KeyExpiresAt) {
			return existing, ErrExpired
		}
		return existing, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return record, err
	}
	now := time.Now().UTC()
	record = Record{Version: 1, ID: id, Owner: request.Owner, Context: request.Context, ServerIdentity: request.ServerIdentity, InputHash: inputHash, State: Planned, CreatedAt: now, KeyExpiresAt: now.Add(30 * 24 * time.Hour), Completed: []string{}, Resources: []Resource{}, Cleanup: "not_required"}
	err = s.save(&record)
	return record, err
}

type Requirement struct{ Code, Action string }

func (r *Requirement) Error() string { return r.Action }

// Step verifies external state before every action, including after a crash.
// Apply must record resource IDs before it reports success. Check must distinguish
// a missing resource from a connection or authorization error.
type Step struct {
	Name  string
	Check func(context.Context, *Record) (bool, error)
	Apply func(context.Context, *Record, func() error) error
}

// Cancel requests cooperative cancellation. A running step may still need cleanup.
func (s Store) Cancel(id string) error {
	if _, err := s.Read(id); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.Root, id+".cancel"), map[string]bool{"requested": true})
}

// Run holds an execution lock. Client termination releases the lock; the next
// client checks external state before repeating a step. It never claims rollback.
func (s Store) Run(ctx context.Context, id string, steps []Step) (Record, error) {
	var record Record
	file, err := s.lock(id)
	if err != nil {
		return record, err
	}
	defer unlock(file)
	record, err = s.Read(id)
	if err != nil {
		return record, err
	}
	if record.State == Succeeded || record.State == Cancelled {
		return record, nil
	}
	checkpoint := func() error { return s.save(&record) }
	fail := func(err error) (Record, error) {
		record.State = Failed
		record.ErrorCode = "step_failed"
		record.Retryable = true
		record.Action = "inspect the selected server and operation resources, then resume"
		var required *Requirement
		if errors.As(err, &required) {
			record.State = Blocked
			record.ErrorCode = required.Code
			record.Action = required.Action
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			record.State = Blocked
			record.ErrorCode = "client_interrupted"
			record.Action = "reconnect and resume the same operation"
		}
		if len(record.Resources) > 0 {
			record.Cleanup = "inspection_required"
		}
		if saveErr := checkpoint(); saveErr != nil {
			return record, errors.Join(err, saveErr)
		}
		return record, err
	}
	record.State = Running
	record.ErrorCode = ""
	record.Action = ""
	if err := checkpoint(); err != nil {
		return record, err
	}
	for _, step := range steps {
		if _, err := os.Stat(filepath.Join(s.Root, id+".cancel")); err == nil {
			record.State = Cancelled
			if len(record.Resources) > 0 {
				record.Cleanup = "inspection_required"
			}
			record.Action = "verify resources; cancellation does not roll back completed changes"
			err := checkpoint()
			return record, err
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		record.Step = step.Name
		if step.Name == "" || step.Check == nil || step.Apply == nil {
			return fail(fmt.Errorf("step requires a name, check, and action"))
		}
		if err := checkpoint(); err != nil {
			return record, err
		}
		ready, err := step.Check(ctx, &record)
		if err != nil {
			return fail(err)
		}
		if !ready {
			if err := step.Apply(ctx, &record, checkpoint); err != nil {
				return fail(err)
			}
			ready, err = step.Check(ctx, &record)
			if err != nil {
				return fail(err)
			}
			if !ready {
				return fail(fmt.Errorf("step verification failed"))
			}
		}
		present := false
		for _, name := range record.Completed {
			if name == step.Name {
				present = true
			}
		}
		if !present {
			record.Completed = append(record.Completed, step.Name)
		}
		if err := checkpoint(); err != nil {
			return record, err
		}
	}
	record.State = Succeeded
	record.Step = ""
	record.Retryable = false
	err = checkpoint()
	return record, err
}

// Update checkpoints reconciliation performed after a runner has stopped.
func (s Store) Update(id string, change func(*Record) error) (Record, error) {
	var record Record
	file, err := s.lock(id)
	if err != nil {
		return record, err
	}
	defer unlock(file)
	record, err = s.Read(id)
	if err != nil {
		return record, err
	}
	if err := change(&record); err != nil {
		return record, err
	}
	err = s.save(&record)
	return record, err
}

// SaveObservation stores a verified server observation without creating local
// progress events or changing the server's progress timestamp.
func (s Store) SaveObservation(record Record) (Record, error) {
	file, err := s.lock(record.ID)
	if err != nil {
		return record, err
	}
	defer unlock(file)
	local, err := s.Read(record.ID)
	if err != nil {
		return record, err
	}
	if record.Version != 1 || record.InputHash != local.InputHash || record.Owner != local.Owner || record.Context != local.Context || record.ServerIdentity != local.ServerIdentity {
		return record, fmt.Errorf("operation observation binding changed")
	}
	if local.Worker != nil && (len(record.Events) < len(local.Events) || (len(record.Events) == len(local.Events) && record.Worker != nil && record.Worker.ObservedAt.Before(local.Worker.ObservedAt))) {
		return local, nil
	}
	if record.ClientConnection == "" {
		record.ClientConnection = local.ClientConnection
		record.ClientConnectionVerifiedAt = local.ClientConnectionVerifiedAt
	}
	if record.ClientConnection == "" {
		record.ClientConnection = "pending"
	}
	return record, writeJSON(filepath.Join(s.Root, record.ID+".json"), record)
}
