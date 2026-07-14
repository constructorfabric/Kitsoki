package starlark

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// InspectCassette is a deterministic record of the fs/probe interactions a
// Starlark script makes. It is the replay (and, with a record path, the record)
// counterpart of the production inspector: a flow fixture supplies one of these
// and the testrunner builds an inspector from it so the REAL script runs with
// its filesystem + probe served from — or recorded to — disk. It is the
// inspection-side sibling of HTTPCassette, kept minimal: each interaction is a
// flat {op, target, exit, out} record matched in order.
//
//	kind: inspect_cassette
//	interactions:
//	  - op: read
//	    target: "go.mod"
//	    out: "module kitsoki\n"
//	  - op: probe
//	    target: "gh.issue.list"
//	    exit: 0
//	    out: '[{"number":1,"title":"x","state":"OPEN"}]'
type InspectCassette struct {
	Kind         string               `yaml:"kind" json:"kind"`
	Interactions []InspectInteraction `yaml:"interactions" json:"interactions"`
}

// InspectInteraction is one recorded fs/probe interaction in an InspectCassette.
// Op is one of read|exists|glob|probe; Target is the path/pattern/probe-name;
// Exit is the probe exit code (0 for fs ops); Out is the recorded payload — file
// bytes (read), probe output (probe). For exists, Out "true"/"false" encodes the
// result; for glob, Out is a newline-joined path list. For write, Out is the
// expected content; replay consumes it without touching disk.
type InspectInteraction struct {
	Op     string `yaml:"op" json:"op"`
	Target string `yaml:"target" json:"target"`
	Exit   int    `yaml:"exit,omitempty" json:"exit,omitempty"`
	Out    string `yaml:"out,omitempty" json:"out,omitempty"`

	consumed bool
}

// ReplayInspector is an Inspector that serves calls from an InspectCassette
// (replay only). A call that matches no remaining interaction returns a clear
// miss error naming the available interactions, so a drifted script or cassette
// fails loudly. It is the inspection-side analogue of ReplayClient.
type ReplayInspector struct {
	mu           sync.Mutex
	cas          *InspectCassette
	interactions []*InspectInteraction
	inspections  []InspectExchange
}

// NewReplayInspector builds a ReplayInspector over the cassette's interactions.
// Matching is stateful (interactions are consumed in order) and guarded by a
// mutex for safety.
func NewReplayInspector(cas *InspectCassette) *ReplayInspector {
	return &ReplayInspector{cas: cas, interactions: interactionPtrs(cas)}
}

// interactionPtrs returns a pointer slice over a cassette's interactions (so
// consumed state is tracked per interaction without copying).
func interactionPtrs(cas *InspectCassette) []*InspectInteraction {
	if cas == nil {
		return nil
	}
	ptrs := make([]*InspectInteraction, len(cas.Interactions))
	for i := range cas.Interactions {
		ptrs[i] = &cas.Interactions[i]
	}
	return ptrs
}

// match returns the first not-yet-consumed interaction with the given op and
// target, consuming it, or nil on a miss.
func (r *ReplayInspector) match(op, target string) *InspectInteraction {
	for _, it := range r.interactions {
		if it.consumed {
			continue
		}
		if it.Op == op && it.Target == target {
			it.consumed = true
			return it
		}
	}
	return nil
}

// missError builds the loud "no interaction matched" error naming the available
// interactions, mirroring the HTTP replay miss error.
func (r *ReplayInspector) missError(op, target string) error {
	keys := make([]string, len(r.interactions))
	for i, it := range r.interactions {
		keys[i] = fmt.Sprintf("%s %s", it.Op, it.Target)
	}
	return fmt.Errorf("starlark inspect replay: no interaction matched %s %s; available: %s",
		op, target, strings.Join(keys, ", "))
}

// Read replays a recorded read interaction.
func (r *ReplayInspector) Read(_ context.Context, path string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it := r.match("read", path)
	if it == nil {
		return nil, r.missError("read", path)
	}
	r.inspections = append(r.inspections, InspectExchange{Op: "read", Target: path, Status: "ok"})
	return []byte(it.Out), nil
}

// Exists replays a recorded exists interaction (Out "true"/"false").
func (r *ReplayInspector) Exists(_ context.Context, path string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it := r.match("exists", path)
	if it == nil {
		return false, r.missError("exists", path)
	}
	ok := strings.EqualFold(strings.TrimSpace(it.Out), "true")
	status := "missing"
	if ok {
		status = "ok"
	}
	r.inspections = append(r.inspections, InspectExchange{Op: "exists", Target: path, Status: status})
	return ok, nil
}

// Glob replays a recorded glob interaction (Out newline-joined paths).
func (r *ReplayInspector) Glob(_ context.Context, pattern string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it := r.match("glob", pattern)
	if it == nil {
		return nil, r.missError("glob", pattern)
	}
	var matches []string
	if trimmed := strings.TrimSpace(it.Out); trimmed != "" {
		matches = strings.Split(trimmed, "\n")
	}
	r.inspections = append(r.inspections, InspectExchange{Op: "glob", Target: pattern, Status: fmt.Sprintf("matched:%d", len(matches))})
	return matches, nil
}

// Write replays a recorded write interaction without touching disk.
func (r *ReplayInspector) Write(_ context.Context, path string, content []byte) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it := r.match("write", path)
	if it == nil {
		return "", r.missError("write", path)
	}
	if it.Out != string(content) {
		return "", fmt.Errorf("starlark inspect replay: write %s content mismatch", path)
	}
	r.inspections = append(r.inspections, InspectExchange{Op: "write", Target: path, Status: "ok"})
	return path, nil
}

// Probe replays a recorded probe interaction.
func (r *ReplayInspector) Probe(_ context.Context, name string, _ []string) (ProbeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it := r.match("probe", name)
	if it == nil {
		return ProbeResult{}, r.missError("probe", name)
	}
	r.inspections = append(r.inspections, InspectExchange{Op: "probe", Target: name, Status: fmt.Sprintf("exit:%d", it.Exit)})
	return ProbeResult{Exit: it.Exit, Out: it.Out}, nil
}

// Inspections returns the body-free summaries recorded so far, so the adapter
// can surface them on the trace exactly as it does for the production inspector.
func (r *ReplayInspector) Inspections() []InspectExchange {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]InspectExchange, len(r.inspections))
	copy(out, r.inspections)
	return out
}
