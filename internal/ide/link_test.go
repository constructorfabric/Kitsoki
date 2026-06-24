package ide

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLink_ConnectAndStatus(t *testing.T) {
	s := newStubServer(t)
	cwd := "/home/u/code/proj"
	s.writeLock(cwd)

	l := NewLink(cwd, s.discoverer(nil))
	info, err := l.Connect(shortCtx(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if !info.Connected || info.Port != s.port || info.IDEName != "Stub Code" {
		t.Fatalf("bad LinkInfo: %+v", info)
	}
	if info.Workspace != cwd {
		t.Fatalf("workspace = %q want %q", info.Workspace, cwd)
	}
	if !l.Connected() || l.Port() != s.port || l.Workspace() != cwd {
		t.Fatalf("mirror methods disagree with status: %+v", l.Status())
	}

	// Second Connect is a no-op returning current info.
	info2, err := l.Connect(shortCtx(t))
	if err != nil || info2 != info {
		t.Fatalf("re-Connect should be a no-op: %+v %v", info2, err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if l.Connected() {
		t.Fatal("Connected() should be false after Close")
	}
}

func TestLink_ConnectNoIDE(t *testing.T) {
	s := newStubServer(t)
	// No lock written, so discovery finds nothing.
	l := NewLink("/home/u/code/proj", s.discoverer(nil))
	_, err := l.Connect(shortCtx(t))
	if !errors.Is(err, ErrNoIDE) {
		t.Fatalf("want ErrNoIDE, got %v", err)
	}
}

func TestLink_CallToolNotConnected(t *testing.T) {
	l := NewLink("/w", &Discoverer{LockDir: t.TempDir(), Environ: func(string) string { return "" }})
	if _, err := l.CallTool(shortCtx(t), "getDiagnostics", nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
}

func TestLink_CallToolRoundTrip(t *testing.T) {
	s := newStubServer(t)
	cwd := "/home/u/code/proj"
	s.writeLock(cwd)
	l := NewLink(cwd, s.discoverer(nil))
	if _, err := l.Connect(shortCtx(t)); err != nil {
		t.Fatal(err)
	}
	raw, err := l.CallTool(shortCtx(t), "getOpenEditors", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("empty result")
	}
}

func TestLink_SingleFlightReconnectPicksFreshestLock(t *testing.T) {
	// First stub connects fine, then drops. A SECOND stub is the freshest lock
	// discovery should pick on reconnect; the first lock is overwritten so the
	// same <port>.lock filename now points at the live second server.
	first := newStubServer(t)
	// The first editor drops its socket right after the first tools/call, so a
	// follow-up CallTool deterministically sees the drop and reconnects.
	first.dropAfter = 1
	cwd := "/home/u/code/proj"
	first.writeLock(cwd)

	l := NewLink(cwd, first.discoverer(nil))
	if _, err := l.Connect(shortCtx(t)); err != nil {
		t.Fatalf("initial connect: %v", err)
	}

	// Stand up the replacement editor and rewrite the lock dir to point at it.
	second := newStubServer(t)
	// Point discovery at the SAME lockDir the link was built with, but with a
	// lock whose port is the second server's. Remove the stale first lock.
	if err := removeLock(first); err != nil {
		t.Fatal(err)
	}
	// Write the second server's lock INTO the first stub's lockDir (the dir the
	// link's discoverer scans), so the freshest lock now points at the second.
	writeLockInto(t, first.lockDir, second, cwd)

	// Trigger the drop: this first call succeeds, then the first server closes
	// the socket. The pump observes it; the next CallTool reconnects.
	if _, err := l.CallTool(shortCtx(t), "getOpenEditors", map[string]any{}); err != nil {
		t.Fatalf("call before drop: %v", err)
	}

	// Fire several concurrent calls; they must all succeed via ONE reconnect to
	// the second server, never N parallel redials.
	deadline := time.Now().Add(5 * time.Second)
	var success bool
	for time.Now().Before(deadline) && !success {
		var wg sync.WaitGroup
		errs := make([]error, 5)
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = l.CallTool(shortCtx(t), "getOpenEditors", map[string]any{})
			}(i)
		}
		wg.Wait()
		success = true
		for _, e := range errs {
			if e != nil {
				success = false
			}
		}
	}
	if !success {
		t.Fatal("reconnect to freshest lock did not succeed")
	}
	// The link should now report the second server's port.
	if l.Port() != second.port {
		t.Fatalf("after reconnect, port = %d want freshest %d", l.Port(), second.port)
	}
}

// TestLink_ReconnectDoesNotResurrectClosedLink guards the single-flight
// reconnect guard: a CallTool that observed a drop and entered reconnect must
// NOT redial when the owner has concurrently Close()d the Link. Close sets
// l.client = nil; reconnect must see current != dropped (current is nil) and
// return without dialing a fresh Client into a deliberately torn-down Link —
// otherwise the new Client's read-pump goroutine + socket leak and the link
// silently comes back to life.
//
// This is a logic-race the detector can't catch (no unsynchronized access), so
// we drive the exact interleaving deterministically: connect, snapshot the
// live client as the "dropped" one the caller observed, Close the link, then
// invoke reconnect with that client. Discovery still points at a LIVE lock, so
// the pre-fix code (guard `current != dropped && current != nil`) WOULD dial a
// new orphan; the fixed guard (`current != dropped`) returns nil.
func TestLink_ReconnectDoesNotResurrectClosedLink(t *testing.T) {
	s := newStubServer(t)
	cwd := "/home/u/code/proj"
	s.writeLock(cwd)

	l := NewLink(cwd, s.discoverer(nil))
	if _, err := l.Connect(shortCtx(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// The client the in-flight CallTool observed before it saw the drop.
	l.mu.Lock()
	dropped := l.client
	l.mu.Unlock()
	if dropped == nil {
		t.Fatal("expected a live client after Connect")
	}

	// The owner Close()s the link concurrently (sets l.client = nil).
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// reconnect must NOT resurrect: it returns nil (no error) and leaves the
	// link torn down. The lock dir still holds a LIVE lock, so a buggy guard
	// would dial and store a fresh client here.
	if err := l.reconnect(shortCtx(t), dropped); err != nil {
		t.Fatalf("reconnect after Close should be a no-op, got %v", err)
	}
	l.mu.Lock()
	resurrected := l.client
	l.mu.Unlock()
	if resurrected != nil {
		t.Fatal("reconnect resurrected a Closed link — new client leaked")
	}
	if l.Connected() {
		t.Fatal("Connected() must stay false after reconnect on a Closed link")
	}
}

func TestLink_CandidatesWithoutConnecting(t *testing.T) {
	s := newStubServer(t)
	cwd := "/home/u/code/proj"
	s.writeLock(cwd)
	l := NewLink(cwd, s.discoverer(nil))
	cands, err := l.Candidates(shortCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Port != s.port {
		t.Fatalf("candidates = %+v", cands)
	}
	if l.Connected() {
		t.Fatal("Candidates must not connect")
	}
}
