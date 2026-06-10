package server_test

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus/server"
)

// editorStubProvider extends stubProvider with the EditorProvider capability,
// serving a fixed compiled app for a single known story path.
type editorStubProvider struct {
	*stubProvider
	storyPath string
	storyDir  string
	a         app.App
}

func (p *editorStubProvider) EditorApp(storyPath string) (app.App, string, bool) {
	if storyPath != p.storyPath {
		return nil, "", false
	}
	return p.a, p.storyDir, true
}

// editorDef builds idle → work, where work runs a host.oracle.decide on_enter.
func editorDef() *app.AppDef {
	return &app.AppDef{
		World: map[string]app.VarDef{"idea": {Type: "string"}},
		Root:  "idle",
		States: map[string]*app.State{
			"idle": {Description: "Start", On: map[string][]app.Transition{"go": {{Target: "work"}}}},
			"work": {
				OnEnter: []app.Effect{{
					Invoke: "host.oracle.decide",
					With:   map[string]any{"schema": "schemas/x.json", "prompt_path": "prompts/p.md"},
					Bind:   map[string]string{"idea": "submitted"},
				}},
			},
		},
	}
}

func newEditorServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	p := &editorStubProvider{
		stubProvider: newStubProvider(),
		storyPath:    "/abs/story/app.yaml",
		storyDir:     "/abs/story",
		a:            app.Compile(editorDef()),
	}
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	t.Cleanup(ts.Close)
	return ts, p.storyPath
}

func TestEditor_Rooms(t *testing.T) {
	ts, story := newEditorServer(t)
	var out struct {
		Rooms []struct {
			ID        string  `json:"id"`
			Distance  float64 `json:"distance"`
			HasOracle bool    `json:"has_oracle"`
		} `json:"rooms"`
	}
	rpcCall(t, ts, "runstatus.editor.rooms", map[string]any{"story_path": story}, &out)
	require.Len(t, out.Rooms, 2)
	assert.Equal(t, "idle", out.Rooms[0].ID)
	assert.Equal(t, float64(0), out.Rooms[0].Distance)
	assert.Equal(t, "work", out.Rooms[1].ID)
	assert.True(t, out.Rooms[1].HasOracle)
}

func TestEditor_RoomDetail(t *testing.T) {
	ts, story := newEditorServer(t)
	var detail struct {
		ID        string `json:"id"`
		WorldKeys []struct {
			Name      string `json:"name"`
			Direction string `json:"direction"`
		} `json:"world_keys"`
		SourceRef *struct {
			Path string `json:"path"`
			Line int    `json:"line"`
		} `json:"source_ref"`
	}
	rpcCall(t, ts, "runstatus.editor.room", map[string]any{"story_path": story, "room_id": "work"}, &detail)
	assert.Equal(t, "work", detail.ID)
	require.NotNil(t, detail.SourceRef)
	assert.Equal(t, story, detail.SourceRef.Path)
	assert.Equal(t, 1, detail.SourceRef.Line)

	dir := map[string]string{}
	for _, wk := range detail.WorldKeys {
		dir[wk.Name] = wk.Direction
	}
	assert.Equal(t, "write", dir["idea"])
}

func TestEditor_OracleContracts(t *testing.T) {
	ts, story := newEditorServer(t)
	var out struct {
		Contracts []struct {
			Kind        string `json:"kind"`
			PromptPath  string `json:"prompt_path"`
			CassetteKey struct {
				Handler    string `json:"handler"`
				Phase      string `json:"phase"`
				SchemaName string `json:"schema_name"`
			} `json:"cassette_key"`
		} `json:"contracts"`
		CassetteGlobs []string `json:"cassette_globs"`
	}
	rpcCall(t, ts, "runstatus.editor.oracles", map[string]any{"story_path": story, "room_id": "work"}, &out)
	require.Len(t, out.Contracts, 1)
	assert.Equal(t, "host.oracle.decide", out.Contracts[0].Kind)
	assert.Equal(t, "prompts/p.md", out.Contracts[0].PromptPath)
	assert.Equal(t, "work", out.Contracts[0].CassetteKey.Phase)
	assert.Equal(t, "x.json", out.Contracts[0].CassetteKey.SchemaName)
	assert.NotEmpty(t, out.CassetteGlobs)
}

func TestEditor_UnknownStory(t *testing.T) {
	ts, _ := newEditorServer(t)
	code, msg := rpcCallExpectError(t, ts, "runstatus.editor.rooms", map[string]any{"story_path": "/nope/app.yaml"})
	assert.Equal(t, -32002, code, "want codeNotFound, got msg=%q", msg)
}

func TestEditor_NoEditorProvider(t *testing.T) {
	// A plain stubProvider does NOT implement EditorProvider → codeReadOnly.
	p := newStubProvider()
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	t.Cleanup(ts.Close)
	code, _ := rpcCallExpectError(t, ts, "runstatus.editor.rooms", map[string]any{"story_path": "/x/app.yaml"})
	assert.Equal(t, -32001, code, "want codeReadOnly")
}

func TestEditor_ReplayRequiresCassette(t *testing.T) {
	ts, story := newEditorServer(t)
	// No cassette_file → live replay rejected as read-only on the per-story surface.
	code, _ := rpcCallExpectError(t, ts, "runstatus.editor.replay", map[string]any{
		"story_path":   story,
		"room_id":      "work",
		"oracle_index": 0,
	})
	assert.Equal(t, -32001, code, "want codeReadOnly for live replay without cassette")
}
