// Benchmarks for the in-memory turn cache. The ~80 µs budget for a
// SQLite-backed Get (see docs/architecture/semantic-routing.md) sets the
// bar the in-memory layer must beat comfortably so the SQLite shim sits
// inside its own budget.
//
// Run:
//
//	go test -bench=. -benchmem ./internal/turncache/...
package turncache

import (
	"context"
	"testing"
	"time"
)

func benchKey(i int) Key {
	return Key{
		App:       "oregon-trail",
		AppHash:   "h1",
		StatePath: "@river_crossing.scouting",
		Signature: signature(i),
	}
}

// BenchmarkMemoryCache_Get_Hit measures the steady-state read path with
// a populated cache. The work is: ctx.Err() + mu.Lock + map lookup +
// value copy + mu.Unlock.
func BenchmarkMemoryCache_Get_Hit(b *testing.B) {
	c := NewMemory(testConfig())
	defer func() { _ = c.Close() }()
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

// BenchmarkMemoryCache_Put measures the write path. Same shape as Get
// but with an extra allocation for the row pointer.
func BenchmarkMemoryCache_Put(b *testing.B) {
	c := NewMemory(testConfig())
	defer func() { _ = c.Close() }()
	ctx := context.Background()
	v := CachedVerdict{Intent: "go"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.Put(ctx, benchKey(i), v)
	}
}
