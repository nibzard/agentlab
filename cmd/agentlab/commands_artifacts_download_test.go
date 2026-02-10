package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobArtifactsDownloadPathEncoding(t *testing.T) {
	jobID := "job-123"
	artifactPath := "logs/run 1+2.txt"
	expectedQuery := "path=" + url.QueryEscape(artifactPath)

	run := func(t *testing.T, base commonFlags) (string, string) {
		t.Helper()
		outPath := filepath.Join(t.TempDir(), "artifact.txt")
		args := []string{"--path", artifactPath, "--out", outPath, jobID}
		err := runJobArtifactsDownload(context.Background(), args, base)
		require.NoError(t, err)
		data, err := os.ReadFile(outPath)
		require.NoError(t, err)
		assert.Equal(t, "ok", string(data))
		return outPath, expectedQuery
	}

	buildMux := func(gotPath *string, gotQuery *string) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/jobs/"+jobID+"/artifacts", func(w http.ResponseWriter, r *http.Request) {
			resp := artifactsResponse{
				JobID: jobID,
				Artifacts: []artifactInfo{
					{Name: "run-log", Path: artifactPath},
				},
			}
			writeJSON(t, w, http.StatusOK, resp)
		})
		mux.HandleFunc("/v1/jobs/"+jobID+"/artifacts/download", func(w http.ResponseWriter, r *http.Request) {
			*gotPath = r.URL.Path
			*gotQuery = r.URL.RawQuery
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
		return mux
	}

	t.Run("unix socket", func(t *testing.T) {
		var gotPath, gotQuery string
		mux := buildMux(&gotPath, &gotQuery)
		socketPath := startUnixHTTPServer(t, mux)
		base := commonFlags{socketPath: socketPath, jsonOutput: true, timeout: time.Second}
		_, expected := run(t, base)
		assert.Equal(t, "/v1/jobs/"+jobID+"/artifacts/download", gotPath)
		assert.Equal(t, expected, gotQuery)
	})

	t.Run("remote endpoint", func(t *testing.T) {
		var gotPath, gotQuery string
		mux := buildMux(&gotPath, &gotQuery)
		server := httptest.NewServer(mux)
		defer server.Close()

		base := commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second}
		_, expected := run(t, base)
		assert.Equal(t, "/v1/jobs/"+jobID+"/artifacts/download", gotPath)
		assert.Equal(t, expected, gotQuery)
	})
}
