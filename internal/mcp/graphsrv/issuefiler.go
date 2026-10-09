package graphsrv

import "context"

// IssueRequest is what the IssueFiler seam receives for the
// --feedback-sink github case (plan §3.6 "Submit stage"): a composed
// GitHub issue, ready to file.
//
// Deliberately independent of internal/mcp/studio's identically-shaped
// IssueRequest/IssueResult/IssueFiler: graphsrv has no need to import
// studio (studio is the one importing graphsrv for the P6 mount, so the
// reverse import would be the wrong direction / a needless coupling for
// two packages that otherwise share nothing). Go's assignability rules
// require identical function types for a bare pass-through, and
// studio.IssueRequest/graphsrv.IssueRequest are distinct named types even
// though their fields match field-for-field — so cmd/kitsoki's production
// wiring adapts its existing studio.IssueFiler-shaped ghIssueFiler to this
// type with a small (~5 line) wrapper rather than sharing the Go type
// across packages.
type IssueRequest struct {
	Repo   string
	Root   string
	Title  string
	Body   string
	Labels []string
}

// IssueResult is what the IssueFiler seam returns: the created issue's URL
// and number.
type IssueResult struct {
	URL    string
	Number int
}

// IssueFiler is the injectable GitHub issue-creation seam feedback.report
// uses when --feedback-sink github (evaluated at flag/report-call time —
// see handleFeedbackReport's routeFeedbackToGithubSink). Nil means no
// GitHub sink is configured; feedback.report degrades to local-only with a
// routing_errors entry rather than failing — feedback.report is
// contractually always ok:true.
type IssueFiler func(ctx context.Context, req IssueRequest) (IssueResult, error)
