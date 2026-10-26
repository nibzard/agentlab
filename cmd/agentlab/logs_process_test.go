package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLogsFollowFlagPlacementsAtProcessBoundary(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "agentlab")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sandboxes/123/touch" {
			w.Write([]byte(`{"vmid":123}`))
			return
		}
		if r.URL.Path != "/v1/sandboxes/123/events" {
			t.Errorf("unexpected route %s", r.URL.Path)
			http.Error(w, "unexpected", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "1" {
			w.Write([]byte(`{"events":[{"id":2,"kind":"second","message":"followed"}],"last_id":2}`))
			return
		}
		if r.URL.Query().Get("tail") != "1" {
			t.Errorf("tail flag lost: %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"events":[{"id":1,"kind":"first","message":"initial"}],"last_id":1}`))
	}))
	defer server.Close()
	for _, args := range [][]string{{"logs", "123", "--follow", "--tail=1", "--json"}, {"--json", "logs", "--tail", "1", "--follow", "123"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		command := exec.CommandContext(ctx, binary, append([]string{"--endpoint", server.URL}, args...)...)
		command.Env = append(os.Environ(), "AGENTLAB_ENDPOINT=", "AGENTLAB_TOKEN=", "XDG_CONFIG_HOME="+dir, "HOME="+dir)
		stdout, err := command.StdoutPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		command.Stderr = &stderr
		if err := command.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		decoder := json.NewDecoder(stdout)
		for _, want := range []int64{1, 2} {
			var event eventResponse
			if err := decoder.Decode(&event); err != nil {
				command.Process.Kill()
				command.Wait()
				cancel()
				t.Fatalf("follow returned before event %d: %v %s", want, err, stderr.String())
			}
			if event.ID != want {
				t.Errorf("cursor: %+v", event)
			}
		}
		if err := command.Process.Signal(os.Interrupt); err != nil {
			t.Error(err)
		}
		err = command.Wait()
		cancel()
		if err != nil || stderr.Len() != 0 {
			t.Fatalf("follow cancellation: %v %s", err, stderr.String())
		}
	}
}
