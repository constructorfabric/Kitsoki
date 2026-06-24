package main

import "testing"

// issue_filer_test.go — guards the label-error heuristic that decides whether
// ghIssueFiler degrades to an unlabelled create. The degrade path is what keeps
// the studio's issue.create self-improvement loop operable on a repo where the
// caller lacks triage (a fork contributor on constructorfabric/Kitsoki). The
// exec orchestration is tested at the studio seam (an injected fake IssueFiler);
// here we pin the pure decision the orchestration branches on.

func TestLooksLikeLabelErr(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   bool
	}{
		{
			// The exact failure the dogfood driver hit (issue 092411's sibling):
			// label-create 403'd, then create rejected the unknown label.
			name:   "could not add label",
			stderr: "could not add label: 'source-autonomous' not found",
			want:   true,
		},
		{
			name:   "fork contributor 403",
			stderr: "GraphQL: Resource not accessible by integration (addLabelsToLabelable)",
			want:   true,
		},
		{
			name:   "no permission to label",
			stderr: "you do not have permission to add labels to this issue",
			want:   true,
		},
		{
			// An auth failure is NOT a label error — must stay fatal so we don't
			// retry-and-mask a genuine credential problem as a silent unlabelled file.
			name:   "auth failure is fatal",
			stderr: "gh: To get started with GitHub CLI, please run: gh auth login",
			want:   false,
		},
		{
			name:   "unknown repo is fatal",
			stderr: "GraphQL: Could not resolve to a Repository with the name 'owner/nope'.",
			want:   false,
		},
		{
			name:   "empty stderr",
			stderr: "",
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeLabelErr(tc.stderr); got != tc.want {
				t.Fatalf("looksLikeLabelErr(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}

func TestIssueNumberFromURL(t *testing.T) {
	cases := map[string]int{
		"https://github.com/constructorfabric/Kitsoki/issues/123": 123,
		"https://github.com/owner/repo/issues/7":                  7,
		"https://github.com/owner/repo/issues/":                   0, // trailing slash, no number
		"not-a-url":                                               0,
		"":                                                        0,
	}
	for url, want := range cases {
		if got := issueNumberFromURL(url); got != want {
			t.Errorf("issueNumberFromURL(%q) = %d, want %d", url, got, want)
		}
	}
}
