package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"kitsoki/internal/app"
	"kitsoki/internal/basestories"
	"kitsoki/internal/kitdev"
	"kitsoki/internal/kitrepo"
	"kitsoki/internal/kitstage"
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
//   - override=true  → first, staged-candidate resolution when the
//     invocation opted in via `--staged` / $KITSOKI_KIT_STAGED
//     (internal/kitstage, S7): a kit staged by `kitsoki kit update` resolves
//     to its pinned candidate tree. Staged beats the dev overrides below —
//     a trial explicitly asks to test the staged bytes, and a lingering
//     `kit dev` override must not silently shadow them. Next, the per-kit
//     `kitsoki kit dev <name> --path <checkout>` override (internal/kitdev,
//     S2): if set, return <checkout>/app.yaml, erroring if it's missing
//     there. This generalizes the single repo-wide override below to one kit
//     at a time — see internal/kitdev's package doc for why (D5,
//     contributing to a kit that no longer lives inside the kitsoki
//     checkout). Otherwise fall to the repo-wide override: return
//     <repo>/stories/<name>/app.yaml when $KITSOKI_REPO is set, erroring if
//     that story is missing there (an explicit override pointing at the
//     wrong tree must fail loudly, never silently fall back to the embedded
//     copy); return ("",nil) when neither override is set.
//   - override=false → materialize the embedded library and return
//     <root>/<name>/app.yaml, erroring if the embedded library lacks the story.
func buildImportResolver() app.ImportResolver {
	base := func(name, _ string, override bool) (string, error) {
		if override {
			if devPath := kitdev.Resolve(name); devPath != "" {
				candidate := filepath.Join(devPath, "app.yaml")
				if _, err := os.Stat(candidate); err != nil {
					return "", fmt.Errorf("kit dev override %s=%s: app.yaml not found (%s): %w",
						name, devPath, candidate, err)
				}
				return candidate, nil
			}
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
	// The selector reads $KITSOKI_KIT_STAGED at resolve time — same
	// env-not-captured-flag reasoning as $KITSOKI_REPO above.
	return kitstage.WrapResolver(base, func(name string) (bool, bool) {
		return kitstage.ParseSelector(os.Getenv(kitstage.EnvStaged))(name)
	})
}

// buildPlainImportResolver is buildImportResolver minus every per-kit
// override (kit-dev, staged): $KITSOKI_REPO then the embedded library. The
// trial engine passes it as a live-verification candidate so the LOCKED leg
// of a cross-version replay can find the accepted bytes even while a
// kit-dev override points the same name at the candidate checkout
// (kitstage.VerifiedTreeDir hash-checks every candidate, so resolver choice
// can never pin wrong content — only fail to find it).
func buildPlainImportResolver() app.ImportResolver {
	full := buildImportResolver()
	return func(name, importerDir string, override bool) (string, error) {
		if override {
			repo := os.Getenv(kitrepo.EnvVar)
			if repo == "" {
				return "", nil
			}
			candidate := filepath.Join(repo, "stories", name, "app.yaml")
			if _, err := os.Stat(candidate); err != nil {
				return "", nil // plain candidate absent; VerifiedTreeDir tries the next resolver
			}
			return candidate, nil
		}
		return full(name, importerDir, false)
	}
}
