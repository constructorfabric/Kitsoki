// Runnable godoc examples for the [bashprofile.Kind] enum. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/bashprofile/...`.
package bashprofile_test

import (
	"fmt"

	"kitsoki/internal/bashprofile"
)

// enforce models the runtime's per-profile branch: given a resolved Kind it
// reports which enforcement strategy the host would apply.
func enforce(profile bashprofile.Kind) string {
	switch profile {
	case bashprofile.ReadOnly:
		return "built-in read-only allowlist; no writes"
	case bashprofile.Commands:
		return "explicit argv0 allowlist"
	case bashprofile.SandboxWrite:
		return "scratch-dir writes; network denied"
	default:
		return "unknown"
	}
}

// ExampleKind_readOnly shows the read-only profile and the fail-closed zero
// value: an unset Kind is ReadOnly, the most restrictive strategy.
func ExampleKind_readOnly() {
	profile := bashprofile.ReadOnly
	var unset bashprofile.Kind // zero value

	fmt.Println(enforce(profile))
	fmt.Println(unset == bashprofile.ReadOnly)
	// Output:
	// built-in read-only allowlist; no writes
	// true
}

// ExampleKind_commands shows the explicit-allowlist profile the loader
// produces from `bash_profile: { commands: [...] }`.
func ExampleKind_commands() {
	profile := bashprofile.Commands
	fmt.Println(enforce(profile))
	// Output:
	// explicit argv0 allowlist
}

// ExampleKind_sandboxWrite shows the most permissive profile, which must be
// opted into because it is never the zero value.
func ExampleKind_sandboxWrite() {
	profile := bashprofile.SandboxWrite
	fmt.Println(enforce(profile))
	fmt.Println(profile == 0)
	// Output:
	// scratch-dir writes; network denied
	// false
}
