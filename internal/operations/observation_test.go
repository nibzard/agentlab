package operations

import (
	"reflect"
	"testing"
	"time"
)

func TestServerObservationPreservesProgressAndClientReceipt(t *testing.T) {
	store := Store{Root: t.TempDir()}
	record, err := store.Create(Request{Owner: "caller", Context: "server", ServerIdentity: "identity", Key: "key", Input: "input"})
	if err != nil {
		t.Fatal(err)
	}
	progress := record.UpdatedAt
	events := append([]Event{}, record.Events...)
	record.Worker = &WorkerObservation{Kind: "systemd", State: "active", ObservedAt: time.Now().UTC(), Active: true}
	saved, err := store.SaveObservation(record)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.UpdatedAt.Equal(progress) || !reflect.DeepEqual(saved.Events, events) {
		t.Fatal("observation changed server progress")
	}
	verified := time.Now().UTC()
	saved.ClientConnection = "verified"
	saved.ClientConnectionVerifiedAt = &verified
	if _, err := store.SaveObservation(saved); err != nil {
		t.Fatal(err)
	}
	observed, err := store.SaveObservation(record)
	if err != nil {
		t.Fatal(err)
	}
	if observed.ClientConnection != "verified" || observed.ClientConnectionVerifiedAt == nil || !observed.ClientConnectionVerifiedAt.Equal(verified) {
		t.Fatal("server observation discarded client verification")
	}
	changed := record
	changed.ServerIdentity = "other"
	if _, err := store.SaveObservation(changed); err == nil {
		t.Fatal("observation redirected the operation")
	}
}
