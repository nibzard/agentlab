package main

import (
	"io"
	"strings"
	"testing"
)

func TestRequireConfirmationNonInteractive(t *testing.T) {
	origInteractive := isInteractiveFn
	isInteractiveFn = func() bool { return false }
	t.Cleanup(func() { isInteractiveFn = origInteractive })

	err := requireConfirmation(confirmOptions{action: "stop all running sandboxes", force: false, jsonOutput: false})
	if err == nil {
		t.Fatalf("expected error for non-interactive confirmation")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected --force hint, got %q", err.Error())
	}
}

func TestRequireConfirmationJSON(t *testing.T) {
	origInteractive := isInteractiveFn
	isInteractiveFn = func() bool { return true }
	t.Cleanup(func() { isInteractiveFn = origInteractive })

	err := requireConfirmation(confirmOptions{action: "expose sandbox 10 port 8080", force: false, jsonOutput: true})
	if err == nil {
		t.Fatalf("expected error for json confirmation")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected --force hint, got %q", err.Error())
	}
}

func TestRequireConfirmationInteractiveYes(t *testing.T) {
	origInteractive := isInteractiveFn
	origReader := confirmReader
	origWriter := confirmWriter
	isInteractiveFn = func() bool { return true }
	confirmReader = strings.NewReader("yes\n")
	confirmWriter = io.Discard
	t.Cleanup(func() {
		isInteractiveFn = origInteractive
		confirmReader = origReader
		confirmWriter = origWriter
	})

	if err := requireConfirmation(confirmOptions{action: "stop all running sandboxes", force: false, jsonOutput: false}); err != nil {
		t.Fatalf("expected confirmation to succeed, got %v", err)
	}
}

func TestRequireConfirmationInteractiveNo(t *testing.T) {
	origInteractive := isInteractiveFn
	origReader := confirmReader
	origWriter := confirmWriter
	isInteractiveFn = func() bool { return true }
	confirmReader = strings.NewReader("no\n")
	confirmWriter = io.Discard
	t.Cleanup(func() {
		isInteractiveFn = origInteractive
		confirmReader = origReader
		confirmWriter = origWriter
	})

	err := requireConfirmation(confirmOptions{action: "expose sandbox 10 port 8080", force: false, jsonOutput: false})
	if err == nil {
		t.Fatalf("expected confirmation to abort")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "aborted") {
		t.Fatalf("expected aborted error, got %q", err.Error())
	}
}
