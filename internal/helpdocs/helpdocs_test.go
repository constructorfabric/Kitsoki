package helpdocs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The handler must never error regardless of whether the site is staged:
// staged → static files; unstaged → the actionable placeholder on every path.
func TestHandlerAlwaysServes(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/", "/index.html", "/guide/getting-started.html", "/nope"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		res := rec.Result()
		body, _ := io.ReadAll(res.Body)

		if Built() {
			// Staged: real files 200, unknown paths 404 — but never 5xx.
			if res.StatusCode >= 500 {
				t.Errorf("GET %s: staged handler returned %d", path, res.StatusCode)
			}
		} else {
			// Unstaged: every path gets the placeholder, 503.
			if res.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("GET %s: want 503 placeholder, got %d", path, res.StatusCode)
			}
			if !strings.Contains(string(body), "make site-embed") {
				t.Errorf("GET %s: placeholder must name the staging command", path)
			}
		}
	}
}
