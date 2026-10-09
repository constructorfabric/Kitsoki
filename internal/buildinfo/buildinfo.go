package buildinfo

// Version is the release version string for the running kitsoki engine.
//
// The cmd/kitsoki package keeps the historical main.version ldflags target for
// release compatibility and copies its value here at command construction time
// so internal reporting paths can stamp the same version.
var Version = "0.0.1-scaffold"

// Revision is the git SHA the binary was compiled from when build metadata was
// stamped in at link time. Empty means "unknown / not stamped".
var Revision = ""

// RevisionShort is the short git SHA corresponding to Revision. Empty means
// "unknown / not stamped".
var RevisionShort = ""
