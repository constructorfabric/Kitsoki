package kitstage

import (
	"fmt"
	"os"
	"path/filepath"

	"kitsoki/internal/app"
	"kitsoki/internal/kitgit"
	"kitsoki/internal/kitlock"
)

// PinnedResolver resolves the given kit names to fixed on-disk trees at the
// override tier, falling through to base for everything else. This is how
// `kit trial` pins BOTH legs of a cross-version replay: leg A pins the
// accepted lockfile trees (AcceptedTree), leg B the staged candidates
// (ResolveTree). Normal resolution of `@kitsoki/<name>` is live (a checkout,
// the embedded library) — for a trial that liveness is exactly wrong, since
// the source directory may already contain the NEXT version while the lock
// still pins the previous one.
func PinnedResolver(base app.ImportResolver, pins map[string]string) app.ImportResolver {
	return func(name, importerDir string, override bool) (string, error) {
		if override {
			if dir, ok := pins[name]; ok {
				candidate := filepath.Join(dir, "app.yaml")
				if _, err := os.Stat(candidate); err != nil {
					return "", fmt.Errorf("pinned kit %s: app.yaml not found (%s): %w", name, candidate, err)
				}
				return candidate, nil
			}
		}
		if base == nil {
			return "", nil
		}
		return base(name, importerDir, override)
	}
}

// AcceptedTree finds an accepted lockfile entry's pinned bytes in the
// content-addressed caches (commit cache for the git tier, tree cache for
// everything else). ok is false when the resolution predates the caches or
// was evicted — callers degrade (skip the pinned leg with a note) rather
// than fall back to live resolution, which could silently pin the wrong
// version.
func AcceptedTree(e *kitlock.Entry) (dir string, ok bool) {
	if e == nil {
		return "", false
	}
	if e.Commit != "" {
		res, cached, err := kitgit.CachedResult(e.Commit)
		if err != nil || !cached {
			return "", false
		}
		return res.Root, true
	}
	if e.TreeHash != "" {
		root, cached, err := kitgit.CachedTree(e.TreeHash)
		if err != nil || !cached {
			return "", false
		}
		return root, true
	}
	return "", false
}

// VerifiedTreeDir looks for a LIVE directory holding exactly the pinned
// bytes: it resolves source through each candidate resolver in turn and
// returns the first resolved directory whose content hash matches wantHash.
// Live-first resolution matters for kits that import sibling stories by
// relative path (dev-story's Phase A layout imports ../bugfix and friends):
// the content-addressed tree cache snapshots only the kit directory, so a
// cache-resolved tree cannot load them — but a hash-verified live checkout
// can, with identical content guarantees. Callers fall back to the cache
// when no live candidate matches (source moved on since staging).
//
// Git-tier pins (a recorded commit) never need this: the commit cache
// materializes the whole repository, so relative imports resolve inside it.
func VerifiedTreeDir(source, importerDir, wantHash string, resolvers []app.ImportResolver) (string, bool) {
	if wantHash == "" {
		return "", false
	}
	for _, r := range resolvers {
		appPath, err := app.ResolveSource(source, importerDir, r)
		if err != nil {
			continue
		}
		dir := filepath.Dir(appPath)
		hash, err := kitgit.DirTreeHash(dir)
		if err != nil || hash != wantHash {
			continue
		}
		return dir, true
	}
	return "", false
}
