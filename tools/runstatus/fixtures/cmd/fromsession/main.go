// fromsession reads a recorded kitsoki session from the SQLite event log and
// emits a runstatus Snapshot JSON file. Use it to turn a real local session
// into a fixture for the run-status UI.
//
//	go run ./tools/runstatus/fixtures/cmd/fromsession \
//	    --db ~/.local/share/kitsoki/sessions.db \
//	    --session f911eedd-... \
//	    --app stories/bugfix/app.yaml \
//	    -o /tmp/bugfix.snapshot.json
//
// The produced JSON should not be committed if the underlying session
// contains internal project data; this tool is for local debugging and
// demoing.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/store"
)

func main() {
	var (
		dbPath    string
		sessionID string
		appPath   string
		outPath   string
	)
	flag.StringVar(&dbPath, "db", "", "path to kitsoki sessions.db")
	flag.StringVar(&sessionID, "session", "", "session id to export")
	flag.StringVar(&appPath, "app", "", "path to app.yaml")
	flag.StringVar(&outPath, "o", "", "output snapshot.json path")
	flag.Parse()

	if dbPath == "" || sessionID == "" || appPath == "" || outPath == "" {
		fmt.Fprintln(os.Stderr, "all of --db --session --app -o are required")
		os.Exit(2)
	}

	def, err := app.Load(appPath)
	if err != nil {
		die("load app", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		die("open store", err)
	}
	defer func() { _ = s.Close() }()

	hist, err := s.LoadHistory(app.SessionID(sessionID))
	if err != nil {
		die("load history", err)
	}
	if len(hist) == 0 {
		die("no events", fmt.Errorf("session %q has no events", sessionID))
	}

	snap, err := runstatus.FromHistory(hist, def, sessionID)
	if err != nil {
		die("build snapshot", err)
	}

	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		die("marshal", err)
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		die("write", err)
	}
	fmt.Fprintf(os.Stderr, "wrote %d events to %s\n", len(snap.Events), outPath)
}

func die(what string, err error) {
	fmt.Fprintf(os.Stderr, "fromsession: %s: %v\n", what, err)
	os.Exit(1)
}
