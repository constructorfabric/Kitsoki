package bashprofile

// Kind names one of the three Bash restriction profiles an agent may declare.
// It exists as a shared int enum so the YAML loader and the runtime enforcer
// agree on a single ordering; see docs/architecture/hosts.md under "Bash
// profiles" for the user-facing shapes. The zero value is [ReadOnly], so an
// unset Kind fails closed.
type Kind int

const (
	// ReadOnly confines Bash to a built-in read-only command allowlist and
	// rejects shell metacharacters; it grants no writes. As Kind(0) it is also
	// the zero value, chosen deliberately so a missing profile is the safest
	// one rather than the most permissive.
	ReadOnly Kind = iota
	// Commands confines Bash to the explicit argv0 allowlist declared
	// alongside the profile in the app YAML; metacharacters are still
	// rejected.
	Commands
	// SandboxWrite allows any command but confines writes to a per-call
	// scratch directory and denies network access; it is the most permissive
	// profile and is never the zero value, so it must be opted into.
	SandboxWrite
)
