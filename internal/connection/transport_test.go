package connection

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRedirectDoesNotChangeServer(t *testing.T) {
	reached := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	base, client := Transport(server.URL, "/unused", time.Second)
	req, _ := http.NewRequest("GET", base, nil)
	req.Header.Set("Authorization", "Bearer private")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if reached || resp.StatusCode != 307 {
		t.Fatal("redirect changed the selected server")
	}
}

func TestRejectEndpointCredential(t *testing.T) {
	if _, err := NormalizeEndpoint("https://user:secret@example.com"); err == nil {
		t.Fatal("accepted embedded credentials")
	}
}
