package graph

import (
	"fmt"
	"path/filepath"
	"strings"

	"kitsoki/internal/app"
)

// OracleContract describes one host.oracle.* invoke a room makes, with enough
// keying for the oracle workbench (slice 3) to locate its cassette episodes.
type OracleContract struct {
	// Kind is the oracle verb handler, e.g. "host.oracle.decide".
	Kind string `json:"kind"`
	// PromptPath is the prompt template path (with.prompt_path or with.prompt).
	PromptPath string `json:"prompt_path,omitempty"`
	// OutputSchema is the schema path (with.schema), relative to the story dir.
	OutputSchema string `json:"output_schema,omitempty"`
	// CassetteKey is the match key a cassette episode must carry to back this
	// call. It mirrors internal/testrunner/cassette.go MatchEpisode: a cassette
	// episode whose `match:` is a subset of these fields will match this call.
	CassetteKey CassetteKey `json:"cassette_key"`
	// EffectIndex is the on_enter (or arc) effect index this contract came from,
	// for stable addressing across calls that share a handler.
	EffectIndex int `json:"effect_index"`
}

// CassetteKey is the cassette-match tuple for an oracle call, mirroring the
// synthetic match fields MatchEpisode derives (handler / phase / schema_name)
// plus the author-assigned call id (Effect.Id) when present, which flow stubs
// select on via by_call: and cassettes via match: { call: <id> }.
type CassetteKey struct {
	Handler    string `json:"handler"`
	Phase      string `json:"phase"`
	SchemaName string `json:"schema_name,omitempty"`
	Call       string `json:"call,omitempty"`
}

// OracleContracts walks a room's on_enter and intent-arc effects and returns
// one OracleContract per host.oracle.* invoke. roomID is the top-level state id.
// It is a pure function over the app.
func OracleContracts(a app.App, roomID string) []OracleContract {
	id := roomOf(roomID)
	if id == "" {
		id = roomID
	}
	st, ok := a.LookupState(app.StatePath(id))
	if !ok {
		return nil
	}

	var out []OracleContract
	phase := id // phase = first dot-segment of statePath; for a top-level room that is the room id.

	add := func(e app.Effect, idx int) {
		if !isOracleInvoke(e.Invoke) {
			return
		}
		promptPath := withString(e.With, "prompt_path")
		if promptPath == "" {
			promptPath = withString(e.With, "prompt")
		}
		schema := withString(e.With, "schema")
		out = append(out, OracleContract{
			Kind:         e.Invoke,
			PromptPath:   promptPath,
			OutputSchema: schema,
			EffectIndex:  idx,
			CassetteKey: CassetteKey{
				Handler:    e.Invoke,
				Phase:      phase,
				SchemaName: schemaName(schema),
				Call:       e.Id,
			},
		})
	}

	for i, e := range st.OnEnter {
		add(e, i)
	}
	for _, transitions := range st.On {
		for _, tr := range transitions {
			for i, e := range tr.Effects {
				add(e, i)
			}
		}
	}
	return out
}

// CassetteGlob returns the conventional glob pattern under which a story's host
// cassette files live, so the workbench can enumerate candidate cassettes for a
// story. Stories keep cassettes alongside their flows under
// <storyDir>/flows/*.cassette.yaml, <storyDir>/cassettes/*.yaml, or one level
// deeper at <storyDir>/flows/cassettes/*.cassette.yaml (the layout real stories
// such as docs-review and frontier_event use); the workbench matches episodes
// by CassetteKey across whatever files it finds.
func CassetteGlob(storyDir string) []string {
	return []string{
		filepath.Join(storyDir, "cassettes", "*.yaml"),
		filepath.Join(storyDir, "flows", "*.cassette.yaml"),
		filepath.Join(storyDir, "flows", "cassettes", "*.cassette.yaml"),
		filepath.Join(storyDir, "flows", "cassettes", "*.yaml"),
	}
}

// schemaName mirrors MatchEpisode's schema_name synthetic field: filepath.Base
// of the schema arg, empty when no schema is set.
func schemaName(schema string) string {
	if schema == "" {
		return ""
	}
	return filepath.Base(schema)
}

// withString reads a string-valued key from an effect's With map. Values are
// templated strings as authored; non-string values stringify via fmt.
func withString(with map[string]any, key string) string {
	if with == nil {
		return ""
	}
	v, ok := with[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
