package studio

import (
	"context"
	"path/filepath"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// DiagnoseArgs declares the attachment that the caller intended to use. Empty
// expectations mean "do not compare that field". studio.diagnose is deliberately
// read-only and does not require an open objective.
type DiagnoseArgs struct {
	ExpectedCheckout   string `json:"expected_checkout,omitempty"`
	ExpectedWorkingDir string `json:"expected_working_dir,omitempty"`
	ExpectedExecutable string `json:"expected_executable,omitempty"`
	ExpectedProfile    string `json:"expected_profile,omitempty"`
	AttachedProfile    string `json:"attached_profile,omitempty"`
}

// DiagnoseFinding is one strictly evidence-backed attachment discrepancy.
// Evidence names the observed PingOK field; callers should not treat an absent
// finding as proof that an unreported expectation is correct.
type DiagnoseFinding struct {
	Class    string         `json:"class"`
	Summary  string         `json:"summary"`
	Evidence map[string]any `json:"evidence"`
	NextCall NextCallHint   `json:"next_call"`
}

// DiagnoseResult is the typed result for studio.diagnose.
type DiagnoseResult struct {
	OK       bool              `json:"ok"`
	Observed PingOK            `json:"observed"`
	Findings []DiagnoseFinding `json:"findings,omitempty"`
	NextCall NextCallHint      `json:"next_call"`
}

// registerDiagnoseExplainTools is intentionally not called by NewServer. The
// final operating-system integration owns public registration and profiles;
// keeping these handlers here makes their contract independently testable.
func (srv *Server) registerDiagnoseExplainTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "studio.diagnose",
		Description: "Read-only attachment diagnosis. Compares the current studio.ping identity with supplied expected checkout, cwd, executable, and profile.",
	}, srv.handleStudioDiagnose)
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.explain",
		Description: "Read-only bounded explanation of a live session trace. Classifies only evidenced route surprises, recorded errors followed by a transition, and unfinished stale turns.",
	}, srv.handleSessionExplain)
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "trace.explain",
		Description: "Read-only bounded explanation of an on-disk trace. Raw trace.read remains available for primary evidence.",
	}, srv.handleTraceExplain)
}

func (srv *Server) handleStudioDiagnose(_ context.Context, _ *mcpsdk.CallToolRequest, args DiagnoseArgs) (*mcpsdk.CallToolResult, any, error) {
	return nil, diagnoseAttachment(buildPingOK(), args), nil
}

func diagnoseAttachment(observed PingOK, args DiagnoseArgs) DiagnoseResult {
	result := DiagnoseResult{
		OK:       true,
		Observed: observed,
		NextCall: NextCallHint{Tool: "studio.ping", Reason: "Refresh the attachment identity before comparing it with a checkout or client profile."},
	}
	add := func(class, summary string, evidence map[string]any, next NextCallHint) {
		result.Findings = append(result.Findings, DiagnoseFinding{Class: class, Summary: summary, Evidence: evidence, NextCall: next})
	}
	if observed.Stale || (args.ExpectedCheckout != "" && observed.Checkout != "" && args.ExpectedCheckout != observed.Checkout) {
		add("stale_executable", "The attached executable identity does not match the expected checkout.", map[string]any{
			"expected_checkout": args.ExpectedCheckout, "observed_checkout": observed.Checkout,
			"build_revision": observed.Revision, "build_reported_stale": observed.Stale,
		}, NextCallHint{Tool: "studio.ping", Reason: "Restart or reconnect the Studio MCP server from the intended checkout, then verify its revision again."})
	}
	if args.ExpectedExecutable != "" && observed.Executable != "" && !samePath(args.ExpectedExecutable, observed.Executable) {
		add("stale_executable", "The process executable differs from the expected executable path.", map[string]any{
			"expected_executable": args.ExpectedExecutable, "observed_executable": observed.Executable,
		}, NextCallHint{Tool: "studio.ping", Reason: "Update the MCP client command to the expected Kitsoki executable and reconnect."})
	}
	if args.ExpectedWorkingDir != "" && observed.WorkingDir != "" && !samePath(args.ExpectedWorkingDir, observed.WorkingDir) {
		add("wrong_working_directory", "The attached server is running from a different working directory.", map[string]any{
			"expected_working_dir": args.ExpectedWorkingDir, "observed_working_dir": observed.WorkingDir,
		}, NextCallHint{Tool: "studio.ping", Reason: "Set the MCP client cwd to the intended checkout and reconnect before making changes."})
	}
	if args.ExpectedProfile != "" && args.AttachedProfile != "" && args.ExpectedProfile != args.AttachedProfile {
		add("attachment_profile_mismatch", "The reported client profile differs from the expected profile.", map[string]any{
			"expected_profile": args.ExpectedProfile, "attached_profile": args.AttachedProfile,
		}, NextCallHint{Tool: "studio.handles", Reason: "Inspect open handles and reopen the session with the intended replay or configured profile."})
	}
	return result
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
