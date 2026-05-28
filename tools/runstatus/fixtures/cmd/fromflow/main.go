// fromflow runs a single kitsoki flow fixture through the real orchestrator
// (with an in-memory event store) and emits a runstatus Snapshot JSON file
// from the resulting event log. Use it to produce a runstatus UI fixture
// whose event names and ordering match what the production engine actually
// emits — unlike hand-authored generator scripts.
//
//	go run ./tools/runstatus/fixtures/cmd/fromflow \
//	    --app  stories/bugfix/app.yaml \
//	    --flow stories/bugfix/flows/happy_human.yaml \
//	    -o     tools/runstatus/fixtures/bugfix-recycle.snapshot.json
//
// The flow's host stubs supply the side-effect responses; everything else
// (machine transitions, effects, state exits/enters, host dispatch events)
// is real.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
)

func main() {
	var (
		appPath  string
		flowPath string
		outPath  string
	)
	flag.StringVar(&appPath, "app", "", "path to app.yaml")
	flag.StringVar(&flowPath, "flow", "", "path to a single flow fixture YAML")
	flag.StringVar(&outPath, "o", "", "output snapshot.json path")
	flag.Parse()

	if appPath == "" || flowPath == "" || outPath == "" {
		fmt.Fprintln(os.Stderr, "all of --app --flow -o are required")
		os.Exit(2)
	}

	def, err := app.Load(appPath)
	if err != nil {
		die("load app", err)
	}

	var (
		captured     bool
		snapshotJSON []byte
		captureErr   error
	)

	opts := testrunner.FlowOptions{
		OnRigClose: func(_ string, st store.Store, sid app.SessionID) error {
			hist, err := st.LoadHistory(sid)
			if err != nil {
				captureErr = fmt.Errorf("load history: %w", err)
				return captureErr
			}
			snap, err := runstatus.FromHistory(hist, def, string(sid),
				runstatus.WithOracleJournal(st.DB()))
			if err != nil {
				captureErr = fmt.Errorf("build snapshot: %w", err)
				return captureErr
			}
			b, err := json.MarshalIndent(snap, "", "  ")
			if err != nil {
				captureErr = fmt.Errorf("marshal: %w", err)
				return captureErr
			}
			snapshotJSON = b
			captured = true
			return nil
		},
	}

	report, err := testrunner.RunFlows(context.Background(), appPath, flowPath, opts)
	if err != nil {
		die("run flow", err)
	}
	if captureErr != nil {
		die("capture", captureErr)
	}
	if !captured {
		die("capture", fmt.Errorf("flow %q did not run through the orchestrator path (legacy machine-only flows are unsupported)", flowPath))
	}

	if report.Failed > 0 {
		// We still emit a snapshot for inspection — the trace of a failing
		// flow is often exactly what the UI author wants to look at — but
		// we exit non-zero so callers (Makefiles, CI) can notice.
		fmt.Fprintf(os.Stderr, "warning: flow had %d failed turn(s); snapshot written anyway\n", report.Failed)
	}

	if err := os.WriteFile(outPath, snapshotJSON, 0o644); err != nil {
		die("write", err)
	}
	fmt.Fprintf(os.Stderr, "wrote snapshot to %s\n", outPath)

	if report.Failed > 0 {
		os.Exit(1)
	}
}

func die(what string, err error) {
	fmt.Fprintf(os.Stderr, "fromflow: %s: %v\n", what, err)
	os.Exit(1)
}
