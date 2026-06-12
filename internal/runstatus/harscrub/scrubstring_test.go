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
