# Runtime: harness profiles & runtime selection

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [`dynamic-model-and-provider-control.md`](dynamic-model-and-provider-control.md)

## Why

Today's four oracle-selection axes (backend / provider / plugin / model)
are each frozen at startup and reachable only through flags, env, or
per-story YAML — never from a live session. The harness is built **once**
in `cmd/kitsoki/runtime.go` (`buildHarness`) and reused for the session
lifetime; the backend is installed per-dispatch from a single
session-global name (`internal/orchestrator/host_dispatch.go:76`,
`offpath.go:113`, set via `orchestrator.WithOracleBackendName`,
`internal/orchestrator/orchestrator.go:385`). Providers retarget the
**claude** subprocess only — their `env` is a no-op under codex/copilot
(`docs/architecture/oracle-providers.md` §intro). There is no
session-scoped, mutable "what should the *next* oracle call use" state for
a surface (`/provider`, web picker) to write.

This slice is the substrate: a **harness profile** (a named bundle of the
four axes), a **mutable per-session selection**, a **lazy next-turn
rebuild**, and the one gap-fix that makes "synthetic.new on codex" real —
applying provider `env` to whichever backend CLI is forked.

## What changes

A `harness_profiles:` block in `.kitsoki.yaml` declares named profiles.
Each session holds an **active selection** (`profile name` + optional
`model` override) behind a mutex. A new orchestrator/driver API
(`SetSelection`, `Selection`, `Profiles`) lets a surface read and swap it.
On the **next** dispatch the host resolves backend + env + model from the
active profile instead of the static session-global values — *every
interpretive call from now on uses the new selection; the one in flight
finishes on the old one.*

One sentence: **the backend/provider/model a session forks becomes a
mutable, profile-named selection resolved per-dispatch rather than a
static value fixed at startup.**

## Impact

- **Code seams:**
  - Config: `internal/webconfig/webconfig.go:40` (add `HarnessProfiles`,
    `DefaultProfile`).
  - Selection state + API: `internal/orchestrator/orchestrator.go:385`
    (replace the static backend-name with a locked `*selection`); a new
    `internal/harness/profile.go` for the profile type + resolution.
  - Backend resolution per-dispatch: `internal/orchestrator/host_dispatch.go:76`
    and `offpath.go:113` read the live selection instead of a fixed name.
  - Env-under-every-backend: the provider-env merge currently scoped to the
    claude subprocess (`internal/host/oracle_ask_with_mcp.go`, applied in
    `oracle_runner.go` around `resolveOracleBin`) moves so it merges onto
    the forked CLI's `cmd.Env` for **all** backends in
    `internal/host/oracle_runner.go:runClaudeStreamJSON`.
  - Trace: `internal/host/oracle_event_sink.go:70` (`OracleCalledPayload`)
    gains a `Profile string` sibling to the existing `Model`.
- **Vocabulary:** new config block (below); no new effects or host verbs —
  the change is in resolution, not story-author surface.
- **Stories affected:** none change behavior. With no `harness_profiles:`
  declared and no selection set, resolution falls through to today's
  static path (default profile = the `--oracle`/`--claude-model` flags).
- **Backward compat:** fully additive, default-off. Existing
  flags/env/`providers:`/`oracle_plugins:` keep working unchanged; a
  profile is just a *named* bundle of the same overrides applied through the
  same merge points.
- **Docs on ship:** new `docs/architecture/harness-profiles.md`; an
  env-under-all-backends note in `docs/architecture/oracle-providers.md`.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| config key | `harness_profiles:` | `map[name]Profile` in `.kitsoki.yaml` | operator-declared, machine-scoped |
| config key | `default_profile:` | `string` | new-session default; omitted ⇒ the flag-derived static profile |
| Profile field | `backend` | `claude\|copilot\|codex` | which CLI is forked (default `claude`) |
| Profile field | `plugin` | oracle plugin alias | optional; e.g. `builtin.local_llm` for llama.cpp |
| Profile field | `model` | `string` | default `--model` for this profile |
| Profile field | `models` | `[]string` | catalog `/model` lists; optional |
| Profile field | `env` | `map[string]string` | `${VAR}`-interpolated; merged onto the forked CLI subprocess |
| API | `Selection() / SetSelection(profile, model) / Profiles()` | orchestrator + session-driver | the read/write seam both surfaces use |

```yaml
# .kitsoki.yaml
default_profile: claude-native
harness_profiles:
  claude-native: { backend: claude }            # ambient Anthropic auth
  synthetic-claude:
    backend: claude
    env:
      ANTHROPIC_BASE_URL: https://api.synthetic.new/anthropic
      ANTHROPIC_AUTH_TOKEN: "${SYNTHETIC_API_KEY}"
    models: [hf:Qwen/Qwen2.5-Coder-32B-Instruct, hf:meta-llama/Llama-3.3-70B-Instruct]
    model: hf:Qwen/Qwen2.5-Coder-32B-Instruct
  synthetic-codex:                               # the env-under-codex gap-fix in action
    backend: codex
    env:
      OPENAI_BASE_URL: https://api.synthetic.new/openai
      OPENAI_API_KEY: "${SYNTHETIC_API_KEY}"
    models: [hf:Qwen/Qwen2.5-Coder-32B-Instruct]
  codex-native: { backend: codex }               # codex's own config/auth
  llama-local:
    plugin: builtin.local_llm
    model: h200/gpt-oss-120b
```

## The model

```
turn N:   surface ──SetSelection("synthetic-codex")──▶ session.selection (locked)
                                                              │ (no effect on the in-flight call)
turn N+1: dispatch ──reads selection──▶ resolve{ backend:codex, env:{OPENAI_*}, model } ──▶ fork codex
```

- **Interpretive vs deterministic:** unchanged. A profile only chooses
  *which* CLI/endpoint/model a `host.oracle.*` call forks — the
  decision-recording, schema validation, and replay contract are identical
  across profiles. The selection itself is deterministic config, recorded
  on every call (below).
- **Resolution precedence** (highest first), preserving today's mental
  model: per-effect `with:{model/provider}` › agent default › **active
  profile** › flag-derived static default. The profile slots in *below*
  story-author intent so a story that pins `model: opus` still gets opus
  regardless of the operator's profile (unless they raw-override).

## Decision recording

The selection is config, not an LLM decision, but it must be
reconstructable from the trace: every oracle call already stamps
`OracleCalledPayload.Model` (`internal/host/oracle_event_sink.go:70`). Add
a `Profile string` field set from the active selection at dispatch, so a
transcript line reads e.g. `oracle.decide · profile=synthetic-codex ·
model=hf:Qwen…`. No secret `env` values are ever recorded — only the
profile name, backend, and model. This is a *consumed* trace field, not a
new event; the [`agent-action-transcripts.md`] surface picks it up for free.

## Engine seams & invariants

- **Load-time validation** (in `webconfig` load): a profile's `backend` must
  be one of `claude|copilot|codex`; an unset `${VAR}` in `env` is a hard
  error (mirrors `providers:` at oracle-providers.md §1); a `plugin` must
  resolve in the registry; `default_profile` (if set) must name a declared
  profile. Fail fast with a clear message — never at first dispatch.
- **Selection write seam:** `SetSelection` validates the profile name and
  (if given) that the model is in the profile's `models` catalog when one is
  declared; rejects unknown names rather than silently falling back, so the
  surface can show an error.
- **Concurrency:** the selection is read on every dispatch and written from
  the surface goroutine — guard with a `sync.RWMutex`; resolution snapshots
  it once per call so a mid-resolution swap can't tear a single dispatch.

## Backward compatibility / migration

Existing stories, cassettes, flags, `providers:`, and `oracle_plugins:` are
untouched. With no `harness_profiles:` block the static path is preserved
byte-for-byte: the "default profile" is synthesized from
`--oracle`/`--claude-model`/env exactly as today. The env-under-all-backends
change is a no-op for any session that declares no profile `env` for a
non-claude backend (today nobody can, since providers were claude-only).

## Tasks

```
## 1. Config + profile type
- [ ] 1.1 `HarnessProfiles` + `DefaultProfile` in webconfig.go; load-time validation + errors
- [ ] 1.2 `internal/harness/profile.go`: Profile type + Resolve(selection) → {backend, env, model}

## 2. Mutable session selection
- [ ] 2.1 Locked selection on the orchestrator/session; `Selection/SetSelection/Profiles` API
- [ ] 2.2 host_dispatch.go + offpath.go read the live selection instead of the static backend name
- [ ] 2.3 Synthesize the default profile from flags when no profiles declared (compat)

## 3. Env under every backend + trace
- [ ] 3.1 Move provider/profile env merge onto the forked CLI's cmd.Env for all backends (oracle_runner.go)
- [ ] 3.2 Add Profile field to OracleCalledPayload; set at dispatch; never record env values

## 4. Verify + document
- [ ] 4.1 Stateless: `kitsoki turn` with two profiles selected between turns picks the right backend/env (cassette oracle)
- [ ] 4.2 Flow fixture: profile switch mid-flow; legacy no-profile path still passes
- [ ] 4.3 Write docs/architecture/harness-profiles.md; note env-under-all-backends in oracle-providers.md; trim this proposal
```

## Verification

No LLM. A cassette/in-process oracle proves the resolution without forking
a real CLI: assert that after `SetSelection("synthetic-codex")` the next
dispatch resolves `backend=codex` with `OPENAI_BASE_URL` on the subprocess
env, and that the in-flight call (queued before the switch) still ran on the
prior selection. A `kitsoki turn` probe between two selections plus an
intent-only flow fixture covers the next-turn semantics; the legacy
no-profile fixture must still pass unchanged. The env-merge change needs a
test asserting the forked **codex** `cmd.Env` carries the profile env (today
that path is claude-only) — verify it fails before the change.

## Open questions

1. **Per-session vs per-process selection** (epic Q1). *Lean: per-session*,
   stored on the `internal/runstatus/server` session entry; `default_profile`
   seeds new sessions.
2. **Raw-axis override storage.** A `/provider backend=codex` override that
   *isn't* a named profile — store as an ad-hoc anonymous profile in the
   selection, or reject non-profile axes? *Lean:* synthesize an anonymous
   profile from the base profile + overrides, labeled `(custom)` in the UI.
3. **Plugin-only profiles** (`llama-local`) bypass the backend CLI entirely
   (they answer via the oracle registry, `internal/oracle`). Confirm the
   selection resolution routes plugin profiles to the registry path
   (`FromHarness`/`Resolve`) rather than the backend fork. *Lean:* a profile
   with `plugin:` set selects the plugin and ignores `backend`.

## Non-goals

- Mid-flight cancellation (epic non-goal). Next-turn only.
- New backends/plugins; synthetic.new is reached via env retarget on an
  existing backend.
- Story-level profile declarations (global `.kitsoki.yaml` only in v1).
