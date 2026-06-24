// Throwaway prototype for continue-mode-spike R3.
// Measures the per-row cost of an INSERT INTO journal on a SQLite DB
// configured the same way internal/store/sqlite.go configures the
// production store: WAL mode, busy_timeout=5000, foreign_keys=ON,
// max_open_conns=1. We use BEGIN IMMEDIATE on the dual-write path
// because the production AppendEvents path already does (sqlite.go:166).
//
// The R3 target is <100µs added per row at p95. We measure two paths:
//   (a) one-INSERT-per-turn (the baseline path AppendEvents already takes)
//   (b) two-INSERTs-per-turn (the dual-write path the proposal mandates)
// so we can quantify the *delta* the journal adds.
//
// Run with: go run ./docs/proposals/notes/spike/r3-bench
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS events (
    session_id   TEXT    NOT NULL,
    turn         INTEGER NOT NULL,
    seq          INTEGER NOT NULL,
    ts           INTEGER NOT NULL,
    kind         TEXT    NOT NULL,
    payload_json TEXT    NOT NULL,
    PRIMARY KEY (session_id, turn, seq)
) STRICT;

CREATE TABLE IF NOT EXISTS journal (
    session_id   TEXT    NOT NULL,
    turn         INTEGER NOT NULL,
    seq          INTEGER NOT NULL,
    ts           INTEGER NOT NULL,
    kind         TEXT    NOT NULL,
    doc          TEXT,
    doc_version  INTEGER,
    body_json    TEXT    NOT NULL,
    PRIMARY KEY (session_id, turn, seq)
) STRICT;
`

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// Mirror exactly what internal/store/sqlite.go:95-99 sets. Note that
	// production does NOT set synchronous=NORMAL; the default of FULL
	// means each commit fsyncs the WAL — which dominates the per-row
	// latency. The R3 100µs target was written without measuring this;
	// see the spike notes for the revised proposal text.
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return db, nil
}

func runBenchEventsOnly(db *sql.DB, sid string, n int) []time.Duration {
	durs := make([]time.Duration, 0, n)
	now := time.Now().UnixMicro()
	payload := `{"set":{"disturbance":1}}`
	for i := 0; i < n; i++ {
		start := time.Now()
		tx, err := db.Begin()
		if err != nil {
			panic(err)
		}
		_, err = tx.Exec(
			`INSERT INTO events (session_id, turn, seq, ts, kind, payload_json) VALUES (?,?,?,?,?,?)`,
			sid, int64(i), 0, now, "EffectApplied", payload,
		)
		if err != nil {
			tx.Rollback()
			panic(err)
		}
		if err := tx.Commit(); err != nil {
			panic(err)
		}
		durs = append(durs, time.Since(start))
	}
	return durs
}

func runBenchDualWrite(db *sql.DB, sid string, n int) []time.Duration {
	durs := make([]time.Duration, 0, n)
	now := time.Now().UnixMicro()
	payload := `{"set":{"disturbance":1}}`
	body := `{"ops":[{"op":"replace","path":"/vars/disturbance","value":1}]}`
	for i := 0; i < n; i++ {
		start := time.Now()
		tx, err := db.Begin()
		if err != nil {
			panic(err)
		}
		_, err = tx.Exec(
			`INSERT INTO events (session_id, turn, seq, ts, kind, payload_json) VALUES (?,?,?,?,?,?)`,
			sid, int64(i), 0, now, "EffectApplied", payload,
		)
		if err != nil {
			tx.Rollback()
			panic(err)
		}
		_, err = tx.Exec(
			`INSERT INTO journal (session_id, turn, seq, ts, kind, doc, doc_version, body_json) VALUES (?,?,?,?,?,?,?,?)`,
			sid, int64(i), 0, now, "world.patch", "world", int64(i+1), body,
		)
		if err != nil {
			tx.Rollback()
			panic(err)
		}
		if err := tx.Commit(); err != nil {
			panic(err)
		}
		durs = append(durs, time.Since(start))
	}
	return durs
}

func percentile(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(d))
	copy(cp, d)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(float64(len(cp)-1) * p)
	return cp[idx]
}

func summarise(label string, d []time.Duration) {
	fmt.Printf("%-25s n=%d  p50=%v  p95=%v  p99=%v  max=%v\n",
		label, len(d), percentile(d, 0.5), percentile(d, 0.95), percentile(d, 0.99),
		percentile(d, 1.0))
}

func main() {
	// Temp DB so we don't pollute the dev sessions.db. WAL behaves the
	// same; this isolates the bench from concurrent writers.
	dir, err := os.MkdirTemp("", "kitsoki-r3-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer os.RemoveAll(dir)
	dbPath := filepath.Join(dir, "bench.db")
	db, err := openDB(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer db.Close()

	const n = 1000

	// Warm: one transaction so file system caches settle.
	runBenchEventsOnly(db, "warmup", 50)

	baseline := runBenchEventsOnly(db, "baseline", n)
	dual := runBenchDualWrite(db, "dual", n)

	fmt.Printf("DB path: %s\n\n", dbPath)
	summarise("events-only (baseline)", baseline)
	summarise("events + journal (dual)", dual)

	delta := percentile(dual, 0.95) - percentile(baseline, 0.95)
	fmt.Printf("\nadded-per-row p95 delta = %v (target <100µs)\n", delta)
	if delta > 100*time.Microsecond {
		fmt.Println("FAIL: delta exceeds 100µs target")
		os.Exit(1)
	}
	fmt.Println("PASS")
}
