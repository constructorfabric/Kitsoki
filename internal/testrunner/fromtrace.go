package testrunner

// fromtrace.go — convert a recorded JSONL session trace into a replayable
// deterministic flow fixture (+ host cassette).
//
// The transform is pure and fast: it reads a kitsoki JSONL trace (the same
// format `kitsoki run` / `kitsoki turn` write, see docs/tracing/trace-format.md)
// and emits two YAML documents:
//
//   - a flow fixture (test_kind: flow) whose turns: list is one entry per
//     machine.transition in the trace, carrying the resolved intent name +
//     slots verbatim; and
//   - a host cassette (kind: host_cassette) whose episodes: list is one entry
//     per host.* call the trace recorded, in trace order, matched on handler.
//
// Why a cassette rather than host_handlers: a session's host/agent responses
// vary per call (e.g. host.agent.converse returns a different reply each of the
// five times it is invoked). host_handlers: declares ONE response per handler
// name and so cannot reproduce a varying session. A cassette's episodes are
// consumed first-unplayed-match-by-handler (MatchEpisode), so emitting one
// non-replay:any episode per recorded call — in order — replays each varying
// response exactly once, in sequence.
//
// Story-drift policy: the converter deliberately does NOT emit expect_state /
// expect_world on the generated turns. A trace recorded against an earlier
// version of a story may route differently against the current story (rooms
// added/removed on the path); strict expectations would hard-fail replay on the
// first divergence and hide the rest of the reconstruction. The generated flow
// is a faithful re-drive of the recorded *intents*, not an assertion of the old
// path. Add expectations by hand if you want to pin a (drift-free) path.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	goyaml "github.com/goccy/go-yaml"
)

// traceLine is the minimal shape of a JSONL trace event the converter reads.
// It intentionally parses only the fields the transform needs (kind, turn,
// state_path, payload) and tolerates any other fields, so it round-trips traces
// written by newer engines without coupling to the full store.Event schema.
type traceLine struct {
	Kind      string          `json:"kind"`
	Turn      int64           `json:"turn"`
	StatePath string          `json:"state_path"`
	Payload   json.RawMessage `json:"payload"`
}

// transitionPayload is the machine.transition payload: the gold source for one
// flow turn. slots is the resolved slot map (values may be strings, e.g.
// n: "1").
type transitionPayload struct {
	From      string         `json:"from"`
	To        string         `json:"to"`
	Intent    string         `json:"intent"`
	Slots     map[string]any `json:"slots"`
	Synthetic bool           `json:"synthetic"`
}

// turnInputPayload is the turn.input (store.UserInputReceived) payload. input is
// the human's actual free-text utterance for that turn — the words slidey shows
// in the user bubble. The converter captures it and stamps it onto the generated
// flow turn as display_input: so replay reproduces the operator's real words
// instead of the synthetic "[intent] <name>" the RunIntent path emits by default.
type turnInputPayload struct {
	Input string `json:"input"`
}

// harnessPayload is the harness.returned / harness.called payload. namespace is
// the host handler name; data is the handler's returned envelope.
type harnessPayload struct {
	Namespace string         `json:"namespace"`
	Data      map[string]any `json:"data"`
}

// FlowFromTrace is the result of converting a trace: the flow fixture document
// and (when the trace recorded any host call) the host cassette document, each
// already marshalled to YAML bytes. CassetteYAML is nil when the trace recorded
// no host.* calls (a pure intent-only session needs no cassette).
type FlowFromTrace struct {
	FlowYAML     []byte
	CassetteYAML []byte
	// NumTurns is the number of machine.transition events mapped to turns.
	NumTurns int
	// NumEpisodes is the number of host.* calls mapped to cassette episodes.
	NumEpisodes int
}

// ConvertOptions configures ConvertTraceToFlow.
type ConvertOptions struct {
	// AppPath is written verbatim into the fixture's app: field (e.g.
	// "../app.yaml" or an absolute path). Required.
	AppPath string
	// CassettePath is written verbatim into the fixture's host_cassette: field
	// and should be the path the cassette will live at relative to the fixture.
	// Ignored when the trace has no host calls. Required when host calls exist.
	CassettePath string
	// AppID is written into the cassette's app_id: field. Optional; defaults to
	// "from-trace".
	AppID string
	// InitialState overrides the derived initial state. When empty the converter
	// uses the FROM state of the first machine.transition (falling back to the
	// earliest event's state_path).
	InitialState string
	// InitialWorld is written verbatim as the fixture's initial_world:. When nil
	// an empty map is emitted (the app's world schema defaults plus on_enter
	// effects repopulate it on replay).
	InitialWorld map[string]any
}

// ConvertTraceToFlow reads the JSONL trace at tracePath and converts it into a
// flow fixture (+ cassette). It is a pure transform over the file contents.
func ConvertTraceToFlow(tracePath string, opts ConvertOptions) (*FlowFromTrace, error) {
	data, err := os.ReadFile(tracePath)
	if err != nil {
		return nil, fmt.Errorf("fromtrace: read %q: %w", tracePath, err)
	}
	lines, err := parseTraceLines(data)
	if err != nil {
		return nil, fmt.Errorf("fromtrace: parse %q: %w", tracePath, err)
	}
	return convertTraceLines(lines, opts)
}

// parseTraceLines splits the trace bytes into events, skipping the
// session.header and any blank lines. It does NOT enforce the strict read-time
// invariants (seq density, ordering) the JSONL sink does — the converter only
// reads kind/turn/payload and tolerates a best-effort trace.
func parseTraceLines(data []byte) ([]traceLine, error) {
	var out []traceLine
	for i, raw := range strings.Split(string(data), "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var tl traceLine
		if err := json.Unmarshal([]byte(raw), &tl); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if tl.Kind == "session.header" {
			continue
		}
		out = append(out, tl)
	}
	return out, nil
}

// convertTraceLines is the pure core of the transform, separated from file I/O
// so tests can drive it with a hand-built event slice.
func convertTraceLines(lines []traceLine, opts ConvertOptions) (*FlowFromTrace, error) {
	if opts.AppPath == "" {
		return nil, fmt.Errorf("fromtrace: AppPath is required")
	}
	appID := opts.AppID
	if appID == "" {
		appID = "from-trace"
	}

	// Derive initial state.
	initialState := opts.InitialState
	if initialState == "" {
		for _, tl := range lines {
			if tl.Kind == "machine.transition" {
				var p transitionPayload
				if err := json.Unmarshal(tl.Payload, &p); err == nil && p.From != "" {
					initialState = p.From
					break
				}
			}
		}
	}
	if initialState == "" {
		// Fall back to the earliest event carrying a state_path.
		for _, tl := range lines {
			if tl.StatePath != "" {
				initialState = tl.StatePath
				break
			}
		}
	}

	// Map machine.transition → turns, and harness.returned → cassette episodes,
	// preserving trace order.
	//
	// originalInputByTurn captures the operator's actual free-text utterance per
	// turn from the turn.input event. The transition that re-drives a turn shares
	// the same turn number, so we look the input up by tl.Turn when emitting the
	// flow turn and stamp it onto display_input: for faithful replay.
	operatorTurn := map[int64]bool{}
	originalInputByTurn := map[int64]string{}
	var turns []flowTurnDoc
	var episodes []cassetteEpisodeDoc
	for _, tl := range lines {
		switch tl.Kind {
		case "turn.input":
			var p turnInputPayload
			if err := json.Unmarshal(tl.Payload, &p); err != nil {
				// A malformed turn.input is non-fatal: replay still works, it just
				// falls back to the synthetic "[intent] <name>" string.
				continue
			}
			operatorTurn[tl.Turn] = true
			if p.Input != "" {
				originalInputByTurn[tl.Turn] = p.Input
			}
		case "machine.transition":
			var p transitionPayload
			if err := json.Unmarshal(tl.Payload, &p); err != nil {
				return nil, fmt.Errorf("fromtrace: decode machine.transition: %w", err)
			}
			if p.Intent == "" {
				// A transition with no resolved intent is not re-drivable; skip
				// it but keep going (e.g. synthetic timeout firings).
				continue
			}
			if p.Synthetic {
				continue
			}
			if len(operatorTurn) > 0 && !operatorTurn[tl.Turn] {
				continue
			}
			turns = append(turns, flowTurnDoc{
				Intent:       flowIntentDoc{Name: p.Intent, Slots: p.Slots},
				DisplayInput: originalInputByTurn[tl.Turn],
			})
		case "harness.returned":
			var p harnessPayload
			if err := json.Unmarshal(tl.Payload, &p); err != nil {
				return nil, fmt.Errorf("fromtrace: decode harness.returned: %w", err)
			}
			if !strings.HasPrefix(p.Namespace, "host.") {
				continue
			}
			episodes = append(episodes, cassetteEpisodeDoc{
				ID:    fmt.Sprintf("%s_%d", episodeIDSlug(p.Namespace), len(episodes)+1),
				Match: map[string]any{"handler": p.Namespace},
				Response: cassetteResponseDoc{
					Data: p.Data,
				},
			})
		}
	}

	if len(turns) == 0 {
		return nil, fmt.Errorf("fromtrace: trace has no replayable machine.transition events")
	}

	initialWorld := opts.InitialWorld
	if initialWorld == nil {
		initialWorld = map[string]any{}
	}

	fixture := flowFixtureDoc{
		TestKind:     "flow",
		App:          opts.AppPath,
		InitialState: initialState,
		InitialWorld: initialWorld,
		Turns:        turns,
	}
	result := &FlowFromTrace{NumTurns: len(turns), NumEpisodes: len(episodes)}

	if len(episodes) > 0 {
		if opts.CassettePath == "" {
			return nil, fmt.Errorf("fromtrace: trace recorded %d host calls but CassettePath is empty", len(episodes))
		}
		fixture.HostCassette = opts.CassettePath
		cas := cassetteDoc{
			Kind:        "host_cassette",
			AppID:       appID,
			GeneratedAt: "from-trace",
			MatchOn:     []string{"handler"},
			Episodes:    episodes,
		}
		cb, err := goyaml.Marshal(cas)
		if err != nil {
			return nil, fmt.Errorf("fromtrace: marshal cassette: %w", err)
		}
		result.CassetteYAML = withHeader(casHeader, cb)
	}

	fb, err := goyaml.Marshal(fixture)
	if err != nil {
		return nil, fmt.Errorf("fromtrace: marshal flow: %w", err)
	}
	result.FlowYAML = withHeader(flowHeader, fb)
	return result, nil
}

// episodeIDSlug turns a handler namespace ("host.agent.converse") into a
// readable, YAML-safe episode-id slug ("host_agent_converse").
func episodeIDSlug(ns string) string {
	return strings.NewReplacer(".", "_", "-", "_").Replace(ns)
}

// withHeader prepends a comment header to marshalled YAML bytes.
func withHeader(header string, body []byte) []byte {
	return append([]byte(header), body...)
}

const flowHeader = "# Generated by `kitsoki trace to-flow`.\n" +
	"# Turns are one-per-machine.transition (intent + resolved slots, verbatim).\n" +
	"# No expect_state/expect_world is emitted: a trace recorded against an older\n" +
	"# story may route differently against the current one; strict expectations\n" +
	"# would hard-fail on the first drift. Host/agent responses replay from the\n" +
	"# sibling cassette in trace order.\n"

const casHeader = "# Generated by `kitsoki trace to-flow`.\n" +
	"# One episode per recorded host.* call, in trace order, matched on handler.\n" +
	"# Episodes are NOT replay:any, so the i-th call to a handler consumes the\n" +
	"# i-th matching episode — this reproduces per-call-varying responses.\n"

// ─── YAML document shapes (write-side only) ──────────────────────────────────
//
// These mirror the read-side FlowFixture / Cassette structs but are write-only
// and ordered for stable, human-friendly output. We keep them local so the
// converter controls field order and omitempty independently of the runner's
// parse structs.

type flowFixtureDoc struct {
	TestKind     string         `yaml:"test_kind"`
	App          string         `yaml:"app"`
	HostCassette string         `yaml:"host_cassette,omitempty"`
	InitialState string         `yaml:"initial_state"`
	InitialWorld map[string]any `yaml:"initial_world"`
	Turns        []flowTurnDoc  `yaml:"turns"`
}

type flowTurnDoc struct {
	Intent flowIntentDoc `yaml:"intent"`
	// DisplayInput carries the operator's original free-text utterance for the
	// turn (from the trace's turn.input event). On replay the flow runner stamps
	// it onto the recorded turn.input / turn.start events in place of the
	// synthetic "[intent] <name>" string, so a reconstructed trace shows the
	// operator's real words. Omitted when the trace had no input for the turn.
	DisplayInput string `yaml:"display_input,omitempty"`
}

type flowIntentDoc struct {
	Name  string         `yaml:"name"`
	Slots map[string]any `yaml:"slots,omitempty"`
}

type cassetteDoc struct {
	Kind        string               `yaml:"kind"`
	AppID       string               `yaml:"app_id"`
	GeneratedAt string               `yaml:"generated_at,omitempty"`
	MatchOn     []string             `yaml:"match_on,omitempty"`
	Episodes    []cassetteEpisodeDoc `yaml:"episodes"`
}

type cassetteEpisodeDoc struct {
	ID       string              `yaml:"id"`
	Match    map[string]any      `yaml:"match"`
	Response cassetteResponseDoc `yaml:"response"`
}

type cassetteResponseDoc struct {
	Data map[string]any `yaml:"data,omitempty"`
}
