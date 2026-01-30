package buildinfo

import "testing"

func TestString(t *testing.T) {
	oldVersion := Version
	oldCommit := Commit
	oldDate := Date
	Version = "1.2.3"
	Commit = "deadbeef"
	Date = "2026-01-30"
	defer func() {
		Version = oldVersion
		Commit = oldCommit
		Date = oldDate
	}()

	got := String()
	want := "version=1.2.3 commit=deadbeef date=2026-01-30"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
