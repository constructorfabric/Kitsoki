package kitstage

import (
	"fmt"
	"path/filepath"
	"strings"

	"kitsoki/internal/app"
)

// EnvStaged selects staged-candidate resolution for this invocation.
//
//	""            staging off (the default — staged entries never affect
//	              normal runs)
//	"all" / "1"   every kit with a staged entry resolves to its candidate;
//	              kits without one fall through to normal resolution
//	"a,b"         only the named kits resolve staged — and naming a kit that
//	              has NO staged entry is a hard error (an explicit ask must
//	              fail loudly, never silently test the accepted bytes; same
//	              posture as a wrong --kitsoki-repo)
//
// The root command's persistent --staged flag sets this to "all", following
// the --kitsoki-repo → $KITSOKI_REPO pattern so subprocesses inherit it.
const EnvStaged = "KITSOKI_KIT_STAGED"

// Selector decides, per kit name, whether staged resolution applies.
// explicit reports whether the name was individually asked for (hard-error
// posture) as opposed to swept in by "all" (prefer-if-staged posture).
type Selector func(name string) (selected, explicit bool)

// SelectAll is the Selector behind `--staged` / KITSOKI_KIT_STAGED=all.
func SelectAll(string) (bool, bool) { return true, false }

// SelectNone disables staged resolution (the zero-config default).
func SelectNone(string) (bool, bool) { return false, false }

// SelectNames returns a Selector matching exactly the given kit names, each
// with explicit (fail-loudly) semantics.
func SelectNames(names ...string) Selector {
	set := map[string]bool{}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			set[n] = true
		}
	}
	return func(name string) (bool, bool) {
		return set[name], set[name]
	}
}

// ParseSelector maps a KITSOKI_KIT_STAGED value to a Selector.
func ParseSelector(v string) Selector {
	switch v = strings.TrimSpace(v); v {
	case "":
		return SelectNone
	case "all", "1", "true":
		return SelectAll
	default:
		return SelectNames(strings.Split(v, ",")...)
	}
}

// WrapResolver layers staged-candidate resolution in front of base. For an
// override-tier call (`@kitsoki/<name>` imports; see app.resolveImportSource)
// whose name the selector picks, it walks up from the importing manifest's
// directory to the nearest `.kitsoki/kits.staged.lock` and resolves the
// staged candidate's pinned tree instead of the accepted source. Everything
// else falls through to base, so precedence is:
//
//	staged (when selected) > kit dev override > $KITSOKI_REPO > discovery > embedded
//
// Staged must beat the dev overrides: a trial explicitly asks to test the
// staged bytes, and being shadowed by a lingering `kit dev` override would
// silently test the wrong tree.
//
// Git-tier import sources (`git+<url>@<ref>`) never reach an ImportResolver
// (they are resolved a tier earlier by shape); staging git-sourced kits is
// therefore surfaced through the kit-level commands (`kit trial` resolves
// the staged tree directly), not through this hook.
func WrapResolver(base app.ImportResolver, selected Selector) app.ImportResolver {
	if selected == nil {
		selected = SelectNone
	}
	return func(name, importerDir string, override bool) (string, error) {
		if override {
			if sel, explicit := selected(name); sel {
				path, err := resolveStaged(name, importerDir, explicit)
				if err != nil {
					return "", err
				}
				if path != "" {
					return path, nil
				}
			}
		}
		if base == nil {
			return "", nil
		}
		return base(name, importerDir, override)
	}
}

// resolveStaged finds the staged candidate for name relative to importerDir.
// It returns "" (no error) when nothing is staged and the selection was not
// explicit; an explicit selection with nothing staged is a hard error.
func resolveStaged(name, importerDir string, explicit bool) (string, error) {
	root, ok := FindProjectRoot(importerDir)
	if !ok {
		if explicit {
			return "", fmt.Errorf("%s=%s: no kits.staged.lock found above %s — run `kitsoki kit update %s` first", EnvStaged, name, importerDir, name)
		}
		return "", nil
	}
	f, err := Load(Path(root))
	if err != nil {
		return "", err
	}
	entry := f.Kits[name]
	if entry == nil {
		if explicit {
			return "", fmt.Errorf("%s=%s: kit %q has no staged candidate in %s — run `kitsoki kit update %s` first", EnvStaged, name, name, Path(root), name)
		}
		return "", nil
	}
	dir, err := ResolveTree(entry)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(dir, "app.yaml")
	return candidate, nil
}
