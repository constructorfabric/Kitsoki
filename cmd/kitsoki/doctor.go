package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"kitsoki/internal/baseskills"

	"github.com/spf13/cobra"
)

// doctorCmd — change 4.3: a preflight readiness report. Answers "is this
// machine set up to run kitsoki?" without the operator reverse-engineering it
// from a failed run: provider (agent backend), toolkit (embedded skills/agents
// staged into the binary), and MCP (studio registered for a coding agent).
func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   "Report provider / toolkit / MCP readiness",
		GroupID: "setup",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "kitsoki doctor — readiness")

			// provider: a live agent backend (claude CLI or an Anthropic credential).
			claude := false
			if _, err := exec.LookPath("claude"); err == nil {
				claude = true
			}
			cred := hasAnthropicCredential()
			switch {
			case claude:
				line(out, true, "provider", "claude CLI on PATH")
			case cred:
				line(out, true, "provider", "Anthropic credential found")
			default:
				line(out, false, "provider", "none — install claude, set ANTHROPIC_API_KEY, or pick a harness profile (only --harness replay works)")
			}

			// toolkit: the agent skills/agents embedded in this binary (project-tools
			// install / init needs them). ErrNotStaged means built without embeds.
			if _, err := baseskills.Materialize(context.Background()); err != nil {
				line(out, false, "toolkit", "not embedded — build with `make embed-skills` (or use a release binary)")
			} else {
				line(out, true, "toolkit", "agent skills/agents embedded")
			}

			// mcp: is the kitsoki studio MCP registered for a coding agent in this
			// project? (a .mcp.json in cwd naming the kitsoki server).
			mcpOK, mcpWhy := mcpReadiness()
			line(out, mcpOK, "mcp", mcpWhy)

			return nil
		},
	}
}

func line(out interface{ Write([]byte) (int, error) }, ok bool, label, detail string) {
	mark := "✓"
	if !ok {
		mark = "✗"
	}
	fmt.Fprintf(out, "  %s %-9s %s\n", mark, label, detail)
}

func mcpReadiness() (bool, string) {
	b, err := os.ReadFile(".mcp.json")
	if err != nil {
		return false, "no .mcp.json here — run `kitsoki init` to register the studio MCP for your coding agent"
	}
	if strings.Contains(string(b), "\"kitsoki\"") {
		return true, "kitsoki studio MCP registered in .mcp.json"
	}
	return false, ".mcp.json present but does not register the kitsoki server — run `kitsoki init`"
}

// initCmd — the *toolkit substep* of project onboarding (an alias for
// `project-tools install`): embedded skills/agents + the studio MCP
// registration. The full onboarding front door is the dev-story pipeline
// (`kitsoki run` → "onboard ."), which discovers a project profile and
// materializes a thin dev-story instance before calling this same install.
// When the target repo has no `.kitsoki.yaml` (i.e. it was never onboarded),
// init points the user at `onboard .` instead of silently looking complete.
func initCmd() *cobra.Command {
	var target string
	c := &cobra.Command{
		Use:     "init",
		Short:   "Install the agent toolkit + register the studio MCP (onboarding's toolkit substep)",
		Long:    "Alias for `kitsoki project-tools install`: copies the embedded skills/agents into the target project and registers the kitsoki studio MCP server so your coding agent can drive kitsoki.\n\nThis is the toolkit substep of full project onboarding. For the complete onboarding (project profile + dev-story instance + toolkit), run `kitsoki run` from the project root and type `onboard .` — see docs/getting-started.md.",
		GroupID: "setup",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(target)
			if err != nil {
				return err
			}
			rep, err := baseskills.Install(cmd.Context(), abs)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "installed kitsoki toolkit into %s\n", rep.Target)
			fmt.Fprintf(out, "  skills: %d linked into .claude/skills\n", len(rep.Skills))
			fmt.Fprintf(out, "  agents: %d linked into .claude/agents\n", len(rep.Agents))
			if rep.MCPWritten {
				fmt.Fprintf(out, "  mcp:    registered kitsoki server in %s\n", rep.MCPPath)
			} else {
				fmt.Fprintf(out, "  mcp:    kitsoki server already registered in %s\n", rep.MCPPath)
			}
			if hint := initOnboardPointer(hasProjectConfig(abs)); hint != "" {
				fmt.Fprint(out, hint)
			}
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", ".", "target project root to onboard")
	return c
}

// hasProjectConfig reports whether the target repo already carries a
// `.kitsoki.yaml` — the marker that full onboarding (profile + instance)
// has run there.
func hasProjectConfig(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".kitsoki.yaml"))
	return err == nil
}

// initOnboardPointer returns the pointer printed after `kitsoki init` when the
// target repo was never fully onboarded (no `.kitsoki.yaml`). init only
// installs the toolkit + MCP; the profile-driven onboarding is `onboard .`.
// Empty when the repo is already onboarded (or init is being used deliberately
// as the MCP-client-only mode on an onboarded repo).
func initOnboardPointer(hasProjectConfig bool) string {
	if hasProjectConfig {
		return ""
	}
	return `
note: no .kitsoki.yaml here — this repo is not fully onboarded yet.
  ` + "`kitsoki init`" + ` only installs the agent toolkit + studio MCP (fine if you
  just want an MCP client to drive kitsoki). For the full onboarding —
  project profile + dev-story instance + toolkit — run:

    kitsoki run        # then type: onboard .

  See docs/getting-started.md.
`
}

// tierHelp groups the ~48 top-level commands into readable tiers for the root
// help output (change 4.3). Commands not in a tier fall under cobra's default
// "Additional Commands" section.
func tierHelp(root *cobra.Command) {
	tiers := []struct {
		id, title string
		cmds      []string
	}{
		{"setup", "Setup & onboarding", []string{"init", "doctor", "project-tools", "project-profile", "docs", "materialize", "version"}},
		{"kits", "Kit dependencies", []string{"kit"}},
		{"drive", "Run & drive", []string{"run", "turn", "drive", "chat", "serve", "web", "tour", "inbox", "session", "status"}},
		{"author", "Author & test", []string{"validate", "test", "viz", "workflow", "prompts", "extract"}},
		{"debug", "Debug & trace", []string{"trace", "replay", "replay-routing", "inspect", "render", "shot", "web-shot", "export-status"}},
		{"agents", "Agents & bench", []string{"agent", "agent-serve", "agent-bench", "eval", "bug", "issues", "gh-agent", "record", "cassette", "migrate-agent"}},
		{"mcp", "MCP & integration", []string{"mcp", "mcp-test", "mcp-bash", "mcp-codeact", "mcp-validator", "mcp-operator-ask", "hook", "intercept", "ui"}},
	}
	byName := map[string]string{}
	for _, t := range tiers {
		root.AddGroup(&cobra.Group{ID: t.id, Title: t.title + ":"})
		for _, n := range t.cmds {
			byName[n] = t.id
		}
	}
	for _, c := range root.Commands() {
		if id, ok := byName[c.Name()]; ok {
			c.GroupID = id
		}
	}
}
