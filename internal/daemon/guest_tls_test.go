package daemon

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestGuestTLSPinPersistsAndRejectsUntrustedClient(t *testing.T) {
	dir := t.TempDir()
	ln, pin, e := guestTLS(dir, "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })}
	defer server.Close()
	go server.Serve(ln)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(pin)) {
		t.Fatal("invalid pin")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	url := "https://" + ln.Addr().String()
	resp, e := client.Get(url)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	bad := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()}}}
	if resp, e = bad.Get(url); e == nil {
		resp.Body.Close()
		t.Fatal("untrusted controller accepted")
	}
	server.Close()
	again, same, e := guestTLS(dir, "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	again.Close()
	if same != pin {
		t.Fatal("restart changed guest trust")
	}
	if e = os.Remove(filepath.Join(dir, "server.crt")); e != nil {
		t.Fatal(e)
	}
	if ln, _, e = guestTLS(dir, "127.0.0.1:0"); e == nil {
		ln.Close()
		t.Fatal("lost certificate silently changed trust")
	}
}
