package ide

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Lock is one parsed ~/.claude/ide/<port>.lock file. Port is taken from the
// filename, NOT the body (see the wire note §2): the body never carries it.
// All other fields are the JSON payload the editor extension writes.
type Lock struct {
	Port             int      // from "<port>.lock" filename
	Path             string   // absolute path to the lock file (provenance/debug)
	PID              int      `json:"pid"`              // VS Code window PID (process.ppid)
	WorkspaceFolders []string `json:"workspaceFolders"` // absolute workspace roots
	IDEName          string   `json:"ideName"`          // e.g. "Visual Studio Code"
	Transport        string   `json:"transport"`        // "ws"
	RunningInWindows bool     `json:"runningInWindows"`
	AuthToken        string   `json:"authToken"` // per-activation UUID
}

// sseEnvVar is the editor-seeded port hint. Despite the name the transport is
// WebSocket, not SSE — when set and a matching lock exists it wins discovery
// outright (the integrated-terminal fast path).
const sseEnvVar = "CLAUDE_CODE_SSE_PORT"

// Discoverer finds and ranks IDE lock files for a working directory. The
// default implementation reads ~/.claude/ide; LockDir and Environ are
// overridable in tests so no real ~/.claude is touched and no real env is read.
type Discoverer struct {
	LockDir string              // default: ~/.claude/ide
	Environ func(string) string // default os.Getenv; reads CLAUDE_CODE_SSE_PORT
}

// NewDiscoverer returns a Discoverer with production defaults: the lock dir
// under the user's home and os.Getenv for the SSE-port hint. If the home
// directory cannot be resolved LockDir is left empty, and Discover then returns
// no candidates (the normal "no IDE" path) rather than erroring.
func NewDiscoverer() *Discoverer {
	dir := ""
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".claude", "ide")
	}
	return &Discoverer{LockDir: dir, Environ: os.Getenv}
}

// environ returns the configured env reader, defaulting to os.Getenv.
func (d *Discoverer) environ(key string) string {
	if d.Environ != nil {
		return d.Environ(key)
	}
	return os.Getenv(key)
}

// Discover parses every <port>.lock under LockDir, then returns candidates
// ordered for a picker (best first):
//
//  1. If $CLAUDE_CODE_SSE_PORT is set AND a lock with that port exists, it is
//     element 0 (exact env match wins outright).
//  2. Otherwise locks whose workspaceFolders contains a path that is a prefix
//     of cwd, ordered by LONGEST matching prefix first (most specific workspace
//     wins). Ties (equal prefix length) keep filesystem-read order.
//  3. Remaining locks (no workspace match) follow, so a caller can still offer
//     them in a picker.
//
// Malformed lock files (bad JSON, transport != "ws", missing authToken) are
// skipped silently — a half-written file from a starting IDE must not error
// discovery. cwd is matched after filepath.Clean; the prefix test is on path
// boundaries (cwd == folder, or cwd starts with folder+separator), never a bare
// strings.HasPrefix, so /foo/bar does not match workspace /foo/ba.
//
// Returns (nil, nil) when LockDir is absent or holds no usable lock — an empty
// result, not an error: "no IDE running" is a normal state.
func (d *Discoverer) Discover(ctx context.Context, cwd string) ([]Lock, error) {
	if d.LockDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(d.LockDir)
	if err != nil {
		// Absent dir is the common "no IDE ever ran" case — not an error.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	cwd = filepath.Clean(cwd)

	// Parse every usable lock, preserving filesystem-read order for tie-breaks.
	var locks []Lock
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".lock")
		port, err := strconv.Atoi(base)
		if err != nil {
			continue // not a "<port>.lock"
		}
		path := filepath.Join(d.LockDir, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			continue // disappeared / unreadable mid-scan
		}
		var lk Lock
		if err := json.Unmarshal(body, &lk); err != nil {
			continue // half-written JSON from a starting IDE
		}
		if lk.Transport != "ws" || lk.AuthToken == "" {
			continue // not usable
		}
		lk.Port = port
		lk.Path = path
		locks = append(locks, lk)
	}
	if len(locks) == 0 {
		return nil, nil
	}

	// 1. Exact env-port match wins outright.
	var envPort int
	if v := d.environ(sseEnvVar); v != "" {
		if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			envPort = p
		}
	}

	// Compute the longest workspace prefix match for each lock.
	type ranked struct {
		lock     Lock
		prefix   int  // length of the longest matching workspace folder, -1 = none
		envMatch bool // exact CLAUDE_CODE_SSE_PORT match
		order    int  // original read order, for stable tie-breaks
	}
	rs := make([]ranked, len(locks))
	for i, lk := range locks {
		rs[i] = ranked{
			lock:     lk,
			prefix:   longestWorkspacePrefix(lk.WorkspaceFolders, cwd),
			envMatch: envPort != 0 && lk.Port == envPort,
			order:    i,
		}
	}

	sort.SliceStable(rs, func(a, b int) bool {
		ra, rb := rs[a], rs[b]
		// Env match is the single strongest signal.
		if ra.envMatch != rb.envMatch {
			return ra.envMatch
		}
		// Then a workspace match beats none, longest prefix first.
		if ra.prefix != rb.prefix {
			return ra.prefix > rb.prefix
		}
		// Stable: preserve filesystem-read order on a true tie.
		return ra.order < rb.order
	})

	out := make([]Lock, len(rs))
	for i := range rs {
		out[i] = rs[i].lock
	}
	return out, nil
}

// longestWorkspacePrefix returns the length (in bytes) of the longest folder in
// folders that is a path-boundary prefix of cwd, or -1 when none match. A folder
// matches when cwd equals it or cwd begins with folder + os.PathSeparator, so
// /foo/bar matches workspace /foo but not /foo/ba.
func longestWorkspacePrefix(folders []string, cwd string) int {
	best := -1
	for _, f := range folders {
		f = filepath.Clean(f)
		if cwd == f {
			if len(f) > best {
				best = len(f)
			}
			continue
		}
		if strings.HasPrefix(cwd, f+string(os.PathSeparator)) {
			if len(f) > best {
				best = len(f)
			}
		}
	}
	return best
}
