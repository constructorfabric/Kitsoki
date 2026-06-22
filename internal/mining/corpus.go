package mining

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"kitsoki/internal/store"
)

// SourceID identifies the backend that produced a mined session.
type SourceID string

const (
	SourceClaudeCode    SourceID = "claude-code"
	SourceCodex         SourceID = "codex"
	SourceKitsokiTrace  SourceID = "kitsoki-trace"
	SourceImportedJSONL SourceID = "imported-jsonl"
)

// SourceScope constrains discovery for a session source.
type SourceScope struct {
	RepoPath   string
	Dirs       []string
	SinceMTime int64
}

// SessionRef is the cheap discovery record for a source session.
type SessionRef struct {
	Source SourceID `json:"source"`
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	MTime  int64    `json:"mtime,omitempty"`
}

// SourceRef points back into the raw source artifact.
type SourceRef struct {
	Backend SourceID `json:"backend,omitempty"`
	Path    string   `json:"path,omitempty"`
	Line    int      `json:"line,omitempty"`
	Event   int      `json:"event,omitempty"`
}

// CanonicalSession is the backend-neutral evidence envelope consumed by mining
// drivers. It intentionally preserves source refs instead of hiding backend
// details behind prose traces.
type CanonicalSession struct {
	SchemaVersion string              `json:"schema_version"`
	Source        CanonicalSource     `json:"source"`
	Session       CanonicalMetadata   `json:"session"`
	Turns         []CanonicalTurn     `json:"turns,omitempty"`
	ToolCalls     []CanonicalToolCall `json:"tool_calls,omitempty"`
	Usage         CanonicalUsage      `json:"usage,omitempty"`
	Kitsoki       *CanonicalKitsoki   `json:"kitsoki,omitempty"`
}

type CanonicalSource struct {
	Backend SourceID `json:"backend"`
	Ref     string   `json:"ref"`
	Path    string   `json:"path,omitempty"`
	MTime   int64    `json:"mtime,omitempty"`
}

type CanonicalMetadata struct {
	ID         string `json:"id"`
	Repo       string `json:"repo,omitempty"`
	CWD        string `json:"cwd,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty"`
}

type CanonicalTurn struct {
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	Text      string          `json:"text,omitempty"`
	SourceRef SourceRef       `json:"source_ref,omitempty"`
	Attrs     json.RawMessage `json:"attrs,omitempty"`
}

type CanonicalToolCall struct {
	ID        string          `json:"id"`
	TurnID    string          `json:"turn_id,omitempty"`
	Tool      string          `json:"tool"`
	Input     json.RawMessage `json:"input,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	SourceRef SourceRef       `json:"source_ref,omitempty"`
}

type CanonicalUsage struct {
	Exact        bool    `json:"exact"`
	InputTokens  int64   `json:"input_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
}

type CanonicalKitsoki struct {
	Story        string                  `json:"story,omitempty"`
	Rooms        []string                `json:"rooms,omitempty"`
	Intents      []string                `json:"intents,omitempty"`
	Routes       []CanonicalRoute        `json:"routes,omitempty"`
	Gates        []map[string]any        `json:"gates,omitempty"`
	Exits        []string                `json:"exits,omitempty"`
	WorldChanges []map[string]any        `json:"world_changes,omitempty"`
	Events       []CanonicalKitsokiEvent `json:"events,omitempty"`
}

type CanonicalRoute struct {
	DecisionID string         `json:"decision_id,omitempty"`
	Input      string         `json:"input,omitempty"`
	RoutedBy   string         `json:"routed_by,omitempty"`
	Selected   map[string]any `json:"selected,omitempty"`
	Final      map[string]any `json:"final,omitempty"`
	Feedback   string         `json:"feedback,omitempty"`
	SourceRef  SourceRef      `json:"source_ref,omitempty"`
}

type CanonicalKitsokiEvent struct {
	Turn      int64           `json:"turn"`
	Seq       int             `json:"seq"`
	Kind      string          `json:"kind"`
	StatePath string          `json:"state_path,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	SourceRef SourceRef       `json:"source_ref,omitempty"`
}

// SessionSource is implemented by backend adapters that can discover and load
// sessions into the canonical envelope.
type SessionSource interface {
	ID() SourceID
	Discover(ctx context.Context, scope SourceScope) ([]SessionRef, error)
	Load(ctx context.Context, ref SessionRef) (CanonicalSession, error)
}

// DirJSONLSource is a small source adapter for backends that expose one JSONL
// session per file. For SourceKitsokiTrace it parses kitsoki event rows into
// the canonical Kitsoki block; for SourceImportedJSONL it expects each file to
// already contain a CanonicalSession JSON object.
type DirJSONLSource struct {
	Backend SourceID
	Dirs    []string
}

func (s DirJSONLSource) ID() SourceID {
	if s.Backend == "" {
		return SourceImportedJSONL
	}
	return s.Backend
}

func (s DirJSONLSource) Discover(_ context.Context, scope SourceScope) ([]SessionRef, error) {
	dirs := append([]string{}, s.Dirs...)
	dirs = append(dirs, scope.Dirs...)
	return discoverJSONLFiles(s.ID(), dirs, scope.SinceMTime)
}

func (s DirJSONLSource) Load(_ context.Context, ref SessionRef) (CanonicalSession, error) {
	switch s.ID() {
	case SourceKitsokiTrace:
		return loadKitsokiTraceCanonical(s.ID(), ref)
	default:
		b, err := os.ReadFile(ref.Path)
		if err != nil {
			return CanonicalSession{}, fmt.Errorf("mining: read canonical session %s: %w", ref.Path, err)
		}
		var sess CanonicalSession
		if err := json.Unmarshal(b, &sess); err != nil {
			return CanonicalSession{}, fmt.Errorf("mining: parse canonical session %s: %w", ref.Path, err)
		}
		if sess.SchemaVersion == "" {
			sess.SchemaVersion = "session-corpus.v1"
		}
		if sess.Source.Backend == "" {
			sess.Source = CanonicalSource{Backend: s.ID(), Ref: ref.ID, Path: ref.Path, MTime: ref.MTime}
		}
		return sess, nil
	}
}

// TranscriptJSONLSource adapts Claude Code and Codex-style JSONL transcripts
// into the canonical envelope. It intentionally extracts only durable evidence
// primitives: ordered messages and tool-use blocks with source refs. Backend
// quirks can grow behind this adapter without changing downstream drivers.
type TranscriptJSONLSource struct {
	Backend SourceID
	Dirs    []string
}

func (s TranscriptJSONLSource) ID() SourceID {
	if s.Backend == "" {
		return SourceClaudeCode
	}
	return s.Backend
}

func (s TranscriptJSONLSource) Discover(_ context.Context, scope SourceScope) ([]SessionRef, error) {
	dirs := append([]string{}, s.Dirs...)
	dirs = append(dirs, scope.Dirs...)
	return discoverJSONLFiles(s.ID(), dirs, scope.SinceMTime)
}

func (s TranscriptJSONLSource) Load(_ context.Context, ref SessionRef) (CanonicalSession, error) {
	f, err := os.Open(ref.Path)
	if err != nil {
		return CanonicalSession{}, fmt.Errorf("mining: open transcript %s: %w", ref.Path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var turns []CanonicalTurn
	var tools []CanonicalToolCall
	line := 0
	for scanner.Scan() {
		line++
		raw := append([]byte(nil), scanner.Bytes()...)
		var rec map[string]any
		if err := json.Unmarshal(raw, &rec); err != nil {
			return CanonicalSession{}, fmt.Errorf("mining: parse transcript %s line %d: %w", ref.Path, line, err)
		}
		role := firstNonEmptyString(stringValue(rec["role"]), stringValue(rec["type"]))
		msg := mapAny(rec["message"])
		if role == "" {
			role = stringValue(msg["role"])
		}
		src := SourceRef{Backend: s.ID(), Path: ref.Path, Line: line}
		texts, calls := extractTranscriptContent(firstNonEmptyAny(rec["content"], msg["content"]))
		if len(texts) > 0 {
			turns = append(turns, CanonicalTurn{
				ID: fmt.Sprintf("line-%d", line), Role: role, Text: strings.Join(texts, "\n"),
				SourceRef: src, Attrs: json.RawMessage(raw),
			})
		}
		for i, call := range calls {
			input, _ := json.Marshal(call.Input)
			tools = append(tools, CanonicalToolCall{
				ID: fmt.Sprintf("line-%d-tool-%d", line, i), TurnID: fmt.Sprintf("line-%d", line),
				Tool: call.Name, Input: input, SourceRef: src,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return CanonicalSession{}, fmt.Errorf("mining: scan transcript %s: %w", ref.Path, err)
	}
	return CanonicalSession{
		SchemaVersion: "session-corpus.v1",
		Source:        CanonicalSource{Backend: s.ID(), Ref: ref.ID, Path: ref.Path, MTime: ref.MTime},
		Session:       CanonicalMetadata{ID: ref.ID, Entrypoint: "human"},
		Turns:         turns, ToolCalls: tools,
	}, nil
}

type transcriptToolCall struct {
	Name  string
	Input any
}

func loadKitsokiTraceCanonical(source SourceID, ref SessionRef) (CanonicalSession, error) {
	events, err := readTraceEvents(ref.Path, source)
	if err != nil {
		return CanonicalSession{}, err
	}
	rooms := map[string]struct{}{}
	intents := map[string]struct{}{}
	var turns []CanonicalTurn
	var routes []CanonicalRoute
	var gates []map[string]any
	var worldChanges []map[string]any
	var exits []string
	var eventRows []CanonicalKitsokiEvent
	for idx, ev := range events {
		if ev.StatePath != "" {
			rooms[string(ev.StatePath)] = struct{}{}
		}
		payload := payloadMap(ev.Payload)
		src := SourceRef{Backend: source, Path: ref.Path, Line: idx + 2, Event: idx}
		eventRows = append(eventRows, CanonicalKitsokiEvent{
			Turn: int64(ev.Turn), Seq: ev.Seq, Kind: string(ev.Kind),
			StatePath: string(ev.StatePath), Payload: ev.Payload, SourceRef: src,
		})
		switch ev.Kind {
		case store.TurnStarted, store.UserInputReceived:
			text := stringValue(payload["input"])
			if text != "" {
				turns = append(turns, CanonicalTurn{
					ID:   fmt.Sprintf("turn-%d-%d", ev.Turn, ev.Seq),
					Role: "user", Text: text, SourceRef: src, Attrs: ev.Payload,
				})
			}
			if ev.Kind == store.TurnStarted && stringValue(payload["routed_by"]) != "" {
				intent := stringValue(payload["intent"])
				if intent == "" {
					intent = stringValue(payload["selected_intent"])
				}
				if intent != "" {
					intents[intent] = struct{}{}
				}
				routes = append(routes, CanonicalRoute{
					DecisionID: stringValue(payload["decision_id"]),
					Input:      text,
					RoutedBy:   stringValue(payload["routed_by"]),
					Selected:   map[string]any{"intent": intent},
					SourceRef:  src,
				})
			}
		case store.TransitionApplied, store.IntentAccepted:
			if intent := stringValue(payload["intent"]); intent != "" {
				intents[intent] = struct{}{}
			}
		case store.GateDecided:
			gates = append(gates, payload)
		case store.EffectApplied:
			worldChanges = append(worldChanges, payload)
		case store.TurnEnded:
			if to := stringValue(payload["to"]); to != "" {
				exits = append(exits, to)
			}
		}
	}
	return CanonicalSession{
		SchemaVersion: "session-corpus.v1",
		Source:        CanonicalSource{Backend: source, Ref: ref.ID, Path: ref.Path, MTime: ref.MTime},
		Session:       CanonicalMetadata{ID: ref.ID, Entrypoint: "human"},
		Turns:         turns,
		Kitsoki: &CanonicalKitsoki{
			Rooms: sortedKeys(rooms), Intents: sortedKeys(intents), Routes: routes,
			Gates: gates, Exits: exits, WorldChanges: worldChanges, Events: eventRows,
		},
	}, nil
}

func readTraceEvents(path string, source SourceID) (store.History, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mining: open trace %s: %w", path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var hist store.History
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Bytes()
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("mining: parse trace %s line %d: %w", path, line, err)
		}
		if probe.Kind == "session.header" {
			continue
		}
		var ev store.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("mining: parse trace event %s line %d: %w", path, line, err)
		}
		hist = append(hist, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mining: scan trace %s: %w", path, err)
	}
	_ = source
	return hist, nil
}

func discoverJSONLFiles(source SourceID, dirs []string, sinceMTime int64) ([]SessionRef, error) {
	var refs []SessionRef
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("mining: discover %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				return nil, fmt.Errorf("mining: stat %s: %w", path, err)
			}
			mtime := info.ModTime().Unix()
			if sinceMTime > 0 && mtime <= sinceMTime {
				continue
			}
			refs = append(refs, SessionRef{
				Source: source,
				ID:     strings.TrimSuffix(entry.Name(), ".jsonl"),
				Path:   path,
				MTime:  mtime,
			})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].MTime == refs[j].MTime {
			return refs[i].Path < refs[j].Path
		}
		return refs[i].MTime < refs[j].MTime
	})
	return refs, nil
}

func extractTranscriptContent(v any) ([]string, []transcriptToolCall) {
	switch x := v.(type) {
	case string:
		if x == "" {
			return nil, nil
		}
		return []string{x}, nil
	case []any:
		var texts []string
		var calls []transcriptToolCall
		for _, item := range x {
			m := mapAny(item)
			switch stringValue(m["type"]) {
			case "text":
				if text := stringValue(m["text"]); text != "" {
					texts = append(texts, text)
				}
			case "tool_use", "tool_call":
				name := firstNonEmptyString(stringValue(m["name"]), stringValue(m["tool"]))
				if name != "" {
					calls = append(calls, transcriptToolCall{Name: name, Input: firstNonEmptyAny(m["input"], m["arguments"])})
				}
			case "tool_result":
				if text := stringValue(m["content"]); text != "" {
					texts = append(texts, text)
				}
			default:
				if text := firstNonEmptyString(stringValue(m["text"]), stringValue(m["content"])); text != "" {
					texts = append(texts, text)
				}
			}
		}
		return texts, calls
	case map[string]any:
		return extractTranscriptContent(firstNonEmptyAny(x["content"], x["text"]))
	default:
		return nil, nil
	}
}

func payloadMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return ""
	}
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptyAny(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func mapAny(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
