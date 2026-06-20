package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ideasPathOverride, when set, is the exact file /ideas appends to. It
// exists so tests can point at a temp file without chdir'ing the whole
// process. In production it stays empty and ideasTargetPath resolves the
// git toplevel instead.
var ideasPathOverride string

// ideasFileName is the basename /ideas writes at the project root.
const ideasFileName = "ideas.md"

// ideasTargetPath resolves where /ideas appends: the test override if
// set, else <git toplevel>/ideas.md so the file lands at the project
// root regardless of where kitsoki was launched (a story subdir, a
// worktree, etc). Falls back to a cwd-relative name when no enclosing
// git repo is found, so the command still does something sane outside a
// checkout.
func ideasTargetPath() string {
	if ideasPathOverride != "" {
		return ideasPathOverride
	}
	if root, err := gitToplevel(); err == nil {
		return filepath.Join(root, ideasFileName)
	}
	return ideasFileName
}

// gitToplevel walks up from the working directory until it finds a .git
// entry (a directory in a normal checkout, a file in a worktree or
// submodule) and returns that directory. Pure-Go so the TUI never shells
// out for a path it can resolve itself.
func gitToplevel() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .git found at or above %s", dir)
		}
		dir = parent
	}
}

// IdeasCommand implements `/ideas <text>` — a deterministic capture
// command. It appends a single bullet line to ideas.md and renders a
// confirmation block; it never touches the agent. The point is to let
// an operator jot a thought mid-conversation and move on: no turn is
// dispatched, world state is untouched, and the on-path room keeps doing
// whatever it was doing. This is the deterministic-execution principle
// applied to the smallest possible action — a file append with zero
// interpretive steps.
type IdeasCommand struct{}

func (IdeasCommand) Name() string { return "/ideas" }

func (IdeasCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	idea := strings.TrimSpace(strings.Join(args, " "))
	if idea == "" {
		return blockSlashLine(m, "(ideas: usage: /ideas <your idea> — appends a line to ideas.md)"), m, nil
	}
	path := ideasTargetPath()
	abs, err := appendIdeaLine(path, idea)
	if err != nil {
		return blockSlashLine(m, fmt.Sprintf("(ideas: could not write %s: %v)", path, err)), m, nil
	}
	return blockSlashLine(m, fmt.Sprintf("(jotted to %s)", abs)), m, nil
}

// appendIdeaLine appends "- <idea>\n" to the file at path, creating it if
// absent. It prefixes a newline only when the existing file is non-empty
// and lacks a trailing one, so the bullet never glues onto a prior line
// nor injects a blank gap. Returns the absolute path for the
// confirmation message.
func appendIdeaLine(path, idea string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return abs, err
	}
	defer f.Close()

	var prefix string
	if info, statErr := f.Stat(); statErr == nil && info.Size() > 0 {
		buf := make([]byte, 1)
		if _, rerr := f.ReadAt(buf, info.Size()-1); rerr == nil && buf[0] != '\n' {
			prefix = "\n"
		}
	}
	_, err = f.WriteString(prefix + "- " + idea + "\n")
	return abs, err
}
