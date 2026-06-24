//go:build ignore
// Measures the cost of one extra INSERT *inside* an already-open
// transaction — i.e. the incremental work the journal write adds when
// piggybacked on AppendEvents' existing transaction. The fsync cost
// is fixed per commit; this isolates the per-row CPU+I/O.
//
// Run with: go run ./docs/proposals/notes/spike/r3-bench/incremental.go
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

const schema = `
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

func main() {
	dir, _ := os.MkdirTemp("", "kitsoki-r3-inc-*")
	defer os.RemoveAll(dir)
	dbPath := filepath.Join(dir, "bench.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		db.Exec(p)
	}
	db.Exec(schema)

	// One big transaction with 1000 INSERTs; measure the per-INSERT
	// time. This is the realistic incremental cost of journal-vs-no-
	// journal because production AppendEvents wraps every event in
	// one tx already.
	const n = 1000
	tx, err := db.Begin()
	if err != nil {
		panic(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO journal (session_id, turn, seq, ts, kind, doc, doc_version, body_json) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		panic(err)
	}
	durs := make([]time.Duration, 0, n)
	now := time.Now().UnixMicro()
	body := `{"ops":[{"op":"replace","path":"/vars/disturbance","value":1}]}`
	for i := 0; i < n; i++ {
		start := time.Now()
		_, err := stmt.Exec("r3-inc", int64(i), 0, now, "world.patch", "world", int64(i+1), body)
		if err != nil {
			panic(err)
		}
		durs = append(durs, time.Since(start))
	}
	stmt.Close()
	tx.Commit()

	fmt.Printf("incremental INSERT inside open tx n=%d\n", n)
	fmt.Printf("  p50=%v  p95=%v  p99=%v  max=%v\n",
		percentile(durs, 0.5), percentile(durs, 0.95),
		percentile(durs, 0.99), percentile(durs, 1.0))
}
