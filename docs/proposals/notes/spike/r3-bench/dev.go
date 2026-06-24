//go:build ignore
// Run with: go run ./docs/proposals/notes/spike/r3-bench/dev.go
// Separate file to avoid the main_test collision in the bench package.
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

func percentileD(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(d))
	copy(cp, d)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(float64(len(cp)-1) * p)
	return cp[idx]
}

func main() {
	home, _ := os.UserHomeDir()
	src := filepath.Join(home, ".local", "share", "kitsoki", "sessions.db")
	dir, _ := os.MkdirTemp("", "kitsoki-r3-dev-*")
	defer os.RemoveAll(dir)
	// Copy the dev DB so we can write without risk.
	dst := filepath.Join(dir, "sessions.db")
	if err := copyFile(src, dst); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	db, err := sql.Open("sqlite", dst)
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
		if _, err := db.Exec(p); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	// Ensure journal table exists.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS journal (
		session_id TEXT NOT NULL, turn INTEGER NOT NULL, seq INTEGER NOT NULL,
		ts INTEGER NOT NULL, kind TEXT NOT NULL, doc TEXT, doc_version INTEGER,
		body_json TEXT NOT NULL, PRIMARY KEY (session_id, turn, seq)) STRICT`); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	const n = 1000
	durs := make([]time.Duration, 0, n)
	now := time.Now().UnixMicro()
	sid := "r3-bench-dev"
	body := `{"ops":[{"op":"replace","path":"/vars/disturbance","value":1}]}`
	// Warm up
	for i := 0; i < 50; i++ {
		_, _ = db.Exec(`INSERT INTO journal VALUES (?,?,?,?,?,?,?,?)`,
			sid+"warm", int64(i), 0, now, "world.patch", "world", int64(i+1), body)
	}
	for i := 0; i < n; i++ {
		start := time.Now()
		tx, err := db.Begin()
		if err != nil {
			panic(err)
		}
		_, err = tx.Exec(`INSERT INTO journal (session_id, turn, seq, ts, kind, doc, doc_version, body_json) VALUES (?,?,?,?,?,?,?,?)`,
			sid, int64(i), 0, now, "world.patch", "world", int64(i+1), body)
		if err != nil {
			tx.Rollback()
			panic(err)
		}
		if err := tx.Commit(); err != nil {
			panic(err)
		}
		durs = append(durs, time.Since(start))
	}
	fmt.Printf("dev-db single-INSERT n=%d  p50=%v  p95=%v  p99=%v\n",
		len(durs), percentileD(durs, 0.5), percentileD(durs, 0.95), percentileD(durs, 0.99))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	buf := make([]byte, 1<<16)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if err.Error() == "EOF" {
				return nil
			}
			break
		}
	}
	return nil
}
