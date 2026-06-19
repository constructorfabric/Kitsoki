package host

import (
	"context"
	"encoding/json"
)

// SetAskStructuredForTest swaps the AskStructured implementation for tests
// and returns a restore function the test should defer.
func SetAskStructuredForTest(fn func(ctx context.Context, opts AskStructuredOptions) (json.RawMessage, error)) (restore func()) {
	prev := askStructuredFunc
	askStructuredFunc = fn
	return func() { askStructuredFunc = prev }
}

// ── oracle.task exports ────────────────────────────────────

// InferReplayModeExport is the test-visible wrapper for inferReplayMode.
func InferReplayModeExport(agent Agent, tools []string) ReplayMode {
	return inferReplayMode(agent, tools)
}

// CapReadToolOutputExport is the test-visible wrapper for capReadToolOutput.
func CapReadToolOutputExport(output string) string {
	return capReadToolOutput(output)
}

// CaptureInitialStateHashExport is the test-visible wrapper for captureInitialStateHash.
func CaptureInitialStateHashExport(ctx context.Context, workingDir string) string {
	return captureInitialStateHash(ctx, workingDir)
}

// CaptureFinalDiffExport is the test-visible wrapper for captureFinalDiff.
func CaptureFinalDiffExport(ctx context.Context, workingDir string) string {
	return captureFinalDiff(ctx, workingDir)
}

// CaptureFilesChangedExport is the test-visible wrapper for captureFilesChanged.
func CaptureFilesChangedExport(ctx context.Context, workingDir string) []string {
	return captureFilesChanged(ctx, workingDir)
}

// ObserveTaskToolCallsExport is the test-visible wrapper for observeTaskToolCalls.
func ObserveTaskToolCallsExport(ctx context.Context, cr ClaudeRun, parentTraceID string) []taskToolEvent {
	return observeTaskToolCalls(ctx, cr, parentTraceID)
}

// ExtractSessionIDExport is the test-visible wrapper for extractSessionID.
func ExtractSessionIDExport(ctx context.Context) string {
	return extractSessionID(ctx)
}

// KitsokiSessionIDFromCtxExport is the test-visible wrapper for
// kitsokiSessionIDFromCtx. Used by cmd/kitsoki tests to verify that
// injectSessionID stores the session ID in context rather than os.Setenv.
func KitsokiSessionIDFromCtxExport(ctx context.Context) string {
	return kitsokiSessionIDFromCtx(ctx)
}

// OracleStreamerRunExport is the test-visible wrapper for OracleStreamer.Run.
// Used by host_test to verify that the returned sessionID from system.init
// is captured for --resume across iterations (H5).
func OracleStreamerRunExport(ctx context.Context, bin string, cliArgs []string, stdin, workingDir string) (ClaudeRun, string, error) {
	return OracleStreamer{
		Bin:        bin,
		CLIArgs:    cliArgs,
		Stdin:      stdin,
		WorkingDir: workingDir,
	}.Run(ctx)
}

// EnvWithSessionIDExport is the test-visible wrapper for envWithSessionID.
// Used by host_test to verify that KITSOKI_SESSION_ID is injected per-subprocess.
func EnvWithSessionIDExport(env []string, sessionID string) []string {
	return envWithSessionID(env, sessionID)
}

// noopStreamSink is a StreamSink that discards all events. Used in tests that
// need to activate the stream-json path without caring about the events.
type noopStreamSink struct{}

func (noopStreamSink) OnStreamEvent(_ context.Context, _ StreamEvent) {}

// NoopStreamSinkExport returns a no-op StreamSink for use in tests.
func NoopStreamSinkExport() StreamSink { return noopStreamSink{} }

// TarballDirectoryExport is the test-visible wrapper for tarballDirectory.
func TarballDirectoryExport(dir string) ([]byte, error) {
	return tarballDirectory(dir)
}

// ── oracle_decide sandbox exports ─────────────────────────────────────────────

// ValidatorOptions is the test-visible surface of validatorOptions
// (used only for constructing RunDecideSandboxValidatorExport calls).
type ValidatorOptions struct {
	PostCmd     string
	PostCmdArgs []postCmdKV
}

// RunDecideSandboxValidatorExport exposes runDecideSandboxValidator for tests.
// Returns (rejection, contractErr) matching the internal signature.
func RunDecideSandboxValidatorExport(ctx context.Context, outputPath string, opts *ValidatorOptions) (rejection, contractErr string) {
	if opts == nil {
		return runDecideSandboxValidator(ctx, outputPath, nil)
	}
	internal := &validatorOptions{
		PostCmd:     opts.PostCmd,
		PostCmdArgs: opts.PostCmdArgs,
	}
	return runDecideSandboxValidator(ctx, outputPath, internal)
}

// IsSandboxContractViolationExport exposes isSandboxContractViolation for tests.
func IsSandboxContractViolationExport(vr ValidatorResult) bool {
	return isSandboxContractViolation(vr)
}

// RewriteToolsForBashMCPExport exposes rewriteToolsForBashMCP for tests.
func RewriteToolsForBashMCPExport(tools []string) []string {
	return rewriteToolsForBashMCP(tools)
}

// ApplyReadOnlyFloorCLIArgsExport exposes applyReadOnlyFloorCLIArgs for tests
// (the write_mode: read_only dispatch rewrite).
func ApplyReadOnlyFloorCLIArgsExport(cliArgs []string) []string {
	return applyReadOnlyFloorCLIArgs(cliArgs)
}

// ReadOnlyDeniedToolsExport exposes the read-only deny set for the write-mode
// floor assertion.
func ReadOnlyDeniedToolsExport() []string {
	return append([]string(nil), readOnlyDeniedTools...)
}
