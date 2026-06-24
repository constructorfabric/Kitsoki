// Package bashprofile defines the three Bash restriction profiles used by
// host.agent.ask and host.agent.decide: it sits between internal/app (the
// YAML loader that parses a `bash_profile:` block into a [Kind]) and
// internal/host (the runtime that enforces the chosen profile when an agent
// invokes the Bash tool).
//
// The enum lives in its own leaf package so that the loader and the runtime
// reference one definition. If each declared its own iota sequence, a reorder
// on one side would silently remap the other side's stored values — a
// read-only profile becoming a sandboxed-write profile is exactly the kind of
// privilege drift this package exists to prevent.
//
// # Algorithm
//
// There is no algorithm at this layer — the package is the shared vocabulary,
// not the enforcement. A [Kind] is produced once by internal/app while parsing
// the app YAML and is consumed later by internal/host, which selects an
// enforcement strategy per value:
//
//   - [ReadOnly] — the runtime restricts Bash to a built-in read-only command
//     allowlist (grep, find, cat, git, jq, ...) and rejects shell
//     metacharacters; no profile-specific configuration is carried.
//   - [Commands] — the runtime restricts Bash to the explicit argv0 allowlist
//     the app declared alongside the profile; metacharacters are still
//     rejected.
//   - [SandboxWrite] — the runtime allows any command but confines writes to a
//     per-call scratch directory and denies network access.
//
// Each profile is enforced at the moment a Bash invocation is dispatched, not
// at load time; load time only resolves the YAML shape to one of these values.
//
// # Contracts
//
// The zero value Kind(0) is [ReadOnly] — the most restrictive profile — so a
// forgotten or zeroed field fails closed rather than granting writes or an
// open command set. Code that wants to distinguish "explicitly read-only" from
// "unset" must track that separately; this enum cannot.
//
// Kind is an immutable int constant. It is safe for concurrent use without
// synchronisation: every value is created during single-threaded app load and
// only read thereafter. There are no methods and no nil receiver to consider.
//
// No function in this package returns an error. The set of valid profiles is
// closed at compile time by the constants below; validation of an unknown
// YAML profile name is the loader's responsibility (internal/app), which maps
// only the three recognised shapes to these constants.
//
// # Worked example
//
// The loader maps each YAML `bash_profile:` shape to one Kind:
//
//	bash_profile: read-only                 -> bashprofile.ReadOnly     (0)
//	bash_profile: { commands: [git, jq] }   -> bashprofile.Commands     (1)
//	bash_profile: { sandboxed-write: /tmp } -> bashprofile.SandboxWrite (2)
//
// The runtime then branches on the resolved Kind to pick the enforcement
// strategy:
//
//	switch profile {
//	case bashprofile.ReadOnly:     // built-in read allowlist
//	case bashprofile.Commands:     // explicit argv0 allowlist
//	case bashprofile.SandboxWrite: // scratch-dir writes, no network
//	}
//
// Runnable forms of these mappings live in [ExampleKind_readOnly],
// [ExampleKind_commands], and [ExampleKind_sandboxWrite].
//
// # Non-goals
//
//   - No enforcement. This package names the profiles; internal/host applies
//     them. Keeping enforcement out keeps the enum importable by the loader
//     without dragging in the runtime's sandbox machinery.
//   - No full shell AST parsing. Profiles gate on argv0 and a metacharacter
//     reject, not on a parsed command tree — the design favours a small,
//     auditable check over emulating a shell.
//   - No perfect network isolation. SandboxWrite denies network through
//     environment-level proxy denial, not a kernel namespace; it raises the
//     bar, it is not a security boundary against a determined process.
//   - No YAML parsing or validation. The loader owns the mapping from YAML
//     shape to Kind so that this package stays a dependency-free leaf.
//
// # Reference
//
// The user-facing profile reference — the YAML shapes, the read-only
// allowlist, and the metacharacter rules — is docs/architecture/hosts.md
// under "Bash profiles".
package bashprofile
