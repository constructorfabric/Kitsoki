package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRepositoryDoesNotNamePrivateExternalRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	out, err := exec.Command(git, "-C", repoRoot, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}

	prefix := "cyber"
	forbidden := regexp.MustCompile(`(?i)\b` + prefix + `(?:[-_ ]repo|repo)\b`)
	var hits []string
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		path := filepath.Join(repoRoot, string(raw))
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if forbidden.Match(body) {
			hits = append(hits, string(raw))
		}
	}
	if len(hits) > 0 {
		t.Fatalf("tracked files contain forbidden private external repo references:\n%s", strings.Join(hits, "\n"))
	}
}
