package mining

import (
	"os"
	"path/filepath"
	"strings"
)

// Slug encodes an absolute repo/worktree path into the Claude Code projects
// slug, mirroring tools/session-mining/recap.sh:49-51 exactly:
//
//	SLUG="$(printf '%s' "$DIR" | sed 's#[/.]#-#g')"
//
// — every '/' and '.' in the path becomes '-'. The transform is pure and the
// single source of truth for the seed→live resolver; see
// docs/architecture/ambient-mining.md.
func Slug(repoPath string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(repoPath)
}

// TranscriptResolver maps a repo path to the Claude Code transcript directory
// that holds its session history (~/.claude/projects/<slug>). It is the
// deterministic, no-LLM history-seed resolver — the Go port of recap.sh's
// PROJ="$HOME/.claude/projects/$SLUG". HomeDir is injectable so tests resolve
// against a fixture dir instead of the real ~/.claude.
type TranscriptResolver struct {
	// HomeDir is the home directory the resolver bases ~/.claude on. Empty ⇒
	// os.UserHomeDir() (the production default).
	HomeDir string
}

// home returns the resolver's effective home directory.
func (r TranscriptResolver) home() (string, error) {
	if r.HomeDir != "" {
		return r.HomeDir, nil
	}
	return os.UserHomeDir()
}

// Dir returns the resolved ~/.claude/projects/<slug> transcript directory for
// repoPath. repoPath is cleaned to an absolute path first (recap.sh runs
// `cd "$DIR" && pwd`); a relative path is resolved against the process cwd.
// The returned path is not guaranteed to exist — call Exists or Resolve for
// presence-aware resolution.
func (r TranscriptResolver) Dir(repoPath string) (string, error) {
	home, err := r.home()
	if err != nil {
		return "", err
	}
	abs := repoPath
	if !filepath.IsAbs(abs) {
		if a, aerr := filepath.Abs(abs); aerr == nil {
			abs = a
		}
	}
	abs = filepath.Clean(abs)
	return filepath.Join(home, ".claude", "projects", Slug(abs)), nil
}

// Resolve returns the full ordered set of transcript directories that feed a
// mining pass for repoPath: the resolved ~/.claude/projects/<slug> first (when
// it exists on disk), then every extra dir in extraDirs that exists. A repo
// with no Claude Code history and no extra dirs resolves to an empty slice —
// the caller treats that as "nothing to mine", not an error. This is the
// presence-aware counterpart to Dir, matching recap.sh's `[ ! -d "$PROJ" ]`
// guard while still honouring mining.transcript_dirs.
func (r TranscriptResolver) Resolve(repoPath string, extraDirs []string) ([]string, error) {
	primary, err := r.Dir(repoPath)
	if err != nil {
		return nil, err
	}
	var dirs []string
	if isDir(primary) {
		dirs = append(dirs, primary)
	}
	for _, d := range extraDirs {
		if d != "" && isDir(d) {
			dirs = append(dirs, d)
		}
	}
	return dirs, nil
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
