package app

import (
	"strings"
	"testing"
)

func TestAssignmentPolicy_LoadAndValidate(t *testing.T) {
	def, err := LoadBytes([]byte(`
app: { id: assignment-test, version: 0.1.0 }
root: build
states:
  build:
    assignment: { role: developer, required: true, allow_reassign: true, sync: linked-ticket }
`))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	p := def.States["build"].Assignment
	if p == nil || p.Role != "developer" || !p.Required || !p.AllowReassign || p.Sync != "linked-ticket" {
		t.Fatalf("assignment = %#v", p)
	}
	for _, body := range []string{
		`assignment: { required: true }`,
		`assignment: { role: developer, sync: overwrite }`,
	} {
		_, err := LoadBytes([]byte("app: { id: assignment-invalid, version: 0.1.0 }\nroot: room\nstates:\n  room:\n    " + body + "\n"))
		if err == nil || !strings.Contains(err.Error(), "assignment") {
			t.Fatalf("body %q error = %v", body, err)
		}
	}
}
