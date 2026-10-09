package harscrub

import (
	"regexp"
	"strings"
	"testing"
)

func TestScrubString(t *testing.T) {
	opts := ScrubOptions{
		Home:           "/Users/alice",
		SecretPatterns: []*regexp.Regexp{regexp.MustCompile(`hunter2`)},
	}
	in := "trace at /Users/alice/code/app.log password=hunter2 ok"
	got := ScrubString(in, opts)

	if strings.Contains(got, "/Users/alice") {
		t.Fatalf("home path not redacted: %q", got)
	}
	if !strings.Contains(got, "$HOME/code/app.log") {
		t.Fatalf("expected $HOME substitution: %q", got)
	}
	if strings.Contains(got, "hunter2") {
		t.Fatalf("secret not redacted: %q", got)
	}
	if !strings.Contains(got, Redacted) {
		t.Fatalf("expected %s in output: %q", Redacted, got)
	}
}

func TestScrubStringEmpty(t *testing.T) {
	if got := ScrubString("", ScrubOptions{Home: "/x"}); got != "" {
		t.Fatalf("empty input should stay empty, got %q", got)
	}
}

func TestScrubStringReportHighEntropy(t *testing.T) {
	opaque := "mF9xQ2rT8vLp0AqZ7nByC4dEuGhJkM3sW6yI"
	got, report := ScrubStringReport("session="+opaque+" sha=abcdef1234567890abcdef1234567890", ScrubOptions{})
	if strings.Contains(got, opaque) {
		t.Fatalf("high-entropy value leaked: %q", got)
	}
	if !strings.Contains(got, Redacted) {
		t.Fatalf("expected high-entropy replacement in %q", got)
	}
	if !strings.Contains(got, "abcdef1234567890abcdef1234567890") {
		t.Fatalf("hex-only hashes should stay intact: %q", got)
	}
	findings := report.Findings()
	if len(findings) != 1 || findings[0].Category != "high_entropy" || findings[0].Count != 1 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

func TestScrubStringReportHighEntropyDoesNotConsumePaths(t *testing.T) {
	path := "/var/folders/l2/rpyfg5h56998lnw93074br640000gp/T/TestIssueCreate_BundlesEvidenceAndFiles2341117700/001/screen.png"
	got, report := ScrubStringReport("asset="+path, ScrubOptions{})
	if got != "asset="+path {
		t.Fatalf("path should stay intact, got %q", got)
	}
	if !report.Empty() {
		t.Fatalf("path should not produce findings: %#v", report.Findings())
	}
}
