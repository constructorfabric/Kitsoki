package graphsrv

import "fmt"

// Server modes gate write-tool registration (plan §3.2): the read family,
// graph.changeset, and the feedback channel exist in every mode; the P4
// write family (graph.propose/withdraw/apply/authorize) is registered only
// in propose and steward mode (never read), and graph.apply's real
// (non-dry-run) path plus graph.authorize additionally require steward mode
// at runtime even when registered.
const (
	ModeRead    = "read"
	ModePropose = "propose"
	ModeSteward = "steward"
)

// DefaultMode is used when --mode is omitted.
const DefaultMode = ModePropose

// ValidateMode checks a --mode flag value against the allow-list.
func ValidateMode(mode string) error {
	switch mode {
	case ModeRead, ModePropose, ModeSteward:
		return nil
	default:
		return fmt.Errorf("mcp-graph: --mode must be one of %s, %s, %s (got %q)", ModeRead, ModePropose, ModeSteward, mode)
	}
}

// FeedbackSink identifies where feedback.report attempts to route a report.
// "local" is always on regardless of this value (plan §3.6). "catalog"
// proposes a changeset per the catalog's feedback_routing block (P6);
// "github" files an issue via the injected IssueFiler (P6). Both degrade to
// local-only with a routing_errors entry when unconfigured/blocked — the
// sink is evaluated at feedback.report call ("flag") time, not at
// authorize/apply time.
const (
	FeedbackSinkLocal   = "local"
	FeedbackSinkCatalog = "catalog"
	FeedbackSinkGithub  = "github"
)

// ValidateFeedbackSink checks a --feedback-sink flag value against the
// allow-list.
func ValidateFeedbackSink(sink string) error {
	switch sink {
	case FeedbackSinkLocal, FeedbackSinkCatalog, FeedbackSinkGithub:
		return nil
	default:
		return fmt.Errorf("mcp-graph: --feedback-sink must be one of %s, %s, %s (got %q)", FeedbackSinkLocal, FeedbackSinkCatalog, FeedbackSinkGithub, sink)
	}
}
