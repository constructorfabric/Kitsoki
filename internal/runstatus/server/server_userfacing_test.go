package server

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestServerErr_SanitizesMessageKeepsData locks the web-surface half of the
// user-facing error fix: serverErr must hand the SPA a sanitized Message (no
// absolute paths, no Go format artifacts, no orchestrator wrapper prefixes)
// while preserving the full chain in Data for logs/dev tools. Before this the red
// banner rendered the raw wrapped error verbatim.
func TestServerErr_SanitizesMessageKeepsData(t *testing.T) {
	raw := fmt.Errorf("runstatus.session.get: orchestrator: load HAR: write %s: %w",
		"/Users/brad/.cache/kitsoki/har.json", os.ErrPermission)
	re := serverErr(raw)

	for _, bad := range []string{"/Users/brad", "/.cache/", "%w", "orchestrator:"} {
		if strings.Contains(re.Message, bad) {
			t.Errorf("serverErr Message = %q, must not leak %q to the user", re.Message, bad)
		}
	}
	if !strings.Contains(strings.ToLower(re.Message), "permission denied") {
		t.Errorf("serverErr Message = %q, want the actionable leaf preserved", re.Message)
	}
	if re.Data != raw.Error() {
		t.Errorf("serverErr Data = %q, want the full raw chain %q", re.Data, raw.Error())
	}
}
