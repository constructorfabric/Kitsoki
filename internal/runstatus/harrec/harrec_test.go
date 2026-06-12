package harrec

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestNewClampsCapacity(t *testing.T) {
	r := New(0)
	if _, cap := r.Depth(); cap != 1 {
		t.Fatalf("capacity=%d, want 1", cap)
	}
}

func TestSnapshotKeepsLastNInOrder(t *testing.T) {
	const capacity = 3
	r := New(capacity)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 10; i++ {
		r.Record(
			"POST",
			fmt.Sprintf("/rpc/%d", i),
			map[string]string{"Content-Type": "application/json"},
			[]byte(fmt.Sprintf("req-%d", i)),
			200,
			map[string]string{"Content-Type": "application/json"},
			[]byte(fmt.Sprintf("resp-%d", i)),
			base.Add(time.Duration(i)*time.Second),
			float64(i),
		)
	}

	count, cap := r.Depth()
	if cap != capacity {
		t.Fatalf("capacity=%d, want %d", cap, capacity)
	}
	if count != capacity {
		t.Fatalf("count=%d, want %d", count, capacity)
	}

	har := r.Snapshot()
	if har.Log.Creator.Name != CreatorName {
		t.Fatalf("creator=%q, want %q", har.Log.Creator.Name, CreatorName)
	}
	if har.Log.Version != "1.2" {
		t.Fatalf("version=%q, want 1.2", har.Log.Version)
	}
	if len(har.Log.Entries) != capacity {
		t.Fatalf("entries=%d, want %d", len(har.Log.Entries), capacity)
	}

	// Only the last N (7,8,9) survive, oldest->newest.
	wantURLs := []string{"/rpc/7", "/rpc/8", "/rpc/9"}
	for i, e := range har.Log.Entries {
		if e.Request.URL != wantURLs[i] {
			t.Fatalf("entry[%d].url=%q, want %q", i, e.Request.URL, wantURLs[i])
		}
		if e.Request.PostData == nil {
			t.Fatalf("entry[%d] missing postData", i)
		}
	}
}

func TestSnapshotIsCopy(t *testing.T) {
	r := New(2)
	r.Record("GET", "/rpc/a", nil, []byte("body"), 200, nil, []byte("out"), time.Now(), 1)

	snap := r.Snapshot()
	// Mutate the snapshot; subsequent snapshots must be unaffected.
	snap.Log.Entries[0].Request.URL = "MUTATED"
	snap.Log.Entries = nil

	snap2 := r.Snapshot()
	if len(snap2.Log.Entries) != 1 || snap2.Log.Entries[0].Request.URL != "/rpc/a" {
		t.Fatalf("snapshot shares mutable state: %+v", snap2.Log.Entries)
	}
}

func TestRecordCopiesInputs(t *testing.T) {
	r := New(1)
	body := []byte("orig")
	hdr := map[string]string{"X": "1"}
	r.Record("GET", "/rpc", hdr, body, 200, nil, nil, time.Now(), 0)

	body[0] = 'X'
	hdr["X"] = "mutated"

	snap := r.Snapshot()
	if got := snap.Log.Entries[0].Request.PostData.Text; got != "orig" {
		t.Fatalf("body not copied: %q", got)
	}
	for _, h := range snap.Log.Entries[0].Request.Headers {
		if h.Name == "X" && h.Value != "1" {
			t.Fatalf("header not copied: %q", h.Value)
		}
	}
}

func TestConcurrentRecordRaceFree(t *testing.T) {
	r := New(64)
	const goroutines = 16
	const perG = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			now := time.Now()
			for i := 0; i < perG; i++ {
				r.Record("POST", fmt.Sprintf("/rpc/%d-%d", g, i), nil, []byte("b"), 200, nil, []byte("r"), now, float64(i))
				if i%17 == 0 {
					_ = r.Snapshot()
					_, _ = r.Depth()
				}
			}
		}(g)
	}
	wg.Wait()

	count, cap := r.Depth()
	if cap != 64 {
		t.Fatalf("capacity=%d, want 64", cap)
	}
	if count != 64 {
		t.Fatalf("count=%d, want 64 (buffer should be full)", count)
	}
	if got := len(r.Snapshot().Log.Entries); got != 64 {
		t.Fatalf("entries=%d, want 64", got)
	}
}
