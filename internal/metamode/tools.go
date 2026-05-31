package metamode

import "strings"

// NormaliseToolName maps a YAML-author-friendly tool name into the
// fully-qualified form the host registry uses.
//
// Inputs may be either short ("authoring.propose") or already qualified
// ("host.authoring.propose"). The function is idempotent and a no-op
// on already-qualified names.
//
// Rules:
//
//   - empty string → empty string (caller's problem)
//   - name already starts with "host." → returned unchanged
//   - anything else → "host." + name
//
// Used by Controller.Send when projecting MetaModeDef.Tools (which the
// loader accepts in either form) into the
// OracleCaller's tool allowlist. Keeps the loader free of host-package
// knowledge and the YAML surface forgiving.
//
// The list this produces is informational today — every claude
// subprocess runs with --permission-mode bypassPermissions, so the
// allowlist documents intent for prompts and human reviewers rather
// than acting as a runtime gate. See docs/stories/meta-mode.md.
func NormaliseToolName(name string) string {
	if name == "" {
		return name
	}
	if strings.HasPrefix(name, "host.") {
		return name
	}
	return "host." + name
}

// normaliseToolNames maps NormaliseToolName over a slice, returning a
// new slice (the input is not mutated).
func normaliseToolNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = NormaliseToolName(n)
	}
	return out
}
