package ide

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// ErrNoIDE is returned by Connect when discovery finds no usable lock matching
// the working directory. The TUI distinguishes it from a dial failure to say
// "no editor found" rather than "failed to connect".
var ErrNoIDE = errors.New("ide: no editor found")

// LinkInfo is the connected-state summary returned by Connect and Status.
type LinkInfo struct {
	Connected bool
	IDEName   string // lock.IDEName, "" when disconnected
	Workspace string // the matched workspace folder (longest-prefix winner)
	Port      int
}

// Link is the process-lifetime singleton holding at most one [Client]. It is
// the handle the TUI starts/stops and the value host.ide.* handlers resolve
// from ctx. Methods are safe for concurrent use.
//
// Link satisfies the host.IDELink interface structurally — internal/ide does
// not import internal/host (that would invert the dependency), so there is no
// compile-time assertion here; the orchestrator wiring proves the fit.
type Link struct {
	cwd   string
	disco *Discoverer

	mu     sync.Mutex
	client *Client
	info   LinkInfo

	// reconnect single-flight: a CallTool that finds the socket dropped runs
	// one reconnect; concurrent callers wait on the same attempt rather than
	// firing N parallel redials.
	reconnectMu sync.Mutex
}

// NewLink builds a Link bound to cwd (used for lock-file workspace matching).
// disco may be nil to use NewDiscoverer().
func NewLink(cwd string, disco *Discoverer) *Link {
	if disco == nil {
		disco = NewDiscoverer()
	}
	return &Link{cwd: cwd, disco: disco}
}

// Connect discovers the best lock (Discover()[0]), dials it, and stores the
// Client. Returns the chosen LinkInfo. If already connected it returns the
// current LinkInfo without redialing. Returns ErrNoIDE when no lock matches
// (distinct from a dial failure).
func (l *Link) Connect(ctx context.Context) (LinkInfo, error) {
	l.mu.Lock()
	if l.client != nil && l.info.Connected {
		info := l.info
		l.mu.Unlock()
		return info, nil
	}
	l.mu.Unlock()

	locks, err := l.disco.Discover(ctx, l.cwd)
	if err != nil {
		return LinkInfo{}, err
	}
	if len(locks) == 0 {
		return LinkInfo{}, ErrNoIDE
	}
	return l.ConnectLock(ctx, locks[0])
}

// ConnectLock dials a specific candidate (the /ide picker path). Any existing
// client is closed first.
func (l *Link) ConnectLock(ctx context.Context, lock Lock) (LinkInfo, error) {
	client, err := Dial(ctx, lock)
	if err != nil {
		return LinkInfo{}, err
	}
	info := LinkInfo{
		Connected: true,
		IDEName:   lock.IDEName,
		Workspace: matchedWorkspace(lock, l.cwd),
		Port:      lock.Port,
	}

	l.mu.Lock()
	old := l.client
	l.client = client
	l.info = info
	l.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	return info, nil
}

// Candidates exposes Discover for the slice-2 picker without connecting.
func (l *Link) Candidates(ctx context.Context) ([]Lock, error) {
	return l.disco.Discover(ctx, l.cwd)
}

// Close tears down the Client if any. Idempotent.
func (l *Link) Close() error {
	l.mu.Lock()
	c := l.client
	l.client = nil
	l.info = LinkInfo{}
	l.mu.Unlock()
	if c != nil {
		return c.Close()
	}
	return nil
}

// Status returns the current LinkInfo without dialing. Connected reflects the
// last known socket state; a drop detected on the next CallTool flips it.
func (l *Link) Status() LinkInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.info
}

// CallTool is the host-facing entry point. If not connected it returns
// ErrNotConnected. If the stored Client reports ErrNotConnected (a drop), Link
// performs a single-flight reconnect against the freshest matching lock and
// retries ONCE; a second failure returns ErrNotConnected.
func (l *Link) CallTool(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	l.mu.Lock()
	c := l.client
	l.mu.Unlock()
	if c == nil {
		return nil, ErrNotConnected
	}

	res, err := c.CallTool(ctx, tool, args)
	if !errors.Is(err, ErrNotConnected) {
		return res, err
	}

	// The socket dropped. Single-flight a reconnect, then retry once.
	if rerr := l.reconnect(ctx, c); rerr != nil {
		return nil, ErrNotConnected
	}
	l.mu.Lock()
	c2 := l.client
	l.mu.Unlock()
	if c2 == nil {
		return nil, ErrNotConnected
	}
	res, err = c2.CallTool(ctx, tool, args)
	if errors.Is(err, ErrNotConnected) {
		return nil, ErrNotConnected
	}
	return res, err
}

// reconnect re-dials the freshest matching lock, but only if the stored client
// is still the dead one (dropped) the caller observed — so concurrent callers
// who lost the race reuse the link the winner just established instead of
// redialing. Single-flight via reconnectMu.
func (l *Link) reconnect(ctx context.Context, dropped *Client) error {
	l.reconnectMu.Lock()
	defer l.reconnectMu.Unlock()

	// Did another caller already reconnect — or did the owner Close() the
	// Link — while we waited for the lock? Only redial when the stored client
	// is still the exact dead one we observed. Any other value means:
	//   - a winner replaced it with a fresh client (current == some other
	//     client) => reuse it, do not redial;
	//   - the Link was Close()d or markDisconnected'd (current == nil) =>
	//     return nil and let CallTool see c2 == nil and report ErrNotConnected,
	//     rather than resurrecting a deliberately torn-down Link with a fresh
	//     read-pump goroutine + socket nobody will ever Close.
	l.mu.Lock()
	current := l.client
	l.mu.Unlock()
	if current != dropped {
		return nil
	}

	locks, err := l.disco.Discover(ctx, l.cwd)
	if err != nil {
		return err
	}
	if len(locks) == 0 {
		l.markDisconnected()
		return ErrNoIDE
	}
	if _, err := l.ConnectLock(ctx, locks[0]); err != nil {
		l.markDisconnected()
		return err
	}
	return nil
}

// markDisconnected clears the link state after a failed reconnect.
func (l *Link) markDisconnected() {
	l.mu.Lock()
	c := l.client
	l.client = nil
	l.info = LinkInfo{}
	l.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// Connected reports whether a live client is held. It mirrors Status().Connected
// so Link satisfies host.IDELink.
func (l *Link) Connected() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.client != nil && l.info.Connected
}

// IDEName mirrors Status().IDEName.
func (l *Link) IDEName() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.info.IDEName
}

// Workspace mirrors Status().Workspace.
func (l *Link) Workspace() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.info.Workspace
}

// Port mirrors Status().Port.
func (l *Link) Port() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.info.Port
}

// matchedWorkspace returns the longest workspace folder of lock that is a
// path-boundary prefix of cwd, or the first folder (else "") when none match —
// so LinkInfo.Workspace is always populated when the lock has any folder.
func matchedWorkspace(lock Lock, cwd string) string {
	best := ""
	bestLen := -1
	for _, f := range lock.WorkspaceFolders {
		if n := longestWorkspacePrefix([]string{f}, cwd); n > bestLen {
			bestLen = n
			best = f
		}
	}
	if best != "" {
		return best
	}
	if len(lock.WorkspaceFolders) > 0 {
		return lock.WorkspaceFolders[0]
	}
	return ""
}
