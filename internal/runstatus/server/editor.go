// editor.go — the story-editor read RPCs (docs/proposals/story-editor-view.md,
// story-graph-api.md). These operate PER STORY selected from the registry
// catalogue — NOT per live session: a story can be inspected in the editor
// without ever starting an orchestrator.
//
// The seam is [EditorProvider]: an optional capability a [SessionProvider] may
// implement to load a static app.App for a story path (the multi-story
// SessionRegistry does; the read-only single-entry adapter does not). When the
// provider does not implement it, every editor RPC returns codeReadOnly.
//
// All graph computation is delegated to the pure internal/app/graph package;
// this file is the JSON-RPC adapter: param parsing, story selection, error
// mapping, and the cassette read/replay paths (which DO touch the filesystem to
// read cassette files, but never an LLM).
package server

import (
	"fmt"
	"path/filepath"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/app/graph"
	"kitsoki/internal/testrunner"
)

// EditorProvider is the optional capability a [SessionProvider] implements to
// back the story-editor RPCs. It resolves a story (by absolute app.yaml path)
// to a static, validated app.App plus the directory the story lives in (used to
// resolve cassette globs). ok is false for an unknown story.
//
// It is deliberately separate from session lifecycle: the editor reads a story
// definition without a session. The concrete registry compiles the app fresh on
// each call (cheap, pure) so the editor always reflects the on-disk story.
type EditorProvider interface {
	// EditorApp loads the story at storyPath and returns its app.App and the
	// story directory. ok is false (no error) for an unknown / invalid story.
	EditorApp(storyPath string) (a app.App, storyDir string, ok bool)
}

// editorProvider returns the provider's EditorProvider capability, or false.
func (s *Server) editorProvider() (EditorProvider, bool) {
	ep, ok := s.provider.(EditorProvider)
	return ep, ok
}

// resolveEditorApp reads the story_path param and loads the story's app.App via
// the EditorProvider. It returns a structured rpcError on a missing capability
// (codeReadOnly), a missing param (codeServerError), or an unknown story
// (codeNotFound).
func (s *Server) resolveEditorApp(params map[string]any) (app.App, string, *rpcError) {
	ep, ok := s.editorProvider()
	if !ok {
		return nil, "", readOnlyErr("editor")
	}
	storyPath, _ := params["story_path"].(string)
	if storyPath == "" {
		return nil, "", &rpcError{Code: codeServerError, Message: "editor: missing 'story_path'"}
	}
	a, dir, ok := ep.EditorApp(storyPath)
	if !ok {
		return nil, "", &rpcError{Code: codeNotFound, Message: "editor: unknown or invalid story: " + storyPath}
	}
	return a, dir, nil
}

// dispatchEditor handles the runstatus.editor.* method family. It returns
// (result, nil, true) when it handled the method, or (nil, nil, false) when the
// method is not an editor method (so the main switch can continue). The third
// return is "handled".
func (s *Server) dispatchEditor(method string, params map[string]any) (any, *rpcError, bool) {
	switch method {
	// runstatus.editor.rooms {story_path} → {rooms: []RoomSummary}
	case "runstatus.editor.rooms":
		a, _, rerr := s.resolveEditorApp(params)
		if rerr != nil {
			return nil, rerr, true
		}
		return map[string]any{"rooms": graph.RoomList(a)}, nil, true

	// runstatus.editor.room {story_path, room_id} → RoomDetail
	case "runstatus.editor.room":
		a, dir, rerr := s.resolveEditorApp(params)
		if rerr != nil {
			return nil, rerr, true
		}
		roomID, _ := params["room_id"].(string)
		if roomID == "" {
			return nil, &rpcError{Code: codeServerError, Message: "editor.room: missing 'room_id'"}, true
		}
		storyPath, _ := params["story_path"].(string)
		detail, ok := graph.Detail(a, roomID, storyPath)
		if !ok {
			return nil, &rpcError{Code: codeNotFound, Message: "editor.room: unknown room: " + roomID}, true
		}
		_ = dir
		return detail, nil, true

	// runstatus.editor.oracles {story_path, room_id} → {contracts, cassette_globs}
	case "runstatus.editor.oracles":
		a, dir, rerr := s.resolveEditorApp(params)
		if rerr != nil {
			return nil, rerr, true
		}
		roomID, _ := params["room_id"].(string)
		if roomID == "" {
			return nil, &rpcError{Code: codeServerError, Message: "editor.oracles: missing 'room_id'"}, true
		}
		contracts := graph.OracleContracts(a, roomID)
		return map[string]any{
			"contracts":      contracts,
			"cassette_globs": graph.CassetteGlob(dir),
		}, nil, true

	// runstatus.editor.cassettes {story_path, cassette_key} →
	//   {episodes: [{cassette_file, episode_id, input_digest, output_preview}]}
	case "runstatus.editor.cassettes":
		_, dir, rerr := s.resolveEditorApp(params)
		if rerr != nil {
			return nil, rerr, true
		}
		key := parseCassetteKey(params["cassette_key"])
		eps, err := listCassetteEpisodes(dir, key)
		if err != nil {
			return nil, serverErr(err), true
		}
		return map[string]any{"episodes": eps}, nil, true

	// runstatus.editor.replay {story_path, room_id, oracle_index, cassette_file?}
	//   → {output, world_snapshot, source: "cassette", note?}
	case "runstatus.editor.replay":
		a, dir, rerr := s.resolveEditorApp(params)
		if rerr != nil {
			return nil, rerr, true
		}
		return editorReplay(a, dir, params)

	default:
		return nil, nil, false
	}
}

// ── Cassette listing ──────────────────────────────────────────────────────────

// CassetteEpisodeSummary is one cassette episode the editor lists for a key.
type CassetteEpisodeSummary struct {
	CassetteFile  string `json:"cassette_file"`
	EpisodeID     string `json:"episode_id"`
	Handler       string `json:"handler,omitempty"`
	Phase         string `json:"phase,omitempty"`
	SchemaName    string `json:"schema_name,omitempty"`
	InputDigest   string `json:"input_digest"`
	OutputPreview string `json:"output_preview"`
}

// listCassetteEpisodes loads every cassette under the story's cassette globs and
// returns the episodes whose match map is consistent with key (handler / phase /
// schema_name when set on the key). A nil/empty key returns all oracle episodes.
func listCassetteEpisodes(storyDir string, key graph.CassetteKey) ([]CassetteEpisodeSummary, error) {
	var files []string
	for _, glob := range graph.CassetteGlob(storyDir) {
		matches, _ := filepath.Glob(glob)
		files = append(files, matches...)
	}

	var out []CassetteEpisodeSummary
	for _, f := range files {
		cas, err := testrunner.LoadCassette(f)
		if err != nil {
			// Skip unreadable / non-cassette files rather than failing the whole list.
			continue
		}
		for i := range cas.Episodes {
			ep := &cas.Episodes[i]
			if !episodeMatchesKey(ep, key) {
				continue
			}
			out = append(out, CassetteEpisodeSummary{
				CassetteFile:  f,
				EpisodeID:     ep.ID,
				Handler:       matchString(ep, "handler"),
				Phase:         matchString(ep, "phase"),
				SchemaName:    matchString(ep, "schema_name"),
				InputDigest:   episodeInputDigest(ep),
				OutputPreview: episodeOutputPreview(ep),
			})
		}
	}
	return out, nil
}

// episodeMatchesKey reports whether an episode's match map is consistent with
// the requested key: for every non-empty key field, the episode either does not
// constrain that field or constrains it to the same value.
func episodeMatchesKey(ep *testrunner.CassetteEpisode, key graph.CassetteKey) bool {
	check := func(field, want string) bool {
		if want == "" {
			return true
		}
		got := matchString(ep, field)
		return got == "" || got == want
	}
	if !check("handler", key.Handler) {
		return false
	}
	if !check("phase", key.Phase) {
		return false
	}
	if !check("schema_name", key.SchemaName) {
		return false
	}
	if key.Call != "" {
		if got := matchString(ep, "call"); got != "" && got != key.Call {
			return false
		}
	}
	return true
}

func matchString(ep *testrunner.CassetteEpisode, field string) string {
	if ep.Match == nil {
		return ""
	}
	if v, ok := ep.Match[field]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprint(v)
	}
	return ""
}

// episodeInputDigest returns a short digest of the episode's recorded input /
// prompt for the list view.
func episodeInputDigest(ep *testrunner.CassetteEpisode) string {
	if ep.Oracle != nil {
		if ep.Oracle.Prompt != "" {
			return preview(ep.Oracle.Prompt, 120)
		}
		if ep.Oracle.Input != nil {
			return preview(fmt.Sprint(ep.Oracle.Input), 120)
		}
	}
	return preview(fmt.Sprint(ep.Match), 120)
}

// episodeOutputPreview returns the first ~200 chars of the episode's recorded
// output (oracle response or response data).
func episodeOutputPreview(ep *testrunner.CassetteEpisode) string {
	if ep.Oracle != nil && ep.Oracle.Response != "" {
		return preview(ep.Oracle.Response, 200)
	}
	if ep.Response.Data != nil {
		return preview(fmt.Sprint(ep.Response.Data), 200)
	}
	if ep.Response.Error != "" {
		return preview(ep.Response.Error, 200)
	}
	return ""
}

func preview(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── Replay ──────────────────────────────────────────────────────────────────

// editorReplay implements the cassette-override read path: it loads the
// recorded output for an oracle call from a cassette and returns it plus a
// best-effort post-bind world snapshot derived from the effect's bind
// directives. The live-dispatch path (a real oracle round-trip) requires a
// session and is intentionally NOT wired here — the editor is per-story, not
// per-session — so a request without a cassette_file returns codeReadOnly.
// Task-oracle live replay is rejected with a clear error regardless.
func editorReplay(a app.App, storyDir string, params map[string]any) (any, *rpcError, bool) {
	roomID, _ := params["room_id"].(string)
	if roomID == "" {
		return nil, &rpcError{Code: codeServerError, Message: "editor.replay: missing 'room_id'"}, true
	}
	idx, hasIdx := intParam(params, "oracle_index")
	if !hasIdx {
		return nil, &rpcError{Code: codeServerError, Message: "editor.replay: missing 'oracle_index'"}, true
	}
	contracts := graph.OracleContracts(a, roomID)
	if idx < 0 || idx >= len(contracts) {
		return nil, &rpcError{Code: codeServerError, Message: fmt.Sprintf("editor.replay: oracle_index %d out of range (room has %d oracle calls)", idx, len(contracts))}, true
	}
	contract := contracts[idx]

	cassetteFile, _ := params["cassette_file"].(string)
	if cassetteFile == "" {
		// Live replay requires a session + operator; not available on the
		// per-story editor surface.
		return nil, &rpcError{Code: codeReadOnly, Message: "editor.replay: live replay requires a session; supply 'cassette_file' for cassette-override replay"}, true
	}
	if strings.HasSuffix(contract.Kind, ".task") {
		return nil, &rpcError{Code: codeServerError, Message: "editor.replay: task-oracle live replay is not supported (would run an agent with side effects)"}, true
	}

	// Resolve the cassette file relative to the story dir when not absolute.
	if !filepath.IsAbs(cassetteFile) {
		cassetteFile = filepath.Join(storyDir, cassetteFile)
	}
	cas, err := testrunner.LoadCassette(cassetteFile)
	if err != nil {
		return nil, serverErr(fmt.Errorf("editor.replay: load cassette: %w", err)), true
	}

	// Find the episode matching this contract's cassette key. We reuse
	// MatchEpisode-style keying via episodeMatchesKey so the same handler/phase/
	// schema selection the runtime uses applies here.
	var matched *testrunner.CassetteEpisode
	for i := range cas.Episodes {
		ep := &cas.Episodes[i]
		if episodeMatchesKey(ep, contract.CassetteKey) {
			matched = ep
			break
		}
	}
	if matched == nil {
		return nil, &rpcError{Code: codeNotFound, Message: "editor.replay: no episode in cassette matches this oracle call"}, true
	}

	output := episodeRecordedOutput(matched)
	world := bindWorldSnapshot(a, roomID, contract, output)

	return map[string]any{
		"output":         output,
		"world_snapshot": world,
		"source":         "cassette",
		"cassette_file":  cassetteFile,
		"episode_id":     matched.ID,
		"note":           "cassette-override replay: recorded output + bind-directive world snapshot (no live oracle, no LLM)",
	}, nil, true
}

// episodeRecordedOutput returns the episode's recorded output as a value the
// frontend can render: the oracle response string when present, else the
// response data map.
func episodeRecordedOutput(ep *testrunner.CassetteEpisode) any {
	if ep.Oracle != nil && ep.Oracle.Response != "" {
		return ep.Oracle.Response
	}
	if ep.Response.Data != nil {
		return ep.Response.Data
	}
	if ep.Response.Error != "" {
		return map[string]any{"error": ep.Response.Error}
	}
	return nil
}

// bindWorldSnapshot builds a best-effort post-bind world snapshot from the
// effect's bind directives: for each world_key←result_key binding, it pulls the
// matching key out of the recorded output (when the output is a map) so the
// editor can show what the call would write to the world.
func bindWorldSnapshot(a app.App, roomID string, contract graph.OracleContract, output any) map[string]any {
	snap := map[string]any{}
	st, ok := a.LookupState(app.StatePath(roomIDOf(roomID)))
	if !ok {
		return snap
	}
	// Recover the bind map for the effect at contract.EffectIndex on on_enter.
	var bind map[string]string
	if contract.EffectIndex < len(st.OnEnter) {
		e := st.OnEnter[contract.EffectIndex]
		if e.Invoke == contract.Kind {
			bind = e.Bind
		}
	}
	outMap, _ := output.(map[string]any)
	for worldKey, resultKey := range bind {
		if outMap != nil {
			if v, ok := outMap[resultKey]; ok {
				snap[worldKey] = v
				continue
			}
		}
		// Common case: the whole submission is bound (bind: {key: submitted}).
		snap[worldKey] = output
	}
	return snap
}

// roomIDOf normalises a possibly-pathful room id to its top-level segment.
func roomIDOf(roomID string) string {
	if i := strings.IndexByte(roomID, '.'); i >= 0 {
		return roomID[:i]
	}
	return roomID
}

// parseCassetteKey reads the cassette_key param (a JSON object) into a
// graph.CassetteKey. A nil/absent param yields the zero key (match-all).
func parseCassetteKey(v any) graph.CassetteKey {
	m, ok := v.(map[string]any)
	if !ok {
		return graph.CassetteKey{}
	}
	str := func(k string) string {
		if s, ok := m[k].(string); ok {
			return s
		}
		return ""
	}
	return graph.CassetteKey{
		Handler:    str("handler"),
		Phase:      str("phase"),
		SchemaName: str("schema_name"),
		Call:       str("call"),
	}
}
