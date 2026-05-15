package app

import "os"

// builtinMetaModes returns the meta_modes that ship with kitsoki and are
// available to every app without YAML declaration. An app declares a
// meta_mode with the same `group.verb` key to override one — the
// injection step only fills entries that aren't already present.
//
// Keys follow the `group.verb` convention: `story.{edit,ask,bug}` and
// `kitsoki.{edit,ask,bug}`. The pre-grouping single-token `bug` and
// `self` keys were removed without aliases. Triggers stay scoped to
// their group, so `story.bug` Trigger=`bug` and `kitsoki.bug`
// Trigger=`bug` coexist.
//
// Each group has exactly one mode flagged `Default: true` — the verb
// `/meta <group>` (no second token) resolves to. `edit` is the default
// for both builtin groups (matches the prior `/meta story` / `/meta
// self` habit).
//
// `kitsoki.edit` chats key against the synthetic `kitsoki-self` app_id
// at chat-resolve time so the conversation persists across apps. That
// special-case lives in internal/metamode/controller.go where the
// scope-key tuple is built.
//
// `story.*` keys per-app — every app has its own story.bug pile under
// `issues/bugs/`. No special-case needed.
//
// `kitsoki.*` is only injected when $KITSOKI_REPO is set, because the
// engineer / explainer agents need an unambiguous cwd. Without the env
// var, failing app loads everywhere (tests, CI, anyone running kitsoki
// on a release binary without dev workspace) would be a worse outcome
// than silently dropping the modes. Users who want them set the env
// var; apps that want them without the env var declare them explicitly.
// kitsoki.bug could in principle work via --target-dir, but for
// symmetry and discoverability we gate the whole group together.
func builtinMetaModes() map[string]*MetaModeDef {
	onpath := &MetaReturnDef{Intent: "onpath"}
	roTools := []string{"Read", "Glob", "Grep"}

	out := map[string]*MetaModeDef{
		"story.edit": {
			Group:   "story",
			Trigger: "edit",
			Default: true,
			Label:   "Edit story",
			Banner:  "Editing this story's YAML — your changes affect the running app.",
			Agent:   "story-author",
			Return:  onpath,
		},
		"story.ask": {
			Group:   "story",
			Trigger: "ask",
			Label:   "Ask about story",
			Banner:  "Asking about this story — read-only, no edits.",
			Agent:   "story-explainer",
			Tools:   roTools,
			Return:  onpath,
		},
		"story.bug": {
			Group:   "story",
			Trigger: "bug",
			Label:   "Story bug",
			Banner:  "Filing a story bug — write it down and the agent files it under issues/bugs/.",
			Agent:   "story-bug-reporter",
			Return:  onpath,
		},
	}
	if _, ok := os.LookupEnv("KITSOKI_REPO"); ok {
		out["kitsoki.edit"] = &MetaModeDef{
			Group:   "kitsoki",
			Trigger: "edit",
			Default: true,
			Label:   "Edit kitsoki",
			Banner:  "Editing kitsoki itself — your changes affect the engine, not the running story.",
			Agent:   "kitsoki-engineer",
			Cwd:     "${KITSOKI_REPO}",
			Return:  onpath,
		}
		out["kitsoki.ask"] = &MetaModeDef{
			Group:   "kitsoki",
			Trigger: "ask",
			Label:   "Ask about kitsoki",
			Banner:  "Asking about kitsoki source — read-only, no edits.",
			Agent:   "kitsoki-explainer",
			Cwd:     "${KITSOKI_REPO}",
			Tools:   roTools,
			Return:  onpath,
		}
		out["kitsoki.bug"] = &MetaModeDef{
			Group:   "kitsoki",
			Trigger: "bug",
			Label:   "Kitsoki bug",
			Banner:  "Filing a bug against kitsoki — write it down and the agent files it under issues/bugs/.",
			Agent:   "kitsoki-bug-reporter",
			Cwd:     "${KITSOKI_REPO}",
			Return:  onpath,
		}
	}
	return out
}

// injectBuiltinMetaModes adds any builtin meta mode whose name isn't
// already present in def.MetaModes. Called between merge and validate
// in both load paths so the validator sees the full effective set —
// trigger collisions between an app's mode and a builtin show up as
// regular validation errors rather than silent overrides.
//
// Apps override a builtin by declaring a meta_mode with the same
// `group.verb` key in their YAML (story-author-style); declaration wins
// over injection. The function is a no-op when def is nil.
func injectBuiltinMetaModes(def *AppDef) {
	if def == nil {
		return
	}
	if def.MetaModes == nil {
		def.MetaModes = make(map[string]*MetaModeDef, len(builtinMetaModes()))
	}
	for name, mode := range builtinMetaModes() {
		if _, exists := def.MetaModes[name]; exists {
			continue
		}
		def.MetaModes[name] = mode
	}
}
