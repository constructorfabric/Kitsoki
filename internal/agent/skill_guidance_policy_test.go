package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var unmanagedSkillGuidance = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{
		name:    "raw Git worktree lifecycle",
		pattern: regexp.MustCompile(`(?i)\bgit(?:\s+-C\s+\S+)?\s+worktree\b`),
	},
	{
		name:    "legacy .worktrees path",
		pattern: regexp.MustCompile(`(?:^|[[:space:]` + "`" + `'"(])\.worktrees/`),
	},
	{
		name:    "ad hoc Kitsoki binary build",
		pattern: regexp.MustCompile(`(?i)\bgo\s+build\b[^\n]*(?:\./cmd/kitsoki|(?:^|[/[:space:]])kitsoki(?:[-/[:space:]]|$))`),
	},
	{
		name:    "throwaway Kitsoki binary build",
		pattern: regexp.MustCompile(`(?i)\b(?:build|compile)\b[^\n]{0,60}\b(?:temporary|throwaway|fresh|named|ad[ -]?hoc)\b[^\n]{0,40}\b(?:binary|executable)\b`),
	},
}

// TestProjectSkillsUseManagedCapsuleGuidance keeps active skills aligned with
// AGENTS.md. Supported Make targets may own packaging/signing for UI capture;
// this guard rejects agent-authored Git lifecycle and ad hoc compiler commands.
func TestProjectSkillsUseManagedCapsuleGuidance(t *testing.T) {
	root := skillPolicyRepoRoot(t)
	paths, err := projectSkillTextFiles(filepath.Join(root, ".agents", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no active project skills found")
	}

	var violations []string
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		previous := ""
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := scanner.Text()
			for _, rule := range unmanagedSkillGuidance {
				if rule.pattern.MatchString(line) && !skillGuidanceExempt(previous, line) {
					rel, _ := filepath.Rel(root, path)
					violations = append(violations, fmt.Sprintf("%s:%d: %s: %s", rel, lineNumber, rule.name, strings.TrimSpace(line)))
				}
			}
			previous = line
		}
		if err := scanner.Err(); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("active skills must delegate development lifecycle to managed Capsule tooling and use go run for source-precise Kitsoki checks:\n%s\nFor a historical/context-only mention, add `<!-- skill-policy: allow-next-line historical-context: <reason> -->` immediately above it.", strings.Join(violations, "\n"))
	}
}

func projectSkillTextFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "assets" || entry.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".md", ".py", ".sh", ".yaml", ".yml", ".json", ".toml":
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func TestSkillGuidancePolicyClassification(t *testing.T) {
	tests := []struct {
		name     string
		previous string
		line     string
		want     bool
	}{
		{name: "raw lifecycle", line: "git worktree add path branch", want: true},
		{name: "raw lifecycle with cwd", line: "git -C repo worktree remove path", want: true},
		{name: "legacy path", line: "edit .worktrees/cell/file", want: true},
		{name: "ad hoc build", line: "go build -o /tmp/kitsoki ./cmd/kitsoki", want: true},
		{name: "prohibition", line: "Do not run raw git worktree commands", want: false},
		{name: "build prohibition", line: "Do not build a temporary Kitsoki binary", want: false},
		{name: "historical marker", previous: "<!-- skill-policy: allow-next-line historical-context: old trace text -->", line: "git worktree list", want: false},
		{name: "supported source invocation", line: "go run ./cmd/kitsoki capsule ci doctor change", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := false
			for _, rule := range unmanagedSkillGuidance {
				if rule.pattern.MatchString(test.line) && !skillGuidanceExempt(test.previous, test.line) {
					got = true
					break
				}
			}
			if got != test.want {
				t.Fatalf("violation = %t, want %t", got, test.want)
			}
		})
	}
}

func skillGuidanceExempt(previous, line string) bool {
	if strings.Contains(strings.ToLower(previous), "skill-policy: allow-next-line historical-context:") {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(line))
	mentionsLifecycle := strings.Contains(lower, "git worktree")
	forbidsLifecycle := mentionsLifecycle && (strings.Contains(lower, "do not") ||
		strings.Contains(lower, "never") ||
		strings.Contains(lower, "rather than"))
	forbidsBuild := strings.HasPrefix(lower, "do not build ") ||
		strings.HasPrefix(lower, "never build ") ||
		strings.HasPrefix(lower, "do not compile ") ||
		strings.HasPrefix(lower, "never compile ")
	return forbidsLifecycle || forbidsBuild
}

func skillPolicyRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve skill policy test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
