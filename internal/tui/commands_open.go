package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/tui/blocks"
)

// commands_open.go — the `/open <path>` slash command: the universal,
// keyboard, terminal-agnostic way to open a markdown (or any) artifact the
// TUI is pointing at. It is the guaranteed fallback for terminals without
// OSC 8 hyperlink support (where a `.md` kv value renders as plain text) and
// the verb the review-diff room points at for "open the changed files".
//
// It lives alongside the `/ide` family (commands_ide.go) and resolves a
// relative path the same way the `media` element's paths are rooted — against
// the run's working directory (where `.artifacts/...` paths live) — then hands
// the file to the operator's surface: $EDITOR when set (the only
// operator-controllable, preferred-reader path), else the OS default opener
// (`open` on macOS, `xdg-open` on Linux). See docs/tui/README.md.

// handleOpenSlash resolves <path> against the run's working directory and
// opens it. With no argument it prints usage. Resolution and the report are
// synchronous; the actual open is delegated to m.openArtifact (the injected
// seam) so tests exercise this without launching a real opener.
func (m RootModel) handleOpenSlash(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.transcript.AppendBlock(m.openBlock("usage: /open <path> — opens an artifact in $EDITOR or the OS default"))
		return m, nil
	}

	// Join the remaining fields so an (unquoted) path with spaces still
	// resolves — the prompt splitter already tokenized on whitespace.
	rel := strings.Join(args, " ")
	abs := rel
	if !filepath.IsAbs(rel) {
		// Root relative paths at the run's working directory, matching how
		// the media element's `.artifacts/...` paths are displayed/rooted.
		if cwd, err := os.Getwd(); err == nil {
			abs = filepath.Join(cwd, rel)
		}
	}
	abs = filepath.Clean(abs)

	if _, err := os.Stat(abs); err != nil {
		m.transcript.AppendBlock(m.openBlock(fmt.Sprintf("not found: %s", rel)))
		return m, nil
	}

	open := m.openArtifact
	if open == nil {
		open = osOpenArtifact
	}
	if err := open(abs); err != nil {
		m.transcript.AppendBlock(m.openBlock(fmt.Sprintf("could not open %s: %v", rel, err)))
		return m, nil
	}
	m.transcript.AppendBlock(m.openBlock(fmt.Sprintf("opened %s", rel)))
	return m, nil
}

// osOpenArtifact is the production opener: it prefers $EDITOR (operator-
// controllable, respects their preferred markdown reader) and otherwise
// falls back to the platform OS opener (`open` / `xdg-open`). It starts the
// process detached and returns once it has launched — it does not wait for
// the editor to close, so the TUI stays responsive.
func osOpenArtifact(path string) error {
	if ed := strings.TrimSpace(os.Getenv("EDITOR")); ed != "" {
		// $EDITOR may carry flags (e.g. "code --wait"); split on spaces.
		fields := strings.Fields(ed)
		cmd := exec.Command(fields[0], append(fields[1:], path)...)
		return cmd.Start()
	}
	var bin string
	switch runtime.GOOS {
	case "darwin":
		bin = "open"
	default:
		bin = "xdg-open"
	}
	return exec.Command(bin, path).Start()
}

// openBlock renders a one-line `/open` chat block via the blocks renderer
// (SlashOutput) — no hand-rolled ANSI, matching ideBlock.
func (m RootModel) openBlock(line string) string {
	return blocks.New(m.transcript.width, m.currentTheme()).SlashOutput("open: " + line)
}
