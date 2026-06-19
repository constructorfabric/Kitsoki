// Package wire holds the production adapters that bind the mining loop's DI
// seams (mining.Reloader / mining.FlowGate) to the concrete engine. It lives in
// its own package because it imports the orchestrator and the flow runner;
// keeping it out of internal/mining lets that package stay free of those import
// graphs (and stay trivially testable with fakes). The orchestrator already
// satisfies mining.EventSink directly via its AppendMiningEvent method, so no
// adapter is needed for the sink.
package wire

import (
	"context"

	"kitsoki/internal/app"
	"kitsoki/internal/mining"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/testrunner"
)

// reloaderAdapter narrows *orchestrator.Orchestrator to mining.Reloader: the
// orchestrator's Reload/RerunOnEnter carry richer return values the mining gate
// does not need (the world is preserved across the swap regardless).
type reloaderAdapter struct{ orch *orchestrator.Orchestrator }

// Reloader returns the mining.Reloader backed by orch. It is the same Reload +
// RerunOnEnter path the TUI edit-mode drives.
func Reloader(orch *orchestrator.Orchestrator) mining.Reloader {
	return reloaderAdapter{orch: orch}
}

func (a reloaderAdapter) Reload(appPath string, prevState app.StatePath) error {
	_, err := a.orch.Reload(appPath, prevState)
	return err
}

func (a reloaderAdapter) RerunOnEnter(ctx context.Context, sid app.SessionID) error {
	_, err := a.orch.RerunOnEnter(ctx, sid)
	return err
}

// flowGate adapts testrunner.RunFlows to mining.FlowGate — the same no-LLM gate
// `kitsoki test flows` runs. A non-zero report.Failed is the red signal the
// apply gate reverts on.
type flowGate struct{ opts testrunner.FlowOptions }

// FlowGate returns the mining.FlowGate backed by testrunner.RunFlows. opts lets
// the caller pass FailFast/RecordingOverride etc.; the default zero value runs
// every matching fixture to completion.
func FlowGate(opts testrunner.FlowOptions) mining.FlowGate {
	return flowGate{opts: opts}
}

func (g flowGate) RunFlows(ctx context.Context, appPath, glob string) (int, error) {
	report, err := testrunner.RunFlows(ctx, appPath, glob, g.opts)
	if err != nil {
		return 0, err
	}
	return report.Failed, nil
}
