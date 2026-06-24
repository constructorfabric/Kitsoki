package mcp

import (
	"strings"
	"testing"
)

// TestValidateJQL exercises the format-validator function directly. It uses
// the unexported name (same package) so we don't have to plumb an export.
// Each row is a representative case from the trace + the false-positive
// boundary cases we explicitly want to be safe against.
func TestValidateJQL(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		wantErr bool
		errFrag string // optional substring to check on error
	}{
		{name: "empty string", input: "", wantErr: true, errFrag: "empty"},
		{name: "whitespace only", input: "   \t  ", wantErr: true, errFrag: "empty"},
		{name: "non-string int", input: 42, wantErr: true, errFrag: "string"},
		{name: "non-string nil", input: nil, wantErr: true, errFrag: "string"},

		// The smoking-gun case from the devstory router trace.
		{name: "natural language", input: "open presentation service bugs", wantErr: true, errFrag: "natural language"},

		// Bare issue keys.
		{name: "bare issue key PLTFRM", input: "PLTFRM-123", wantErr: false},
		{name: "bare issue key DBI", input: "DBI-9876", wantErr: false},
		{name: "bare issue key with underscore", input: "AB_TEST-1", wantErr: false},

		// Operator forms.
		{name: "equals operator", input: "issuetype = Bug", wantErr: false},
		{name: "tilde operator with quoted value", input: "text ~ 'foo'", wantErr: false},
		{name: "less-than operator", input: "priority < 3", wantErr: false},
		{name: "not-equals operator", input: "status != Done", wantErr: false},

		// Keyword in operator position.
		{name: "in operator", input: "assignee in (a, b)", wantErr: false},
		{name: "is empty", input: "resolution is empty", wantErr: false},

		// "is" must keep working when followed by other keywords.
		{name: "is-not-empty after a non-jql substring", input: "winner is not empty", wantErr: false},

		// False-positive boundary cases.
		{name: "winning team natural language", input: "winning team", wantErr: true, errFrag: "natural language"},

		// "isbn = 9780" contains `=`, so it MUST be accepted (the test
		// documents that we don't false-reject when an operator is
		// present, even if the field name is unusual).
		{name: "field name containing is-substring with =", input: "isbn = 9780", wantErr: false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateJQL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("validateJQL(%q): want error, got nil", tc.input)
				}
				if tc.errFrag != "" && !strings.Contains(err.Error(), tc.errFrag) {
					t.Fatalf("validateJQL(%q): error %q does not contain %q", tc.input, err, tc.errFrag)
				}
			} else {
				if err != nil {
					t.Fatalf("validateJQL(%v): want nil, got error %q", tc.input, err)
				}
			}
		})
	}
}
