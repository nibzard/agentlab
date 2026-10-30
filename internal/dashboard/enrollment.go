package dashboard

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"
)

// enrollBrowser exchanges the code for an HttpOnly session cookie. The daemon
// credential never crosses the browser boundary.
func (s *Server) enrollBrowser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !s.csrfOK(r) {
		http.Error(w, "cross-site request rejected", http.StatusForbidden)
		return
	}
	s.enrollmentMu.Lock()
	defer s.enrollmentMu.Unlock()
	code := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.browserToken == "" || subtle.ConstantTimeCompare([]byte(code), []byte(s.browserToken)) != 1 ||
		(s.generatedToken && (s.enrollmentUsed || time.Now().After(s.enrollmentExpiry))) {
		http.Error(w, "invalid or expired enrollment code; restart the dashboard", http.StatusUnauthorized)
		return
	}
	if s.sessionToken == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			http.Error(w, "session unavailable", http.StatusInternalServerError)
			return
		}
		s.sessionToken = base64.RawURLEncoding.EncodeToString(buf)
	}
	s.enrollmentUsed = true
	http.SetCookie(w, &http.Cookie{Name: "dashboard_token", Value: s.sessionToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	w.WriteHeader(http.StatusNoContent)
}
