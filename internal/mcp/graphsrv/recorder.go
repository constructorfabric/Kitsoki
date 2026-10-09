package graphsrv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CallRecord is one entry in the server's last-N tool-call ring buffer
// (plan §3.6): args are hashed, never stored verbatim, so it can be
// attached as redacted evidence to a feedback.report submission.
type CallRecord struct {
	Tool       string    `json:"tool"`
	ArgsDigest string    `json:"args_digest"`
	ResultCode string    `json:"result_code"` // "ok" or an ErrorPayload.Code
	At         time.Time `json:"at"`
}

// ringBufferSize is the "last-10-call" evidence window the plan specifies.
const ringBufferSize = 10

// Recorder is a fixed-size, mutex-guarded ring buffer of the server's most
// recent tool calls across every registered tool (not just feedback.*).
type Recorder struct {
	mu      sync.Mutex
	entries []CallRecord // oldest first, capped at ringBufferSize
}

// NewRecorder builds an empty Recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// Record appends one call to the ring buffer, evicting the oldest entry
// once ringBufferSize is exceeded.
func (r *Recorder) Record(tool, argsDigest, resultCode string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, CallRecord{Tool: tool, ArgsDigest: argsDigest, ResultCode: resultCode, At: at})
	if len(r.entries) > ringBufferSize {
		r.entries = r.entries[len(r.entries)-ringBufferSize:]
	}
}

// Snapshot returns a copy of the current ring buffer, oldest first.
func (r *Recorder) Snapshot() []CallRecord {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]CallRecord, len(r.entries))
	copy(out, r.entries)
	return out
}

// digestArgs hashes a tool call's raw JSON arguments (sha256, first 16 hex
// chars) so evidence trails (the ring buffer, feedback.report's attached
// evidence) never carry raw argument values — the "redacted" requirement.
func digestArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "-"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}

// resultCodeOf extracts "ok" or the ErrorPayload code from a rendered tool
// result, for the ring buffer's result_code field.
func resultCodeOf(res *mcpsdk.CallToolResult) string {
	if res == nil || !res.IsError {
		return "ok"
	}
	if ep, ok := res.StructuredContent.(*ErrorPayload); ok && ep != nil {
		return ep.Code
	}
	return "ERROR"
}

// recorded wraps a tool handler so every call — success or error — appends
// one entry to deps.Recorder before returning the result unmodified. Used
// at every tool's registration site so feedback.report always has a real
// evidence trail to attach, regardless of which tools were actually called
// leading up to it.
//
// It is also the server's panic firewall: mcpsdk v1.0.0 does not recover
// tool-handler panics, so an unrecovered panic anywhere in a handler would
// kill the whole stdio server process. Since every tool registers through
// this wrapper, recovering here converts any handler panic into a normal
// CodeInternal error payload (still recorded as evidence) while the panic
// value and stack go to stderr for postmortem — the server stays up.
func recorded(deps *Deps, tool string, h mcpsdk.ToolHandler) mcpsdk.ToolHandler {
	record := func(req *mcpsdk.CallToolRequest, res *mcpsdk.CallToolResult) {
		if deps.Recorder == nil {
			return
		}
		at := time.Now().UTC()
		if deps.Clock != nil {
			at = deps.Clock.Now()
		}
		deps.Recorder.Record(tool, digestArgs(req.Params.Arguments), resultCodeOf(res), at)
	}
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (res *mcpsdk.CallToolResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "mcp-graph: recovered panic in tool %q: %v\n%s", tool, r, debug.Stack())
				res = errorResult(NewError(CodeInternal,
					fmt.Sprintf("%s: internal error (recovered panic): %v", tool, r),
					"this is a server bug — the call failed but the server is still up; the stack was written to the server's stderr"))
				err = nil
				record(req, res)
			}
		}()
		res, err = h(ctx, req)
		record(req, res)
		return res, err
	}
}
