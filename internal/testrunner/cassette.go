package testrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/host"
	"kitsoki/internal/journal"
	"kitsoki/internal/store"
)

// Cassette is one host-cassette file (kind: host_cassette).
type Cassette struct {
	Kind        string            `yaml:"kind"`
	AppID       string            `yaml:"app_id"`
	AppVersion  string            `yaml:"app_version,omitempty"`
	SourceRun   string            `yaml:"source_run,omitempty"`
	GeneratedAt string            `yaml:"generated_at,omitempty"`
	MatchOn     []string          `yaml:"match_on,omitempty"`
	RecordMode  string            `yaml:"record_mode,omitempty"`
	PhaseFrom   string            `yaml:"phase_from,omitempty"`
	Episodes    []CassetteEpisode `yaml:"episodes"`

	path       string
	phaseRegex *regexp.Regexp
	mu         sync.Mutex

	// episodeMatchCounts tracks the number of times each episode has been
	// matched so far (keyed by episode ID). For replay:any episodes this
	// is the running matchIdx counter; for normal episodes it is capped at 1.
	// Protected by mu. Seeded from prior trace history via SeedMatchCountsFromHistory
	// so post-resume matches produce collision-free call_ids.
	episodeMatchCounts map[string]int
}

// CassetteEpisode is one episode entry in a cassette.
type CassetteEpisode struct {
	ID       string           `yaml:"id"`
	Match    map[string]any   `yaml:"match"`
	Response CassetteResponse `yaml:"response,omitempty"`
	Delay    string           `yaml:"delay,omitempty"`
	Replay   string           `yaml:"replay,omitempty"`
	Agent    *EpisodeAgent    `yaml:"agent,omitempty"` // present for host.agent.* episodes

	played bool
}

// EpisodeAgent mirrors journal.AgentCallBody. Present when the episode was
// recorded against a host.agent.* handler. On replay the dispatcher writes a
// KindAgentCall journal entry from this block. On record the dispatcher
// populates this block from the KindAgentCall entry the handler just wrote.
//
// All fields map 1:1 to AgentCallBody. Long prompts and responses may live in
// sidecar files referenced by !include (the preprocessor handles them before
// unmarshaling). The call_id field is advisory at load time — it is always
// recomputed as sha256("agent-call:" + appID + ":" + episodeID)[:16] so that
// re-records are byte-stable and hand-edits to the episode id roll the call_id
// forward automatically.
//
// Input is typed as `any` rather than json.RawMessage because goccy/go-yaml
// cannot deserialize YAML flow mappings into []byte. The dispatcher marshals
// it to json.RawMessage before writing journal entries.
type EpisodeAgent struct {
	Verb           string  `yaml:"verb"` // ask | decide | extract | task | converse
	Agent          string  `yaml:"agent,omitempty"`
	Model          string  `yaml:"model,omitempty"`
	Turn           int64   `yaml:"turn,omitempty"`
	DurationMs     int64   `yaml:"duration_ms,omitempty"`
	PromptTokens   int     `yaml:"prompt_tokens,omitempty"`
	ResponseTokens int     `yaml:"response_tokens,omitempty"`
	CostUSD        float64 `yaml:"cost_usd,omitempty"`
	SystemPrompt   string  `yaml:"system_prompt,omitempty"`
	Prompt         string  `yaml:"prompt,omitempty"`
	Input          any     `yaml:"input,omitempty"` // marshaled to json.RawMessage when written to journal
	Response       string  `yaml:"response,omitempty"`
	Error          string  `yaml:"error,omitempty"`
	CallID         string  `yaml:"call_id,omitempty"` // advisory; recomputed on every load

	// Transcript, when present, is the recorded agent-action transcript for this
	// agent call (the claude stream-json / openai-chat events). On replay it is
	// written to the <call_id>.jsonl (+ .timings) sidecar verbatim via the
	// TranscriptWriter in ctx and referenced from agent.call.complete by a
	// transcript_ref pointer — NO live tool ever runs, and the sidecar is
	// byte-identical to the recorded events. In record mode (new_episodes) the
	// live captured transcript is folded in here. Mirrors the
	// PromptTokens/CostUSD record-and-replay-verbatim flow. Optional and additive:
	// a code path that does not read transcript: ignores it entirely, so existing
	// cassettes (and the trace-features-video spec that replays this one) are
	// unaffected. See docs/tracing/cassettes.md (Recorded agent-action transcripts).
	Transcript *EpisodeTranscript `yaml:"transcript,omitempty"`
}

// EpisodeTranscript is one agent call's recorded agent-action transcript inside
// a cassette episode. It is the cassette-side mirror of host.TranscriptRef +
// the sidecar contents: Format names the event schema, Events are the verbatim
// backend-native events (one per sidecar line), and Timings are the optional
// per-event capture-time offsets (ms since call start) for the waterfall.
//
// Events accepts either form an author finds convenient and both replay to a
// byte-identical sidecar after canonicalization:
//   - a list of JSON strings (one verbatim event line each), or
//   - a list of YAML/JSON mappings (objects), marshaled compactly to one line.
type EpisodeTranscript struct {
	Format  string  `yaml:"format"`
	Events  []any   `yaml:"events"`
	Timings []int64 `yaml:"timings,omitempty"`
}

// eventLines converts the authored Events into verbatim JSON byte lines, one per
// event, suitable for the TranscriptWriter.
//
// A string element is an already-serialized event line and is preserved
// BYTE-VERBATIM (key order, number literals like 49.0, incidental spacing). This
// is the load-bearing determinism invariant: the live claude tee writes its raw
// stream-json bytes unchanged (transcript_writer.go Finalize), and foldLiveTranscript
// stores those exact lines as string elements — so a live-captured-then-folded
// transcript MUST replay byte-identical to the original sidecar. Re-marshaling
// here would re-sort keys and reformat numbers and break that contract.
//
// A mapping element (a YAML/JSON object authored for convenience, never produced
// by live capture) is marshaled compactly; no byte-identity contract applies to
// it because there is no live counterpart. Elements that fail to parse/marshal
// are skipped (best-effort) — LoadCassette validates string events up front
// (validateEpisodeTranscripts) so a fat-fingered recorded line fails fast there
// rather than silently shortening the sidecar.
func (t *EpisodeTranscript) eventLines() []json.RawMessage {
	if t == nil {
		return nil
	}
	out := make([]json.RawMessage, 0, len(t.Events))
	for _, ev := range t.Events {
		switch e := ev.(type) {
		case string:
			if !json.Valid([]byte(e)) {
				continue
			}
			// Preserve the authored bytes verbatim — do NOT re-marshal.
			line := make(json.RawMessage, len(e))
			copy(line, e)
			out = append(out, line)
		default:
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			out = append(out, json.RawMessage(b))
		}
	}
	return out
}

// validateEpisodeTranscripts checks that every string-form event in every
// episode's recorded transcript is valid JSON, so a malformed recorded line
// fails at load rather than silently dropping from the replayed sidecar (which
// would make transcript_ref.events under-count the authored events). Mapping-form
// events are skipped — they are re-marshaled at replay and cannot be malformed.
func validateEpisodeTranscripts(cas *Cassette) error {
	for _, ep := range cas.Episodes {
		if ep.Agent == nil || ep.Agent.Transcript == nil {
			continue
		}
		for i, ev := range ep.Agent.Transcript.Events {
			s, ok := ev.(string)
			if !ok {
				continue
			}
			if !json.Valid([]byte(s)) {
				return fmt.Errorf("episode %q: transcript event[%d] is not valid JSON: %q", ep.ID, i, s)
			}
		}
	}
	return nil
}

// CassetteResponse is the canned response for an episode.
type CassetteResponse struct {
	Data       map[string]any `yaml:"data,omitempty"`
	Error      string         `yaml:"error,omitempty"`
	InfraError string         `yaml:"infra_error,omitempty"`
}

// UnmatchedEpisodes returns the IDs of every episode that was never played
// at least once. Episodes with replay: any that were matched at least once
// are NOT considered unmatched — only episodes with a zero play count are
// returned. The slice is ordered by episode position in the cassette.
//
// Callers use this to detect phantom / orphan episodes after a complete flow
// run: any unmatched episode indicates either a cassette mismatch or a flow
// fixture that did not exercise all expected paths.
func (c *Cassette) UnmatchedEpisodes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for _, ep := range c.Episodes {
		if !ep.played {
			ids = append(ids, ep.ID)
		}
	}
	return ids
}

// PlayedEpisodes returns the IDs of every episode that was consumed (matched)
// at least once during this run — the complement of UnmatchedEpisodes, in the
// same episode-position order. replay: any episodes count as played after
// their first match, exactly as they do for orphan accounting.
//
// Play counts are deliberately NOT returned: the internal match counter
// (episodeMatchCounts) is seeded from prior trace history via
// SeedMatchCountsFromHistory so post-resume call_ids stay collision-free,
// which makes it a cross-run counter rather than an in-run play count. The
// per-episode played flag is the only faithful per-run signal, so this
// accessor exposes exactly that.
func (c *Cassette) PlayedEpisodes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for _, ep := range c.Episodes {
		if ep.played {
			ids = append(ids, ep.ID)
		}
	}
	return ids
}

// ErrCassetteMiss is returned when no episode matches a handler call.
type ErrCassetteMiss struct {
	Handler           string
	Args              map[string]any
	AvailableEpisodes []string
}

func (e *ErrCassetteMiss) Error() string {
	return fmt.Sprintf("cassette miss: no episode matched handler=%q args=%v; available episodes: %v",
		e.Handler, e.Args, e.AvailableEpisodes)
}

// LoadCassette reads and parses the YAML cassette at path. It resolves
// !include directives (paths relative to the cassette file) before
// unmarshaling. Returns an error if kind != "host_cassette".
func LoadCassette(path string) (*Cassette, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("cassette: abs path %q: %w", path, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("cassette: read %q: %w", abs, err)
	}

	baseDir := filepath.Dir(abs)
	resolved, err := resolveIncludes(data, baseDir)
	if err != nil {
		return nil, fmt.Errorf("cassette: resolve !include in %q: %w", abs, err)
	}

	var cas Cassette
	if err := goyaml.Unmarshal(resolved, &cas); err != nil {
		return nil, fmt.Errorf("cassette: parse %q: %w", abs, err)
	}

	if cas.Kind != "host_cassette" {
		return nil, fmt.Errorf("cassette: %q: kind must be \"host_cassette\", got %q", abs, cas.Kind)
	}

	switch cas.RecordMode {
	case "", "none", "new_episodes":
		// ok
	default:
		return nil, fmt.Errorf("cassette: %q: record_mode %q is not supported; valid values are \"none\" or \"new_episodes\"", abs, cas.RecordMode)
	}

	if cas.PhaseFrom != "" {
		re, reErr := regexp.Compile(cas.PhaseFrom)
		if reErr != nil {
			return nil, fmt.Errorf("cassette: %q: phase_from regex: %w", abs, reErr)
		}
		cas.phaseRegex = re
	}

	// replay:any + agent: was previously forbidden because re-invoking the same
	// episode would produce duplicate journal rows in the SQLite agent journal.
	// With agent events written to the JSONL event sink, each match produces a
	// distinct AgentCalled/AgentReturned pair with a unique call_id (different
	// matchIdx) and the same episode_id. The constraint is lifted: replay:any +
	// agent: is now legal and means "this agent exchange is replayable N times,
	// each producing a fresh event pair." See docs/architecture/agent-plugin.md
	// (cassette agent, call_id derivation) for the recording format.
	_ = cas.Episodes // no validation against replay:any + agent: any more

	// Fail fast on a malformed recorded transcript event. eventLines() skips
	// unparseable elements at replay time; without this check a fat-fingered
	// string event would silently shorten the sidecar and desync transcript_ref.
	if err := validateEpisodeTranscripts(&cas); err != nil {
		return nil, fmt.Errorf("cassette: %q: %w", abs, err)
	}

	cas.path = abs
	return &cas, nil
}

// resolveIncludes scans the YAML bytes for !include <path> tags and replaces
// them inline with the file's content (resolved relative to baseDir). Uses a
// simple line-by-line pre-pass because goccy/go-yaml does not natively expand
// custom YAML tags before unmarshaling into typed structs.
//
// Recognised form (anywhere a YAML value appears):
//
//	someKey: !include relative/path.json
//	someKey: !include relative/path.txt
//
// For .json files: the content is parsed and re-serialised as a compact JSON
// literal (single line, YAML-safe). For other file types (e.g. .txt): the
// content is serialised as a YAML double-quoted string so it can be inlined.
//
// Only the value side of a mapping entry is replaced; block-scalars and
// anchors using !include are not supported.
func resolveIncludes(data []byte, baseDir string) ([]byte, error) {
	// Match: optional whitespace + !include + whitespace + path (rest of line).
	tagRe := regexp.MustCompile(`^(\s*)([^:]+:\s*)!include\s+(.+?)\s*$`)
	lines := strings.Split(string(data), "\n")
	var out []string
	for _, line := range lines {
		m := tagRe.FindStringSubmatch(line)
		if m == nil {
			out = append(out, line)
			continue
		}
		prefix := m[1] + m[2]
		rawIncPath := strings.TrimSpace(m[3])

		// Reject absolute paths unconditionally.
		if filepath.IsAbs(rawIncPath) {
			return nil, fmt.Errorf("!include %q: absolute paths are not allowed", rawIncPath)
		}

		// Resolve relative to baseDir, then verify the resolved path stays within baseDir.
		incPath := filepath.Join(baseDir, rawIncPath)

		// Canonicalise BOTH paths with EvalSymlinks before the containment check.
		// baseDir is resolved in case it (or a parent) is a symlink — e.g. on
		// macOS the temp root /var/folders/... is itself a symlink to
		// /private/var/folders/... Crucially incPath must be resolved the SAME
		// way, or canonBase (resolved) and canonInc (unresolved) never share a
		// prefix and every include is falsely rejected as "outside" on macOS.
		// Resolving incPath also HARDENS the check: a symlink planted inside the
		// cassette dir that points outward resolves to its real target here and
		// is correctly rejected, whereas a plain Clean would let the later
		// ReadFile follow it and escape.
		canonBase, evalErr := filepath.EvalSymlinks(baseDir)
		if evalErr != nil {
			// baseDir might not exist yet in some edge cases; fall back to clean path.
			canonBase = filepath.Clean(baseDir)
		}
		canonInc, incEvalErr := filepath.EvalSymlinks(incPath)
		if incEvalErr != nil {
			// incPath itself may not exist yet; resolve its existing parent dir
			// (which shares baseDir's symlink resolution) and re-join the leaf so
			// the comparison still uses the same canonical root as canonBase.
			if parent, perr := filepath.EvalSymlinks(filepath.Dir(incPath)); perr == nil {
				canonInc = filepath.Join(parent, filepath.Base(incPath))
			} else {
				canonInc = filepath.Clean(incPath)
			}
		}
		rel, relErr := filepath.Rel(canonBase, canonInc)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("!include %q: path resolves outside the cassette directory", rawIncPath)
		}

		raw, readErr := os.ReadFile(incPath)
		if readErr != nil {
			return nil, fmt.Errorf("!include %q: %w", incPath, readErr)
		}

		var inlined string
		if strings.HasSuffix(strings.ToLower(incPath), ".json") {
			// JSON file: compact and inline as a JSON literal.
			var v any
			if jsonErr := json.Unmarshal(raw, &v); jsonErr != nil {
				return nil, fmt.Errorf("!include %q: parse JSON: %w", incPath, jsonErr)
			}
			compact, marshalErr := json.Marshal(v)
			if marshalErr != nil {
				return nil, fmt.Errorf("!include %q: re-marshal JSON: %w", incPath, marshalErr)
			}
			inlined = string(compact)
		} else {
			// Text file (e.g. .txt): inline as a YAML double-quoted string.
			// json.Marshal on a string produces a JSON string literal which is
			// also valid YAML double-quoted scalar syntax.
			quoted, marshalErr := json.Marshal(string(raw))
			if marshalErr != nil {
				return nil, fmt.Errorf("!include %q: marshal text as string: %w", incPath, marshalErr)
			}
			inlined = string(quoted)
		}

		out = append(out, prefix+inlined)
	}
	return []byte(strings.Join(out, "\n")), nil
}

// phaseFromStatePath extracts the "phase" synthetic field from the orchestrator
// state path. Uses the cassette's PhaseFrom regex (first capture group) when
// set; otherwise uses the first dot-separated segment of statePath.
func (c *Cassette) phaseFromStatePath(statePath string) string {
	if c.phaseRegex != nil {
		sub := c.phaseRegex.FindStringSubmatch(statePath)
		if len(sub) >= 2 {
			return sub[1]
		}
		return ""
	}
	if idx := strings.IndexByte(statePath, '.'); idx >= 0 {
		return statePath[:idx]
	}
	return statePath
}

// episodeIDs returns the IDs of the provided episode slice for error messages.
func episodeIDs(eps []*CassetteEpisode) []string {
	ids := make([]string, len(eps))
	for i, e := range eps {
		ids[i] = e.ID
	}
	return ids
}

// SeedMatchCountsFromHistory initialises the per-episode match counters from a
// prior trace history. It scans AgentCalled events that carry an episode_id
// field (written by writeCassetteAgentEvents) and sets each episode's counter
// to max(match_idx)+1 so that the first post-resume match produces a fresh
// matchIdx that does not collide with any pre-resume call_id.
//
// This must be called before the cassette dispatcher processes any events.
// Callers that hold a prior store.History (e.g. after reloading a JSONL trace)
// should call this once immediately after LoadCassette.
func (c *Cassette) SeedMatchCountsFromHistory(hist store.History) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.episodeMatchCounts == nil {
		c.episodeMatchCounts = make(map[string]int)
	}
	for _, ev := range hist {
		if ev.Kind != store.AgentCalled || ev.EpisodeID == "" {
			continue
		}
		// match_idx is the index of this match; next must be at least match_idx+1.
		next := ev.MatchIdx + 1
		if next > c.episodeMatchCounts[ev.EpisodeID] {
			c.episodeMatchCounts[ev.EpisodeID] = next
		}
	}
}

// MatchEpisode finds the first unplayed episode that matches (handler, args,
// statePath). Returns ErrCassetteMiss when no episode matches.
func MatchEpisode(handler string, args map[string]any, statePath string, cas *Cassette) (*CassetteEpisode, error) {
	phase := cas.phaseFromStatePath(statePath)

	// Compute schema_name synthetic field.
	schemaName := ""
	if s, ok := args["schema"].(string); ok {
		schemaName = filepath.Base(s)
	}

	ptrs := make([]*CassetteEpisode, len(cas.Episodes))
	for i := range cas.Episodes {
		ptrs[i] = &cas.Episodes[i]
	}

	for _, ep := range ptrs {
		if ep.played && ep.Replay != "any" {
			continue
		}
		if !episodeMatches(ep, handler, args, phase, schemaName) {
			continue
		}
		return ep, nil
	}

	return nil, &ErrCassetteMiss{
		Handler:           handler,
		Args:              args,
		AvailableEpisodes: episodeIDs(ptrs),
	}
}

// episodeMatches checks whether a single episode's match map matches the call.
func episodeMatches(ep *CassetteEpisode, handler string, args map[string]any, phase, schemaName string) bool {
	for k, want := range ep.Match {
		var got any
		switch k {
		case "handler":
			got = handler
		case "phase":
			got = phase
		case "schema_name":
			got = schemaName
		default:
			got = args[k]
		}
		if !matchValue(got, want) {
			return false
		}
	}
	return true
}

// matchValue compares a field value against the expected match pattern.
// Uses deep equality after JSON-normalising both sides so that int/float
// comparisons from YAML work against string-typed args and vice-versa.
func matchValue(got, want any) bool {
	// Fast path: direct equality, but ONLY for comparable dynamic types.
	// `got == want` panics ("comparing uncomparable type") when either side is
	// a slice or map (e.g. matching host.run's `args` list), so guard it — the
	// JSON-normalised path below handles those cases correctly.
	if isComparable(got) && isComparable(want) && got == want {
		return true
	}
	// JSON-normalise both sides so numeric types compare correctly.
	gj, err1 := json.Marshal(got)
	wj, err2 := json.Marshal(want)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(gj) == string(wj)
}

// isComparable reports whether v's dynamic type is comparable with ==. Slices,
// maps, and funcs are not, and comparing them with == panics at runtime.
func isComparable(v any) bool {
	if v == nil {
		return true
	}
	return reflect.TypeOf(v).Comparable()
}

// CassetteDispatcherOpts carries optional dependencies for BuildCassetteDispatcher.
// All fields are optional; nil values are safe no-ops.
type CassetteDispatcherOpts struct {
	// JournalWriter, when non-nil, is used on replay to write KindAgentCall
	// journal entries for episodes that carry an agent: block (Phase 2). It is
	// also used on record to read back the KindAgentCall the live handler wrote
	// and capture it into the synthesised episode's agent: block (Phase 3).
	JournalWriter journal.Writer

	// JournalDB, when non-nil, is the *sql.DB backing the in-memory journal so
	// the dispatcher can query KindAgentCall entries by call_id after a live
	// handler returns in record mode (Phase 3).
	JournalDB interface {
		Query(query string, args ...any) (interface {
			Next() bool
			Scan(dest ...any) error
			Err() error
			Close() error
		}, error)
	}

	// SessionID is needed to write journal entries on replay. The dispatcher
	// reads it from the agent context in ctx when empty.
	SessionID app.SessionID

	// EventSink, when non-nil, receives AgentCalled / AgentReturned /
	// AgentError events on replay (wave 3-agent parallel write). This is the
	// JSONL-side write alongside the existing journal write.
	EventSink store.EventSink
}

// AgentJournalLookup is the function type used by the cassette dispatcher in
// record mode to retrieve the KindAgentCall journal entry the live handler
// just wrote. ctx carries the agent call context (session, turn), verb is the
// agent verb derived from the handler name (e.g. "task" from "host.agent.task").
// Returns (nil, false) when no entry is found.
type AgentJournalLookup func(ctx context.Context, verb string) (*host.AgentCallBody, bool)

// BuildCassetteDispatcher returns a host.Handler closure that the testrunner
// installs under every handler name referenced by the cassette's episodes.
// stateOf is called per-invocation to read the orchestrator's current StatePath.
// fallback is dispatched only when cassette record mode is enabled; replay-mode
// misses always return ErrCassetteMiss so deterministic runs fail closed instead
// of silently calling live handlers. recordSink is called with synthesised
// episodes when KITSOKI_CASSETTE_RECORD is active.
func BuildCassetteDispatcher(
	cas *Cassette,
	handlerName string,
	stateOf func() string,
	fallback host.Handler,
	recordSink func(ep *CassetteEpisode),
	clk clock.Clock,
) host.Handler {
	return buildCassetteDispatcherFull(cas, handlerName, stateOf, fallback, recordSink, clk, nil, nil, nil)
}

// BuildCassetteDispatcherWithJournal is the journal-aware variant of
// BuildCassetteDispatcher. jw is the journal writer for the rig's in-memory
// store; journalLookup reads the KindAgentCall entry the live handler just
// wrote, identified by ctx + verb (derived from handlerName). Returns (nil, false)
// when no entry is found.
func BuildCassetteDispatcherWithJournal(
	cas *Cassette,
	handlerName string,
	stateOf func() string,
	fallback host.Handler,
	recordSink func(ep *CassetteEpisode),
	clk clock.Clock,
	jw journal.Writer,
	journalLookup AgentJournalLookup,
) host.Handler {
	return buildCassetteDispatcherFull(cas, handlerName, stateOf, fallback, recordSink, clk, jw, journalLookup, nil)
}

// BuildCassetteDispatcherWithJournalAndSink combines journal and event sink support.
// Both jw and sink are optional; if both are provided, agent events are written
// to the sink during cassette replay.
func BuildCassetteDispatcherWithJournalAndSink(
	cas *Cassette,
	handlerName string,
	stateOf func() string,
	fallback host.Handler,
	recordSink func(ep *CassetteEpisode),
	clk clock.Clock,
	jw journal.Writer,
	journalLookup AgentJournalLookup,
	sink store.EventSink,
) host.Handler {
	return buildCassetteDispatcherFull(cas, handlerName, stateOf, fallback, recordSink, clk, jw, journalLookup, sink)
}

// BuildCassetteDispatcherWithSink is the EventSink-aware variant of
// BuildCassetteDispatcher. sink receives AgentCalled / AgentReturned /
// AgentError events on replay (wave 3-agent parallel write). When priorHist
// is non-nil, SeedMatchCountsFromHistory is called first so that post-resume
// matches produce collision-free call_ids.
func BuildCassetteDispatcherWithSink(
	cas *Cassette,
	handlerName string,
	stateOf func() string,
	fallback host.Handler,
	recordSink func(ep *CassetteEpisode),
	clk clock.Clock,
	sink store.EventSink,
	priorHist store.History,
) host.Handler {
	if priorHist != nil {
		cas.SeedMatchCountsFromHistory(priorHist)
	}
	return buildCassetteDispatcherFull(cas, handlerName, stateOf, fallback, recordSink, clk, nil, nil, sink)
}

func buildCassetteDispatcherFull(
	cas *Cassette,
	handlerName string,
	stateOf func() string,
	fallback host.Handler,
	recordSink func(ep *CassetteEpisode),
	clk clock.Clock,
	jw journal.Writer,
	journalLookup AgentJournalLookup,
	eventSink store.EventSink,
) host.Handler {
	return func(ctx context.Context, args map[string]any) (host.Result, error) {
		statePath := stateOf()

		cas.mu.Lock()
		ep, err := MatchEpisode(handlerName, args, statePath, cas)
		if err == nil {
			// Mark played. replay:any episodes are marked played on first match
			// so UnmatchedEpisodes doesn't count them as orphans, while still
			// being re-matchable (MatchEpisode skips only when played && not any).
			ep.played = true
			// Allocate and advance the per-episode match counter atomically under mu.
			// This is the matchIdx used in call_id derivation: for replay:any
			// episodes each match gets a distinct idx; normal episodes always get 0.
			if cas.episodeMatchCounts == nil {
				cas.episodeMatchCounts = make(map[string]int)
			}
			matchIdx := cas.episodeMatchCounts[ep.ID]
			cas.episodeMatchCounts[ep.ID] = matchIdx + 1
			// Capture values before releasing lock.
			resp := ep.Response
			delay := ep.Delay
			agentBlock := ep.Agent
			epID := ep.ID
			cas.mu.Unlock()

			// Honor delay via the injected clock.
			if delay != "" {
				d, parseErr := app.ParseDuration(delay)
				if parseErr == nil && d > 0 {
					clk.Sleep(d)
				}
			}

			// Wave 3-agent / B-4: write AgentCalled+AgentReturned to JSONL sink.
			// The SQLite journal write was removed in B-4 — cassette dispatch is
			// sink-only now. B-5 will delete agent_journal.go entirely.
			// matchIdx is threaded through so the emitted AgentCalled event carries
			// episode_id and match_idx; on post-resume reload these are used by
			// SeedMatchCountsFromHistory to restore the counter.
			if agentBlock != nil && eventSink != nil {
				writeCassetteAgentEvents(ctx, eventSink, cas, epID, matchIdx, agentBlock)
			}

			if resp.InfraError != "" {
				return host.Result{}, errors.New(resp.InfraError)
			}
			return host.Result{Data: resp.Data, Error: resp.Error}, nil
		}
		cas.mu.Unlock()

		// Miss path.
		var miss *ErrCassetteMiss
		if !errors.As(err, &miss) {
			return host.Result{}, err
		}

		mode := CassetteRecordMode(cas)

		if mode == "none" || mode == "" {
			callID, _ := args["call"].(string)
			slog.WarnContext(ctx, "cassette.miss.fail_closed",
				slog.String("handler", handlerName),
				slog.String("call", callID),
				slog.Any("available_episodes", miss.AvailableEpisodes),
			)
			return host.Result{}, miss
		}

		// Recording mode: delegate to fallback (must exist), capture, append.
		if fallback == nil {
			return host.Result{}, fmt.Errorf("cassette: record mode %q but no fallback handler for %q", mode, handlerName)
		}
		liveResult, liveErr := fallback(ctx, args)
		if liveErr != nil {
			return liveResult, liveErr
		}

		if recordSink != nil {
			synth := synthesiseEpisode(handlerName, args, statePath, cas, liveResult)

			// Phase 3: for host.agent.* handlers, read the KindAgentCall entry
			// the live handler just wrote and capture it in the episode's agent: block.
			// Derive the verb from the handler name (e.g. "task" from "host.agent.task").
			if strings.HasPrefix(handlerName, "host.agent.") && journalLookup != nil {
				verb := strings.TrimPrefix(handlerName, "host.agent.")
				if body, ok := journalLookup(ctx, verb); ok {
					// Compute the deterministic call_id for this episode.
					detCallID := host.DeriveCallID(cas.AppID, synth.ID)
					synth.Agent = agentBodyToEpisode(body, detCallID)
					synth.Agent.Turn = int64(host.AgentCallCtxFrom(ctx).Turn)
					// Phase 3 (agent-action-transcripts): fold the live captured
					// transcript+timings into the episode so a re-record carries the
					// agent's actions, and a subsequent replay reproduces the sidecar
					// verbatim. Best-effort: the live handler wrote it under its own
					// (live) call_id; we read that sidecar from ctx's transcripts dir.
					if et := foldLiveTranscript(ctx, body.CallID); et != nil {
						synth.Agent.Transcript = et
					}
				}
			}

			recordSink(synth)
		}
		return liveResult, nil
	}
}

// The SQLite agent journal (agent_journal.go) was deleted in B-5.
// AgentCalled / AgentReturned events flow to the JSONL EventSink only.
// BuildCassetteDispatcherWithJournal retains its jw parameter for API
// backwards compat; the journal write path is gone.

// writeCassetteAgentEvents writes an AgentCalled + AgentReturned (or
// AgentError) event pair to sink for a cassette episode replay (legacy dispatcher
// path; the cassetteAgent transport also uses this implicitly via Dispatch).
//
// call_id is derived deterministically as:
//
//	sha256("agent-call:" + appID + ":" + episodeID + ":" + matchIdx)[:16]
//
// using host.DeriveCallID. matchIdx is the 0-based match counter allocated by
// buildCassetteDispatcherFull atomically under cas.mu. For replay:any episodes
// each call gets a distinct matchIdx so the call_id differs per match even
// though the episode body is identical. episode_id and match_idx are
// written as top-level fields on the AgentCalled event so that post-resume
// SeedMatchCountsFromHistory can reconstruct the counters.
func writeCassetteAgentEvents(ctx context.Context, sink store.EventSink, cas *Cassette, epID string, matchIdx int, o *EpisodeAgent) {
	callID := host.DeriveCallID(cas.AppID, fmt.Sprintf("%s:%d", epID, matchIdx))
	oc := host.AgentCallCtxFrom(ctx)
	now := time.Now()

	// Build input raw from the agent block (best-effort).
	var inputRaw json.RawMessage
	if o.Input != nil {
		if b, merr := json.Marshal(o.Input); merr == nil {
			inputRaw = json.RawMessage(b)
		}
	}

	// AgentCalled: use now as dispatch time (cassette replay is instantaneous).
	// Guarantee a prompt reference on every agent.call.start:
	// large prompts spill to a sidecar file and
	// are referenced via PromptFile; small (or any non-offloaded) prompts are
	// embedded inline so a consumer never faces a missing reference. This mirrors
	// the live host.Dispatch path (internal/host/agent_dispatch.go) — the
	// cassette replay emitter must produce the same invariant as production.
	promptFile, _ := host.StorePromptIfLargeForTest(ctx, o.CallID, o.Prompt)
	inlinePrompt := ""
	if promptFile == "" {
		inlinePrompt = o.Prompt
	}
	// Stamp the live harness selection so a cassette-replayed agent event
	// reflects which profile/model the operator selected for this turn — the
	// same provenance the live path records (agent_event_sink.go). The active
	// profile's resolved model (selection override or profile default) supersedes
	// the cassette's static o.Model when present, so the trace matches the picker.
	model := o.Model
	profileName := host.ActiveProfileNameFromCtx(ctx)
	effort := ""
	if ap, ok := host.ActiveProfileFromContext(ctx); ok {
		if ap.Provider.Model != "" {
			model = ap.Provider.Model
		}
		effort = ap.Provider.Effort
	}
	calledPayload := host.AgentCalledPayload{
		Verb:       o.Verb,
		Agent:      o.Agent,
		Model:      model,
		Backend:    host.AgentBackendFromContext(ctx).Name(),
		Profile:    profileName,
		Effort:     effort,
		Prompt:     inlinePrompt,
		PromptFile: promptFile,
		Input:      inputRaw,
	}
	calledRaw, err := json.Marshal(calledPayload)
	if err == nil {
		calledEv := store.Event{
			Turn:      oc.Turn,
			Ts:        now,
			Kind:      store.AgentCalled,
			StatePath: oc.StatePath,
			Payload:   json.RawMessage(calledRaw),
			CallID:    callID,
			EpisodeID: epID,
			MatchIdx:  matchIdx,
		}
		_ = sink.Append(calledEv)
	}

	// AgentReturned or AgentError: use now (slightly after) as response time.
	returnedAt := time.Now()
	if o.Error != "" {
		errPayload := host.AgentErrorPayload{
			Verb:       o.Verb,
			Agent:      o.Agent,
			DurationMS: o.DurationMs,
			Error:      o.Error,
		}
		errRaw, merr := json.Marshal(errPayload)
		if merr == nil {
			errEv := store.Event{
				Turn:      oc.Turn,
				Ts:        returnedAt,
				Kind:      store.AgentError,
				StatePath: oc.StatePath,
				Payload:   json.RawMessage(errRaw),
				CallID:    callID,
			}
			_ = sink.Append(errEv)
		}
	} else {
		responseRaw := json.RawMessage(marshalAgentResponseString(o.Response))

		// Replay the recorded agent-action transcript: write the verbatim events
		// (+ timings) to the <call_id>.jsonl sidecar via the TranscriptWriter in
		// ctx and reference it from this agent.call.complete by a transcript_ref
		// pointer. No live tool runs; the sidecar is byte-identical to the record.
		// Nil/empty (no transcript: block) is a no-op, so legacy episodes are
		// unaffected. Mirrors the live host.Dispatch transcript_ref flow.
		var transcriptRef *host.TranscriptRef
		if o.Transcript != nil {
			transcriptRef = host.WriteReplayTranscript(ctx, callID,
				o.Transcript.Format, o.Transcript.eventLines(), o.Transcript.Timings)
		}

		// Surface recorded token usage + cost in the canonical opaque-meta shape
		// the live claude-CLI transport emits (meta.usage.{input,output}_tokens +
		// meta.cost_usd), so a cassette-replayed agent.call.complete carries the
		// same cost signal the runstatus usage meter aggregates as a real trace.
		// Without this the host-cassette replay path dropped usage entirely and
		// the meter under-reported the agent's spend as $0. Mirrors
		// cassetteAgent.Ask (cassette_agent.go).
		var meta map[string]any
		if o.PromptTokens > 0 || o.ResponseTokens > 0 || o.CostUSD > 0 {
			meta = map[string]any{}
			if o.PromptTokens > 0 || o.ResponseTokens > 0 {
				usage := map[string]any{}
				if o.PromptTokens > 0 {
					usage["input_tokens"] = o.PromptTokens
				}
				if o.ResponseTokens > 0 {
					usage["output_tokens"] = o.ResponseTokens
				}
				meta["usage"] = usage
			}
			if o.CostUSD > 0 {
				meta["cost_usd"] = o.CostUSD
			}
		}

		retPayload := host.AgentReturnedPayload{
			Verb:          o.Verb,
			Agent:         o.Agent,
			Model:         o.Model,
			DurationMS:    o.DurationMs,
			Response:      responseRaw,
			TranscriptRef: transcriptRef,
			Meta:          meta,
		}
		retRaw, merr := json.Marshal(retPayload)
		if merr == nil {
			retEv := store.Event{
				Turn:      oc.Turn,
				Ts:        returnedAt,
				Kind:      store.AgentReturned,
				StatePath: oc.StatePath,
				Payload:   json.RawMessage(retRaw),
				CallID:    callID,
			}
			_ = sink.Append(retEv)
		}
	}
}

// marshalAgentResponseString converts the response string from an
// EpisodeAgent to the JSON form expected in AgentCallBody.Response
// (a JSON-encoded string value). If the response is already valid JSON, it is
// returned as-is (allowing rich response objects); otherwise it is wrapped in a
// JSON string literal.
func marshalAgentResponseString(s string) []byte {
	if s == "" {
		return nil
	}
	// If the value is already a JSON object or array, pass it through.
	if len(s) > 0 && (s[0] == '{' || s[0] == '[') {
		return []byte(s)
	}
	// Plain text response: wrap as JSON string.
	b, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	return b
}

// foldLiveTranscript reads the live agent-action transcript sidecar the live
// agent handler just wrote (keyed by its live call_id) and returns it as an
// EpisodeTranscript for folding into a recorded episode. The sidecar lives in
// the run's transcripts/ dir, the sibling of the agent-prompts/ dir installed
// in ctx (see orchestrator host_dispatch). Returns nil when no prompts dir is in
// ctx, no sidecar exists for liveCallID, or it is empty — record mode then
// simply omits the transcript: block (best-effort; never aborts a recording).
//
// The .timings sidecar ("<idx> <ms>" per line) is parsed back into the per-event
// offset slice so a subsequent replay reproduces the waterfall verbatim.
func foldLiveTranscript(ctx context.Context, liveCallID string) *EpisodeTranscript {
	if liveCallID == "" {
		return nil
	}
	promptsDir := host.AgentPromptsDirFromCtx(ctx)
	if promptsDir == "" {
		return nil
	}
	transcriptsDir := filepath.Join(filepath.Dir(promptsDir), "transcripts")
	jsonlPath := filepath.Join(transcriptsDir, liveCallID+".jsonl")
	raw, err := os.ReadFile(jsonlPath)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	var events []any
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Store as a string element (verbatim JSON line); eventLines re-canonicalizes.
		events = append(events, line)
	}
	if len(events) == 0 {
		return nil
	}
	et := &EpisodeTranscript{Format: "claude-stream-json", Events: events}
	// Parse the parallel .timings sidecar ("<idx> <ms>" per line), preserving order.
	if traw, terr := os.ReadFile(filepath.Join(transcriptsDir, liveCallID+".timings")); terr == nil {
		for _, line := range strings.Split(strings.TrimRight(string(traw), "\n"), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			ms, perr := strconv.ParseInt(fields[1], 10, 64)
			if perr != nil {
				continue
			}
			et.Timings = append(et.Timings, ms)
		}
		if len(et.Timings) != len(et.Events) {
			et.Timings = nil // mismatch — drop rather than misalign the waterfall
		}
	}
	return et
}

// agentBodyToEpisode converts a host.AgentCallBody into an EpisodeAgent
// suitable for embedding in a cassette episode. detCallID is the deterministic
// call_id computed from the episode identity; it overrides the UUID the
// live handler generated.
func agentBodyToEpisode(b *host.AgentCallBody, detCallID string) *EpisodeAgent {
	responseStr := ""
	if b.Response != nil {
		// Store the raw JSON response string for .txt sidecar friendliness;
		// if it's a plain JSON string, unwrap it; otherwise keep the JSON form.
		var s string
		if err := json.Unmarshal(b.Response, &s); err == nil {
			responseStr = s
		} else {
			responseStr = string(b.Response)
		}
	}

	// Input is omitted from cassette episodes: it duplicates data already in
	// Prompt/SystemPrompt, and storing map[string]interface{} values through
	// goccy/go-yaml v1.19.2 can cause encoder stack overflow on certain YAML-
	// decoded map structures. The Prompt and Response fields are sufficient for
	// fromhistory to synthesise agent.<verb>.start/.complete trace events.

	return &EpisodeAgent{
		CallID:         detCallID,
		Verb:           b.Verb,
		Agent:          b.Agent,
		Model:          b.Model,
		DurationMs:     b.DurationMS,
		PromptTokens:   b.PromptTokens,
		ResponseTokens: b.ResponseTokens,
		CostUSD:        b.CostUSD,
		SystemPrompt:   b.SystemPrompt,
		Prompt:         b.Prompt,
		Response:       responseStr,
		Error:          b.Error,
	}
}

// synthesiseEpisode builds a new CassetteEpisode from a live handler result.
func synthesiseEpisode(handlerName string, args map[string]any, statePath string, cas *Cassette, result host.Result) *CassetteEpisode {
	matchMap := map[string]any{"handler": handlerName}
	if statePath != "" {
		matchMap["phase"] = cas.phaseFromStatePath(statePath)
	}

	// For agent handlers:
	//   1. Only match on handler + phase (skip args — they contain YAML-decoded
	//      nested maps that trigger a goccy/go-yaml v1.19.2 encoder stack overflow).
	//   2. Don't store result.Data — it contains deeply-nested JSON-decoded values
	//      (e.g. the "submitted" artifact) that also cause the YAML overflow. On
	//      replay the live agent fallback is always called, so the stored response
	//      data is never used.
	var respData map[string]any
	if !strings.HasPrefix(handlerName, "host.agent.") {
		for k, v := range args {
			if k != "handler" && k != "phase" && k != "schema_name" {
				matchMap[k] = v
			}
		}
		respData = result.Data
	}
	return &CassetteEpisode{
		ID:    fmt.Sprintf("recorded_%s_%s", handlerName, statePath),
		Match: matchMap,
		Response: CassetteResponse{
			Data:  respData,
			Error: result.Error,
		},
	}
}

// CassetteRecordMode returns the effective record mode. The environment variable
// KITSOKI_CASSETTE_RECORD wins over the file-level field. Returns "none" or
// "new_episodes". Any other value passed via the env var (e.g. "all") is
// returned as-is so the caller can surface a clear error — LoadCassette
// already rejects unsupported file-level values at parse time.
func CassetteRecordMode(cas *Cassette) string {
	if env := os.Getenv("KITSOKI_CASSETTE_RECORD"); env != "" {
		return env
	}
	if cas != nil && cas.RecordMode != "" {
		return cas.RecordMode
	}
	return "none"
}

// ValidateRecordMode reports whether mode is a supported effective record
// mode. Used by the testrunner to surface a clear error when an env-var
// override contains an unsupported value.
func ValidateRecordMode(mode string) error {
	switch mode {
	case "", "none", "new_episodes":
		return nil
	default:
		return fmt.Errorf("record_mode %q is not supported; valid values are \"none\" or \"new_episodes\"", mode)
	}
}

// CassetteStrictRecording returns true when KITSOKI_CASSETTE_STRICT=1 is set.
func CassetteStrictRecording() bool {
	v := os.Getenv("KITSOKI_CASSETTE_STRICT")
	return v == "1" || v == "true"
}

// AppendEpisodeToFile appends ep to the cassette file at cas.path.
//
// Rather than re-marshaling the entire cassette (which can stack-overflow on
// large agent responses when goccy/go-yaml encodes deeply-nested any values),
// we marshal only the new episode and raw-append it to the existing file.
// The episode is indented by two spaces to slot into the episodes: list.
func AppendEpisodeToFile(cas *Cassette, ep *CassetteEpisode) error {
	if cas.path == "" {
		return fmt.Errorf("cassette: AppendEpisodeToFile: cassette has no path")
	}

	cas.mu.Lock()
	defer cas.mu.Unlock()

	// Marshal only the single episode. goyaml encodes it as a YAML mapping
	// document (no leading "- "). We indent each line by two spaces and prefix
	// the first line with "- " to produce a valid list item.
	epBytes, marshalErr := goyaml.Marshal(ep)
	if marshalErr != nil {
		return fmt.Errorf("cassette: AppendEpisodeToFile: marshal episode: %w", marshalErr)
	}

	// Indent each non-empty line by four spaces so the episode sits at the
	// second indent level (under episodes:). Then replace the first four spaces
	// of the first line with "  - " (two spaces + list marker + space) so the
	// episode becomes a valid YAML sequence item inside the episodes: key.
	//
	//   episodes:
	//     - id: ...      ← four-space indent, first line gets "  - "
	//       match: ...   ← four-space indent on continuation lines
	lines := strings.Split(string(epBytes), "\n")
	for i, line := range lines {
		if line == "" {
			continue // preserve blank lines as-is
		}
		lines[i] = "    " + line
	}
	if len(lines) > 0 {
		// Replace the four leading spaces on the first non-empty line with "  - ".
		lines[0] = "  - " + strings.TrimPrefix(lines[0], "    ")
	}
	indented := strings.Join(lines, "\n")
	// Ensure the snippet ends with a newline.
	if !strings.HasSuffix(indented, "\n") {
		indented += "\n"
	}

	block := "\n# appended by KITSOKI_CASSETTE_RECORD\n" + indented

	// Open the cassette file in append mode — no read/re-marshal required.
	f, openErr := os.OpenFile(cas.path, os.O_WRONLY|os.O_APPEND, 0o644)
	if openErr != nil {
		return fmt.Errorf("cassette: AppendEpisodeToFile: open %q: %w", cas.path, openErr)
	}
	if _, writeErr := f.WriteString(block); writeErr != nil {
		_ = f.Close()
		return fmt.Errorf("cassette: AppendEpisodeToFile: write: %w", writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("cassette: AppendEpisodeToFile: close: %w", closeErr)
	}

	// Update the in-memory cassette to reflect the appended episode.
	cas.Episodes = append(cas.Episodes, *ep)
	return nil
}
