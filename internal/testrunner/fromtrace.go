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
	"path/filepath"
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

// These trace payloads complete a host.agent.* cassette episode. A host return
// can replay state changes alone, but the workbench also needs the canonical
// agent lifecycle pair to render the captured exchange.
type agentCallStartPayload struct {
	Verb       string `json:"verb"`
	Agent      string `json:"agent"`
	Model      string `json:"model"`
	Prompt     string `json:"prompt"`
	PromptFile string `json:"prompt_file"`
}

type agentCallCompletePayload struct {
	Verb         string          `json:"verb"`
	Agent        string          `json:"agent"`
	Model        string          `json:"model"`
	DurationMS   int64           `json:"duration_ms"`
	Response     json.RawMessage `json:"response"`
	ResponseFile string          `json:"response_file"`
	Meta         struct {
		CostUSD float64 `json:"cost_usd"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"meta"`
	TranscriptRef *struct {
		Format string `json:"format"`
		Path   string `json:"path"`
	} `json:"transcript_ref"`
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
	// EmitAcceptance, when true, appends a DRAFT session-level acceptance:
	// block to the generated fixture: final_state_in pins the final
	// machine.transition's target, host_calls.required lists the unique
	// dispatched host handlers (handler-only — no args, so the entries stay
	// portable across arg-shape drift), and world: is emitted empty with a
	// comment telling the operator to curate the key world vars. The draft is
	// deliberately coarse — an outcome contract, not a path assertion — so the
	// story-drift policy above is unaffected: still no per-turn expect_state /
	// expect_world.
	EmitAcceptance bool
	traceDir       string
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
	opts.traceDir = filepath.Dir(tracePath)
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
	agentStarts := map[int64]agentCallStartPayload{}
	agentCompletes := map[int64]agentCallCompletePayload{}
	// finalState / dispatchedHandlers feed the draft acceptance: block when
	// EmitAcceptance is set. finalState tracks the target of the LAST
	// machine.transition — including synthetic and on_complete transitions the
	// turn mapping skips, because they still move the session and the contract
	// pins where the session ended up, not how it was driven there.
	// dispatchedHandlers is the unique host.* handler set in first-seen order.
	finalState := ""
	var dispatchedHandlers []string
	seenHandler := map[string]bool{}
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
			if p.To != "" {
				finalState = p.To
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
		case "agent.call.start":
			var p agentCallStartPayload
			if json.Unmarshal(tl.Payload, &p) == nil {
				agentStarts[tl.Turn] = p
			}
		case "agent.call.complete":
			var p agentCallCompletePayload
			if json.Unmarshal(tl.Payload, &p) == nil {
				agentCompletes[tl.Turn] = p
			}
		case "harness.returned":
			var p harnessPayload
			if err := json.Unmarshal(tl.Payload, &p); err != nil {
				return nil, fmt.Errorf("fromtrace: decode harness.returned: %w", err)
			}
			if !strings.HasPrefix(p.Namespace, "host.") {
				continue
			}
			if !seenHandler[p.Namespace] {
				seenHandler[p.Namespace] = true
				dispatchedHandlers = append(dispatchedHandlers, p.Namespace)
			}
			ep := cassetteEpisodeDoc{
				ID:    fmt.Sprintf("%s_%d", episodeIDSlug(p.Namespace), len(episodes)+1),
				Match: map[string]any{"handler": p.Namespace},
				Response: cassetteResponseDoc{
					Data: p.Data,
				},
			}
			if strings.HasPrefix(p.Namespace, "host.agent.") {
				ep.Agent = episodeAgentFromTrace(agentStarts[tl.Turn], agentCompletes[tl.Turn], opts.traceDir)
			}
			episodes = append(episodes, ep)
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

	if opts.EmitAcceptance {
		draft, dErr := buildAcceptanceDraft(finalState, dispatchedHandlers)
		if dErr != nil {
			return nil, fmt.Errorf("fromtrace: build acceptance draft: %w", dErr)
		}
		result.FlowYAML = append(result.FlowYAML, draft...)
	}
	return result, nil
}

// buildAcceptanceDraft renders the DRAFT acceptance: block appended to the
// generated fixture when ConvertOptions.EmitAcceptance is set. The block is
// marshalled through goyaml (so state paths and handler names are quoted
// correctly) and then annotated with operator-facing comments — goyaml cannot
// emit comments itself, so they are spliced into the rendered lines.
func buildAcceptanceDraft(finalState string, handlers []string) ([]byte, error) {
	draft := acceptanceDraftDoc{
		FinalStateIn: []string{},
		World:        map[string]any{},
	}
	if finalState != "" {
		draft.FinalStateIn = append(draft.FinalStateIn, finalState)
	}
	if len(handlers) > 0 {
		hc := &acceptanceHostCallsDraftDoc{}
		for _, h := range handlers {
			hc.Required = append(hc.Required, acceptanceRequiredDraftDoc{Handler: h})
		}
		draft.HostCalls = hc
	}
	wrapper := struct {
		Acceptance acceptanceDraftDoc `yaml:"acceptance"`
	}{Acceptance: draft}
	b, err := goyaml.Marshal(wrapper)
	if err != nil {
		return nil, err
	}
	out := "# DRAFT acceptance contract derived from the recorded trace — curate before\n" +
		"# trusting: it pins the final state and the set of dispatched host handlers,\n" +
		"# nothing more.\n" +
		strings.Replace(string(b),
			"\n  world: {}",
			"\n  # TODO(operator): curate the key world vars this session must land\n"+
				"  # (plain scalar = exact JSON-normalized match; { matches: \"regex\" } = Go regex).\n"+
				"  world: {}",
			1)
	return []byte(out), nil
}

func episodeAgentFromTrace(start agentCallStartPayload, complete agentCallCompletePayload, traceDir string) *EpisodeAgent {
	if start.Verb == "" && complete.Verb == "" {
		return nil
	}
	verb := complete.Verb
	if verb == "" {
		verb = start.Verb
	}
	agent := complete.Agent
	if agent == "" {
		agent = start.Agent
	}
	model := complete.Model
	if model == "" {
		model = start.Model
	}
	prompt := start.Prompt
	if prompt == "" {
		prompt = readTraceSidecar(traceDir, start.PromptFile)
	}
	response := string(complete.Response)
	if b := readTraceSidecar(traceDir, complete.ResponseFile); b != "" {
		response = b
	}
	ep := &EpisodeAgent{Verb: verb, Agent: agent, Model: model, DurationMs: complete.DurationMS,
		PromptTokens: complete.Meta.Usage.InputTokens, ResponseTokens: complete.Meta.Usage.OutputTokens,
		CostUSD: complete.Meta.CostUSD, Prompt: prompt, Response: response}
	if complete.TranscriptRef != nil {
		ep.Transcript = readTraceTranscript(traceDir, complete.TranscriptRef.Path, complete.TranscriptRef.Format)
	}
	return ep
}

func readTraceSidecar(traceDir, rel string) string {
	if traceDir == "" || rel == "" || filepath.IsAbs(rel) {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(traceDir, rel))
	if err != nil {
		return ""
	}
	return string(b)
}

func readTraceTranscript(traceDir, rel, format string) *EpisodeTranscript {
	data := readTraceSidecar(traceDir, rel)
	if data == "" {
		return nil
	}
	et := &EpisodeTranscript{Format: format}
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if json.Valid([]byte(line)) {
			et.Events = append(et.Events, line)
		}
	}
	if len(et.Events) == 0 {
		return nil
	}
	return et
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

// acceptanceDraftDoc is the write-side shape of the draft acceptance: block
// (buildAcceptanceDraft). It mirrors the read-side FlowAcceptance but keeps
// final_state_in and world non-omitempty so the draft always shows both keys —
// the empty world: {} is the operator's curation hook, not an omission.
type acceptanceDraftDoc struct {
	FinalStateIn []string                     `yaml:"final_state_in"`
	World        map[string]any               `yaml:"world"`
	HostCalls    *acceptanceHostCallsDraftDoc `yaml:"host_calls,omitempty"`
}

type acceptanceHostCallsDraftDoc struct {
	Required []acceptanceRequiredDraftDoc `yaml:"required"`
}

// acceptanceRequiredDraftDoc is one handler-only required entry — the draft
// deliberately omits args so the contract stays portable across arg drift.
type acceptanceRequiredDraftDoc struct {
	Handler string `yaml:"handler"`
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
	Agent    *EpisodeAgent       `yaml:"agent,omitempty"`
}

type cassetteResponseDoc struct {
	Data map[string]any `yaml:"data,omitempty"`
}
