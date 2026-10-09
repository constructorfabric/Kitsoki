package app

import (
	"os"

	"kitsoki/internal/agents"
	"kitsoki/internal/reportcontract"
)

// builtinMetaModes returns the meta_modes that ship with kitsoki and are
// available to every app without YAML declaration. An app declares a
// meta_mode with the same `group.verb` key to override one — the
// injection step only fills entries that aren't already present.
//
// Keys follow the `group.verb` convention: `story.{edit,ask,improve,bug}` and
// `kitsoki.{edit,ask,improve,bug}`. Triggers stay scoped to their group, so
// `story.bug` Trigger=`bug` and `kitsoki.bug` Trigger=`bug` coexist.
//
// The keys are deliberately grouped rather than flat: the TUI resolver
// (`resolveMetaName` → `metaVerbKey`) lets a bare `/meta <verb>` —
// `/meta bug`, `/meta ask`, `/meta edit` — resolve to the matching verb
// while PREFERRING the `story` group, so the one-token form targets the
// running story. A flat, un-namespaced builtin key (`bug`) could not do
// this safely: `validateMetaModes` forbids an un-namespaced meta trigger
// from colliding with a story's intent of the same name, and stories
// routinely declare `ask`/`bug` intents. Grouping is what keeps the verb
// and the intent namespaces independent. The legacy single-token `self`
// key was removed (use `/meta kitsoki edit`).
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
// engineer / explainer agents need an unambiguous cwd. Without it,
// failing app loads everywhere (tests, CI, anyone running kitsoki on a
// release binary without a dev workspace) would be a worse outcome than
// silently dropping the modes. The operator rarely needs to set the env
// var by hand, though: the root command resolves the repo via
// kitrepo.Resolve (which auto-detects a dev checkout and remembers it
// under ~/.kitsoki/repo) and exports $KITSOKI_REPO before app load, so
// after the first run from the source tree these modes light up
// everywhere. Apps that want them regardless declare them explicitly.
func builtinMetaModes() map[string]*MetaModeDef {
	onpath := &MetaReturnDef{Intent: "onpath"}
	roTools := reportcontract.ReadOnlyTools()
	bugTools := reportcontract.BugFilerTools()

	out := map[string]*MetaModeDef{
		"story.edit": {
			Group:   "story",
			Trigger: "edit",
			Default: true,
			Label:   "Edit story",
			Banner:  "Editing this story's YAML — your changes affect the running app.",
			Agent:   agents.NameStoryAuthor,
			Return:  onpath,
		},
		"story.ask": {
			Group:   "story",
			Trigger: "ask",
			Label:   "Ask about story",
			Banner:  "Asking about this story — read-only, no edits.",
			Agent:   agents.NameStoryExplainer,
			Tools:   roTools,
			Return:  onpath,
		},
		"story.improve": {
			Group:   "story",
			Trigger: "improve",
			Label:   "Improve run",
			Banner:  "Reviewing this run for story prompt, tool, and flow-test improvements — read-only.",
			Agent:   agents.NameStoryImprover,
			Tools:   roTools,
			Return:  onpath,
		},
		"story.bug": {
			Group:   "story",
			Trigger: "bug",
			Label:   "Story bug",
			Banner:  "Filing a story bug — write it down and the agent files it under issues/bugs/.",
			Agent:   agents.NameStoryBugReporter,
			Tools:   bugTools,
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
			Agent:   agents.NameKitsokiEngineer,
			Cwd:     "${KITSOKI_REPO}",
			Return:  onpath,
		}
		out["kitsoki.ask"] = &MetaModeDef{
			Group:   "kitsoki",
			Trigger: "ask",
			Label:   "Ask about kitsoki",
			Banner:  "Asking about kitsoki source — read-only, no edits.",
			Agent:   agents.NameKitsokiExplainer,
			Cwd:     "${KITSOKI_REPO}",
			Tools:   roTools,
			Return:  onpath,
		}
		out["kitsoki.improve"] = &MetaModeDef{
			Group:   "kitsoki",
			Trigger: "improve",
			Label:   "Improve kitsoki",
			Banner:  "Reviewing this run for reusable kitsoki engine, tool, and workflow improvements — read-only.",
			Agent:   agents.NameKitsokiImprover,
			Cwd:     "${KITSOKI_REPO}",
			Tools:   roTools,
			Return:  onpath,
		}
		out["kitsoki.bug"] = &MetaModeDef{
			Group:   "kitsoki",
			Trigger: "bug",
			Label:   "Kitsoki bug",
			Banner:  "Filing a bug against kitsoki — write it down and the agent files it under issues/bugs/.",
			Agent:   agents.NameKitsokiBugReporter,
			Cwd:     "${KITSOKI_REPO}",
			Tools:   bugTools,
			Return:  onpath,
		}
	}
	return out
}

// InjectBuiltinMetaModes is the exported entrypoint to injectBuiltinMetaModes,
// for callers outside the loader that need the builtin meta_modes on a
// synthetic AppDef the loader never produced — specifically `kitsoki web`'s
// home-screen "self" meta controller, which serves the cross-app `kitsoki.*`
// modes without a running story behind them. Same KITSOKI_REPO gating applies.
func InjectBuiltinMetaModes(def *AppDef) { injectBuiltinMetaModes(def) }

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
