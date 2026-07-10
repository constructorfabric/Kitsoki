package app_test

// Reproduction for bug 44:
//
//	bf's iface.ticket.comment/transition ignore kitsoki-dev's
//	host.gh.ticket rebind (falls back to host.local_files.ticket,
//	closing GitHub issues silently fails).
//
// A parent app can rebind the `ticket` capability onto the GitHub provider:
//
//	imports.core.host_bindings:
//	  ticket: host.gh.ticket
//
// dev-story (imported as `core`) declares its own `ticket` iface AND
// transitively imports the bugfix story (alias `bf`), which declares its
// own `ticket` iface too. When bf's iface is lifted through dev-story it
// becomes `bf__ticket`, and through the parent `core__bf__ticket`. The
// parent's single `ticket:` binding key only matches dev-story's own
// `ticket` iface (→ core__ticket), so `core__bf__ticket` never gets
// rebound and keeps its default host.local_files.ticket.
//
// Concretely: the bugfix `done` room closes the fixed ticket via
//
//	invoke: iface.ticket.comment      (post the fix close-out)
//	invoke: iface.ticket.transition   (move the ticket to "resolved")
//
// In a GitHub-backed instance these MUST reach the rebound GitHub
// provider so the GitHub issue is actually commented + closed. Today
// they resolve to host.local_files.ticket.{comment,transition}: the
// close-out is written to a local file and the GitHub issue is never
// touched — a silent no-op from the operator's perspective.
//
// This test loads a real dev-story import with `ticket` rebound to
// host.gh.ticket and asserts the OUTCOME the operator depends on: no
// ticket operation in the transitive bugfix child may still dispatch to
// host.local_files.ticket. It fails RED when the rebind does not cascade
// to nested imports and passes for ANY fix that makes the top-level
// binding reach the imported child's ticket iface.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"kitsoki/internal/app"
)

// collectTicketInvokes walks an effect list (including nested inline
// effects and on_complete chains) and records every host invoke target.
func collectResolvedInvokes(effs []app.Effect, out map[string]bool) {
	for i := range effs {
		if effs[i].Invoke != "" {
			out[effs[i].Invoke] = true
		}
		collectResolvedInvokes(effs[i].Effects, out)
		collectResolvedInvokes(effs[i].OnComplete, out)
	}
}

// walkResolvedInvokes recurses through the state tree collecting every
// resolved host invoke from on_enter and transition effects.
func walkResolvedInvokes(states map[string]*app.State, out map[string]bool) {
	for _, s := range states {
		if s == nil {
			continue
		}
		collectResolvedInvokes(s.OnEnter, out)
		for _, list := range s.On {
			for i := range list {
				collectResolvedInvokes(list[i].Effects, out)
			}
		}
		walkResolvedInvokes(s.States, out)
	}
}

func TestRepro_Bug44_BfTicketRebindReachesGitHub(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("infra: resolve repo root: %v", err)
	}
	appDir := t.TempDir()
	appYAML := filepath.Join(appDir, "app.yaml")
	devStory := filepath.Join(repoRoot, "stories", "dev-story")
	if err := os.WriteFile(appYAML, []byte(fmt.Sprintf(`
app: { id: ticket-rebind-repro, version: 0.1.0 }
root: core
hosts: []
imports:
  core:
    source: %q
    entry: landing
    host_bindings:
      ticket: host.gh.ticket
    exits:
      work_merged:
        to: done
      needs_human:
        to: needs_human
states:
  done:
    terminal: true
  needs_human:
    terminal: true
`, devStory)), 0o644); err != nil {
		t.Fatalf("infra: write parent app: %v", err)
	}

	def, err := app.Load(appYAML)
	if err != nil {
		t.Fatalf("infra: load explicit ticket-rebind app: %v", err)
	}

	invokes := map[string]bool{}
	walkResolvedInvokes(def.States, invokes)

	// After the parent rebinds `ticket: host.gh.ticket`, every ticket
	// operation the composed app dispatches must reach the GitHub provider.
	// Any surviving host.local_files.ticket.* invoke is a ticket op that
	// ignored the rebind.
	var strays []string
	for inv := range invokes {
		if strings.HasPrefix(inv, "host.local_files.ticket.") {
			strays = append(strays, inv)
		}
	}
	sort.Strings(strays)

	if len(strays) > 0 {
		t.Fatalf("bug 44: %d ticket op(s) ignored the kitsoki-dev `ticket: host.gh.ticket` rebind "+
			"and still dispatch to the local-files provider (GitHub issue close/comment silently no-ops): %v\n"+
			"  expected: every iface.ticket.* call — including bf's done-room comment + transition — "+
			"resolves to host.gh.ticket.*",
			len(strays), strays)
	}
}
