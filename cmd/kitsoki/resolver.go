package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"kitsoki/internal/app"
	"kitsoki/internal/basestories"
	"kitsoki/internal/kitrepo"
)

// buildImportResolver constructs the app.ImportResolver the loader uses to
// resolve `@kitsoki/<name>` sources that find no on-disk kitsoki checkout. It
// is the DI seam (CLAUDE.md: no package globals) carrying the `--kitsoki-repo`
// override and the embedded story library into the import system.
//
// The override is read from $KITSOKI_REPO, which the root command's
// PersistentPreRunE populates from the `--kitsoki-repo` flag (flag wins over a
// pre-existing env value) and from kitrepo.Resolve. Reading the env here — not
// a captured flag variable — keeps the resolver buildable from every load site
// without threading a value through each subcommand, and matches how every
// other engine-targeting feature already consumes $KITSOKI_REPO.
//
// The returned resolver honours app.ImportResolver's two-call contract:
//
//   - override=true  → return <repo>/stories/<name>/app.yaml when $KITSOKI_REPO
//     is set, erroring if that story is missing there (an explicit override
//     pointing at the wrong tree must fail loudly, never silently fall back to
//     the embedded copy); return ("",nil) when no override is set.
//   - override=false → materialize the embedded library and return
//     <root>/<name>/app.yaml, erroring if the embedded library lacks the story.
func buildImportResolver() app.ImportResolver {
	return func(name, _ string, override bool) (string, error) {
		if override {
			repo := os.Getenv(kitrepo.EnvVar)
			if repo == "" {
				return "", nil // no override configured; fall through
			}
			candidate := filepath.Join(repo, "stories", name, "app.yaml")
			if _, err := os.Stat(candidate); err != nil {
				return "", fmt.Errorf("%s=%s: story %q not found (looked for %s): %w",
					kitrepo.EnvVar, repo, name, candidate, err)
			}
			return candidate, nil
		}

		// Embedded-library fallback.
		root, err := basestories.Materialize(context.Background())
		if err != nil {
			return "", fmt.Errorf("resolve @kitsoki/%s from embedded library: %w", name, err)
		}
		candidate := filepath.Join(root, name, "app.yaml")
		if _, statErr := os.Stat(candidate); statErr != nil {
			return "", fmt.Errorf("@kitsoki/%s: not in the embedded story library (looked for %s): %w",
				name, candidate, statErr)
		}
		return candidate, nil
	}
}
