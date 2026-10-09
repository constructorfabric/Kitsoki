package studio

// app_dir.go — publishes KITSOKI_APP_DIR for an MCP-driven session (issue #37 /
// decomposition 2.7).
//
// host.starlark.run (internal/host/starlark_run.go) resolves a relative
// `with.script` path against KITSOKI_APP_DIR at DISPATCH time — the same
// process-global env var cmd/kitsoki's loadAppWithEnv publishes before every
// CLI entry point's app.Load. The studio session runner never published it, so
// a story loaded purely through session.new/session.attach (no CLI in the
// loop) could not resolve its own relative Starlark scripts: the read failed
// as an infra error that the machine absorbs into world.last_error/host_error,
// silently leaving the room's bound output at its zero value.
//
// publishAppDir mirrors cmd/kitsoki's helper of the same name exactly (same
// Abs+Dir derivation from the app.yaml path) so the two entry points agree on
// what "the app dir" means. It must run BEFORE the story loads, matching the
// CLI's ordering requirement: the loader's env-var validator
// (expandMetaCwd/`${KITSOKI_APP_DIR}`) fires during app.Load and errors out if
// the var is unset, so a post-load setenv is too late.
//
// KITSOKI_APP_DIR is a process-global env var, so two concurrent live
// sessions on different stories will race — whichever session.new call ran
// last wins the global. That is the same limitation the CLI already accepts
// for this var, and distinct from the per-session prompt-renderer path
// host.agent.task's schema resolution now uses (see
// TestConcurrentSessions_SchemaResolvesPerSession). Fixing that generally is
// out of scope here: this file only closes the gap that made
// host.starlark.run unusable from a studio-opened session at all.

import (
	"os"
	"path/filepath"

	"kitsoki/internal/host"
)

func publishAppDir(storyPath string) {
	if storyPath == "" {
		return
	}
	if absPath, err := filepath.Abs(storyPath); err == nil {
		_ = os.Setenv(host.AppDirEnv, filepath.Dir(absPath))
	}
}
