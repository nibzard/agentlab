package dashboard

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRemoteScopePreserved(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer scoped" {
			t.Error("missing scoped credential")
		}
		if r.URL.Path != "/v1/sandboxes/100" {
			t.Error(r.URL.Path)
		}
		w.Header().Set("X-Agentlab-Dashboard-Auth", "required")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer remote.Close()
	s := NewServer(Config{Endpoint: remote.URL, SocketPath: "/must-not-use", Token: "scoped"}, log.New(io.Discard, "", 0))
	w := httptest.NewRecorder()
	s.forward(w, http.MethodDelete, "/v1/sandboxes/100", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("scope response: %d", w.Code)
	}
	if w.Header().Get("X-Agentlab-Dashboard-Auth") != "" {
		t.Fatal("upstream response impersonated browser enrollment")
	}
}

func TestEnrollmentRejectsExpiredAndCrossOrigin(t *testing.T) {
	s := NewServer(Config{}, nil)
	if err := s.ensureBrowserToken(); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://evil.example", "http://localhost"} {
		s.enrollmentExpiry = time.Now().Add(-time.Minute)
		r := httptest.NewRequest(http.MethodPost, "http://localhost/auth/enroll", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		r.Header.Set("Authorization", "Bearer "+s.browserToken)
		w := httptest.NewRecorder()
		s.enrollBrowser(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("accepted invalid enrollment: %d", w.Code)
		}
	}
}
