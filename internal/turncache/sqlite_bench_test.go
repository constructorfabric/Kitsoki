// Benchmarks for the SQLite turn cache. The routing architecture commits
// to ~80 µs/op for the SQLite-backed Get path (see the turn-cache section
// of docs/architecture/semantic-routing.md); the benchmarks here let us
// confirm that against actual hardware before claiming the budget.
//
// Run:
//
//	go test -bench=BenchmarkSQLite -benchmem ./internal/turncache/...
//
// Use -benchtime=1x in CI for compile-only confirmation, longer durations
// for steady-state numbers.
package turncache

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// benchSQLiteCache opens a fresh on-disk SQLite cache under b.TempDir
// and registers Close in b.Cleanup so the benchmark doesn't leak files.
func benchSQLiteCache(b *testing.B) Cache {
	b.Helper()
	path := filepath.Join(b.TempDir(), "cache.db")
	c, err := NewSQLite(path, testConfig())
	if err != nil {
		b.Fatalf("NewSQLite: %v", err)
	}
	b.Cleanup(func() { _ = c.Close() })
	return c
}

// BenchmarkSQLite_Get_Hit measures the steady-state read path against
// a populated cache (1024 keys; benchmark reads round-robin).
func BenchmarkSQLite_Get_Hit(b *testing.B) {
	c := benchSQLiteCache(b)
	ctx := context.Background()
	const n = 1024
	for i := 0; i < n; i++ {
		_ = c.Put(ctx, benchKey(i), CachedVerdict{Intent: "go", CreatedAt: time.Now()})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = c.Get(ctx, benchKey(i%n))
	}
}

// BenchmarkSQLite_Put measures the write path with INSERT OR REPLACE.
// The cache replaces the same key, so the underlying SQLite operation
// is closer to an UPDATE than a fresh INSERT — appropriate for the
// turn-cache workload where the same signature is re-Put on each LLM
// re-verdict.
func BenchmarkSQLite_Put(b *testing.B) {
	c := benchSQLiteCache(b)
	ctx := context.Background()
	v := CachedVerdict{Intent: "go"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.Put(ctx, benchKey(i), v)
	}
}

// BenchmarkSQLite_RecordHit measures the hit-counter UPDATE path. The
// benchmark targets a single hot row so every iteration does the same
// UPDATE with the same SQLITE_BUSY-prone WAL fsync timing.
func BenchmarkSQLite_RecordHit(b *testing.B) {
	c := benchSQLiteCache(b)
	ctx := context.Background()
	k := benchKey(0)
	if err := c.Put(ctx, k, CachedVerdict{Intent: "go"}); err != nil {
		b.Fatalf("seed Put: %v", err)
	}
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.RecordHit(ctx, k, now)
	}
}
