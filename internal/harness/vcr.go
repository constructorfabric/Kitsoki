package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/goccy/go-yaml"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// VCRMode selects the python-vcr-style record/playback policy a VCRHarness
// applies to its routing cassette. The four modes trade determinism against
// the ability to grow the cassette by calling the live harness on a miss.
//
//	none — replay on hit; HARD ERROR on miss (ErrRecordingMiss). Zero live
//	       calls ever. The CI / regression mode: a cassette drift fails the
//	       run instead of silently falling through to a real LLM.
//	once — replay on hit; on miss call live + append, but ONLY while the
//	       cassette started empty/new. Once a non-empty cassette exists a
//	       miss is an error (record the suite once, then it's frozen).
//	new  — replay on hit; on miss call live + append the novel entry. The
//	       exploratory-QA mode: replay what's known, grow the cassette.
//	all  — ignore the cassette on read; always call live and (re)record
//	       every turn. The re-bake mode after a story change.
type VCRMode int

const (
	// VCRModeNone replays on hit and errors on miss — no live calls.
	VCRModeNone VCRMode = iota
	// VCRModeOnce replays on hit; records a miss only while the cassette is empty/new.
	VCRModeOnce
	// VCRModeNew replays on hit; records every novel miss live.
	VCRModeNew
	// VCRModeAll ignores the cassette on read; always records live.
	VCRModeAll
)

// ParseVCRMode maps the --record flag value to a VCRMode.
func ParseVCRMode(s string) (VCRMode, error) {
	switch s {
	case "none", "":
		return VCRModeNone, nil
	case "once":
		return VCRModeOnce, nil
	case "new":
		return VCRModeNew, nil
	case "all":
		return VCRModeAll, nil
	default:
		return VCRModeNone, fmt.Errorf("harness/vcr: unknown --record mode %q (want none|once|new|all)", s)
	}
}

// String renders the mode as its --record flag token.
func (m VCRMode) String() string {
	switch m {
	case VCRModeNone:
		return "none"
	case VCRModeOnce:
		return "once"
	case VCRModeNew:
		return "new"
	case VCRModeAll:
		return "all"
	default:
		return "unknown"
	}
}

// VCRHarness is the routing harness `kitsoki drive` wires in place of the
// stateless turn path's noRunHarness. It unifies replay and recording over a
// single recording.yaml cassette: replays known (state, input) pairs for free,
// and — per its VCRMode — falls through to a live harness on a miss, appending
// the novel entry back to the cassette in the SAME on-disk YAML shape a later
// `--harness replay --record none` run reads (decision: one file, no convert
// step; see docs/proposals/qa-drive-command.md).
//
// The replay/record file shape is recordingFile (replay.go) — the canonical
// cassette format — so a VCRHarness-recorded cassette round-trips through
// NewReplay unchanged.
type VCRHarness struct {
	mode VCRMode
	path string
	live Harness

	mu sync.Mutex
	// rf is the in-memory cassette: loaded entries plus any appended this run.
	rf recordingFile
	// replay indexes rf for (state, input) lookup. Rebuilt on every append so
	// a just-recorded entry replays on a later identical turn within one run.
	replay *ReplayHarness
	// startedEmpty records whether the cassette was absent or empty at open —
	// the gate VCRModeOnce uses to decide a miss may still be recorded.
	startedEmpty bool
	// lastConfidence is the confidence of the most recently resolved turn,
	// surfaced via LastConfidence (ConfidenceReporter) for kitsoki drive.
	lastConfidence float64
}

// LastConfidence reports the confidence of the most recently resolved turn —
// the replayed cassette entry's confidence on a hit, or the live harness's
// reported confidence on a recorded miss. Satisfies ConfidenceReporter.
func (h *VCRHarness) LastConfidence() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastConfidence
}

// NewVCR opens (or prepares) the cassette at path and returns a VCRHarness in
// the given mode. live is the fall-through harness used on a miss for modes
// that record; it MAY be nil for VCRModeNone (which never calls live) — a nil
// live in any recording mode is rejected so a misconfigured drive fails loudly
// instead of falling through to a real LLM.
//
// A missing cassette file is not an error: it is treated as an empty,
// not-yet-written cassette (startedEmpty=true). A present-but-malformed
// cassette IS an error.
func NewVCR(mode VCRMode, path string, live Harness) (*VCRHarness, error) {
	if path == "" {
		return nil, errors.New("harness/vcr: cassette path is required")
	}
	if mode != VCRModeNone && live == nil {
		return nil, fmt.Errorf("harness/vcr: mode %q records on a miss but no live harness was supplied", mode)
	}

	h := &VCRHarness{mode: mode, path: path, live: live}

	data, readErr := os.ReadFile(path)
	switch {
	case errors.Is(readErr, os.ErrNotExist):
		// Fresh cassette. Seed the header so the first append writes a
		// well-formed recording.yaml.
		h.rf = recordingFile{Kind: "recording", Generator: "kitsoki drive (vcr)"}
		h.startedEmpty = true
	case readErr != nil:
		return nil, fmt.Errorf("harness/vcr: read cassette %q: %w", path, readErr)
	default:
		if err := yaml.Unmarshal(data, &h.rf); err != nil {
			return nil, fmt.Errorf("harness/vcr: parse cassette %q: %w", path, err)
		}
		if h.rf.Kind == "" {
			h.rf.Kind = "recording"
		}
		if h.rf.Kind != "recording" {
			return nil, fmt.Errorf("harness/vcr: cassette %q has unexpected kind %q (want \"recording\")", path, h.rf.Kind)
		}
		h.startedEmpty = len(h.rf.Entries) == 0
	}

	// Mode all ignores existing entries on read (it always records); every
	// other mode indexes the loaded entries for replay.
	if mode != VCRModeAll {
		if err := h.rebuildReplay(); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// RunTurn applies the VCR policy: replay on a hit, then per-mode fall through
// to the live harness on a miss (recording the result back to the cassette).
func (h *VCRHarness) RunTurn(ctx context.Context, in TurnInput) (mcp.CallToolParams, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Replay tier (skipped entirely in mode all).
	if h.mode != VCRModeAll && h.replay != nil {
		params, err := h.replay.RunTurn(ctx, in)
		if err == nil {
			h.lastConfidence = h.replay.LastConfidence()
			return params, nil
		}
		var miss *ErrRecordingMiss
		if !errors.As(err, &miss) {
			// A clarify entry or any non-miss error is authoritative — surface it.
			return params, err
		}
		// fall through to the miss policy below.
	}

	// Miss policy.
	switch h.mode {
	case VCRModeNone:
		return mcp.CallToolParams{}, &ErrRecordingMiss{State: string(in.StatePath), Input: in.UserText}
	case VCRModeOnce:
		if !h.startedEmpty {
			// A frozen cassette: a miss is a hard error, never a live call.
			return mcp.CallToolParams{}, &ErrRecordingMiss{State: string(in.StatePath), Input: in.UserText}
		}
	case VCRModeNew, VCRModeAll:
		// always allowed to record
	}

	// Record tier: call live, append the resolved intent to the cassette.
	params, err := h.live.RunTurn(ctx, in)
	if err != nil {
		// A live clarify/no-match is NOT recorded as an intent entry — surface it.
		return params, err
	}
	_, _, h.lastConfidence = parseTransitionArgs(params)
	if appendErr := h.appendLocked(in, params); appendErr != nil {
		return params, appendErr
	}
	return params, nil
}

// appendLocked records one (state, input)→intent entry and re-serialises the
// whole cassette to disk in the recording.yaml shape, then re-indexes replay
// so a repeat of the same turn this run hits the cache. Caller holds h.mu.
func (h *VCRHarness) appendLocked(in TurnInput, params mcp.CallToolParams) error {
	intentName, slots, conf := parseTransitionArgs(params)
	entry := recordingEntry{
		State:      string(in.StatePath),
		Input:      in.UserText,
		Intent:     recordingIntent{Name: intentName, Slots: slots},
		Confidence: conf,
	}
	h.rf.Entries = append(h.rf.Entries, entry)
	if h.rf.GeneratedAt == "" {
		h.rf.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := writeRecordingFile(h.path, h.rf); err != nil {
		return err
	}
	// A non-empty cassette now exists on disk; mode once stops recording further.
	h.startedEmpty = false
	return h.rebuildReplay()
}

// rebuildReplay re-indexes the in-memory entries into a ReplayHarness. With no
// entries the replay tier is left nil (every lookup is a miss, by design).
func (h *VCRHarness) rebuildReplay() error {
	if len(h.rf.Entries) == 0 {
		h.replay = nil
		return nil
	}
	rp, err := newReplayFromFile(h.rf)
	if err != nil {
		return fmt.Errorf("harness/vcr: index cassette: %w", err)
	}
	h.replay = rp
	return nil
}

// Close closes the live harness (if any). The cassette is flushed on every
// append, so there is nothing buffered to flush here.
func (h *VCRHarness) Close() error {
	if h.live != nil {
		return h.live.Close()
	}
	return nil
}

// writeRecordingFile serialises a recordingFile to path in the canonical
// recording.yaml shape (the same shape NewReplay reads). The write is atomic
// (temp file + rename) so a concurrent replay reader never sees a half-written
// cassette.
func writeRecordingFile(path string, rf recordingFile) error {
	if rf.Kind == "" {
		rf.Kind = "recording"
	}
	data, err := yaml.Marshal(rf)
	if err != nil {
		return fmt.Errorf("harness/vcr: marshal cassette: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("harness/vcr: write cassette %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("harness/vcr: rename cassette %q: %w", path, err)
	}
	return nil
}
