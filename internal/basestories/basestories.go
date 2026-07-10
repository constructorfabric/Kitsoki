// Package basestories ships the kitsoki story library inside the binary and
// materializes it to an on-disk cache on first use, so a foreign repo that
// carries only a tiny instance (`import: { source: "@kitsoki/dev-story" }`)
// can run with the binary alone — no kitsoki source checkout present.
//
// # Why materialize-to-cache (not fs.FS plumbing)
//
// A base (imported) story is not just an app.yaml: at load time the loader
// reads child manifests, include: globs, agent system_prompt_path, and
// .star/.star.yaml sidecars; at runtime its rooms read prompts/*.md, JSON
// schemas, views/, and .star scripts — all resolved relative to the story's
// own directory on the OS filesystem (load time via baseDir; runtime via the
// KITSOKI_APP_DIR env). Rather than rewrite the ~20 os.ReadFile/os.Stat call
// sites onto an fs.FS, [Materialize] extracts the embedded library ONCE to a
// content-addressed cache dir and returns that on-disk path. Everything
// downstream then works unchanged on the real filesystem, KITSOKI_APP_DIR
// semantics intact. This mirrors Go's module cache (principle of least
// surprise).
//
// # Cache key — content hash, not binary version
//
// The cache dir is keyed by a SHA-256 over the embedded FS tree (every file's
// forward-slash path, its size, and its bytes, in stable lexical order). The
// key is therefore version-independent: rebuilding the binary with no story
// change re-uses the same cache; a story change yields a new key and a fresh
// extraction, leaving the old tree untouched (delete it by hand to reclaim
// space, the `go clean -modcache` idiom). Extraction is idempotent: a present
// cache dir is returned as-is.
//
// # Non-goals
//
//   - This package itself still does no remote/git fetch: it is embed +
//     local-override, never a fetcher, for the embedded story library
//     specifically (see stories/dev-story/README.md). That said, the
//     broader "kitsoki never fetches" posture this non-goal used to imply
//     no longer holds at the loader level: S2
//     (.context/kits-implementation-plan.md) added a 4th `resolveImportSource`
//     tier, `git+<url>@<ref>`, which DOES fetch over git into its own
//     content-addressed cache (internal/kitgit) — deliberately, so a kit can
//     be sourced from a real git remote rather than only the embedded
//     library or an on-disk checkout. This package's own embed→cache
//     mechanism is unchanged; only the universe of resolver tiers around it
//     grew a fetching one.
//   - No fs.FS plumbing through the loader/runtime: deliberately rejected in
//     favour of the cache (above).
//   - This package does NOT decide WHEN to fall back to the embedded library;
//     it only delivers the on-disk root. The override order (--kitsoki-repo ›
//     on-disk kitsoki root › embedded) lives in the loader's import resolver
//     (internal/app, cmd/kitsoki).
package basestories

import "errors"

// ErrNotStaged is returned by [Materialize] when the story library has not
// been staged into this binary — i.e. only the stories/.gitkeep placeholder
// is embedded. Run `make embed-stories` (or `go generate ./internal/basestories`)
// to copy the repo's stories/ into the embed dir before building.
var ErrNotStaged = errors.New("kitsoki story library not staged into this binary: run `make embed-stories` (copies stories/ into the embed dir) before building")
