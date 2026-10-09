package orchestrator_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/capsuletest"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	renderElements "kitsoki/internal/render/elements"
)

func TestSlideyEditKitsokiPitchAnnotationRefineCapsule(t *testing.T) {
	repoRoot := capsuletest.Open(t, "slidey-edit-kitsoki-pitch")
	capsuletest.Verify(t, repoRoot)

	kitsokiRoot := kitsokiRootFromOrchestratorTest(t)
	require.NoError(t,
		copyTree(filepath.Join(kitsokiRoot, "stories", "slidey-edit"), filepath.Join(repoRoot, "stories", "slidey-edit")),
		"copy slidey-edit story into capsule workspace",
	)
	t.Chdir(repoRoot)

	def, err := app.Load(filepath.Join(repoRoot, "stories", "slidey-edit", "app.yaml"))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := newTestStore(t)
	require.NoError(t, err)

	var renderCount, artifactCount, refineAgentCalls int
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	reg.Replace("host.git_worktree", func(ctx context.Context, args map[string]any) (host.Result, error) {
		require.Equal(t, "create", args["op"])
		require.NotEmpty(t, args["id"])
		require.NotEmpty(t, args["name"])
		require.Equal(t, "staging/local", args["base"])
		return host.Result{Data: map[string]any{
			"ok":   true,
			"path": repoRoot,
		}}, nil
	})
	reg.Replace("host.slidey.render", func(ctx context.Context, args map[string]any) (host.Result, error) {
		renderCount++
		specPath, _ := args["spec_path"].(string)
		require.Equal(t, filepath.Join(repoRoot, "docs", "decks", "kitsoki-pitch.slidey.json"), specPath)
		require.FileExists(t, specPath)

		artifactDir := filepath.Join(repoRoot, ".artifacts", "slidey-edit-test")
		require.NoError(t, os.MkdirAll(artifactDir, 0o755))
		htmlPath := filepath.Join(artifactDir, fmt.Sprintf("deck-%d.html", renderCount))
		semanticPath := filepath.Join(artifactDir, fmt.Sprintf("deck-%d.semantic.json", renderCount))
		require.NoError(t, os.WriteFile(htmlPath, []byte("<!doctype html><title>deck</title>"), 0o644))
		require.NoError(t, os.WriteFile(semanticPath, []byte(`{"elements":[]}`), 0o644))

		return host.Result{Data: map[string]any{
			"path":          htmlPath,
			"semantic_path": semanticPath,
		}}, nil
	})
	reg.Replace("host.artifacts_dir", func(ctx context.Context, args map[string]any) (host.Result, error) {
		artifactCount++
		handle := fmt.Sprintf("slidey-edit-render-%d", artifactCount)
		return host.Result{Data: map[string]any{
			"ok":     true,
			"path":   args["src_path"],
			"handle": map[string]any{"id": handle},
		}}, nil
	})
	reg.Replace("host.agent.task", func(ctx context.Context, args map[string]any) (host.Result, error) {
		refineAgentCalls++
		require.Equal(t, filepath.Join(repoRoot, "docs", "decks"), args["working_dir"])

		contextArgs := requireMap(t, requireMap(t, args, "context"), "args")
		require.Equal(t, repoRoot, contextArgs["workdir"])
		require.Equal(t, "docs/decks", contextArgs["workspace"])
		deckArg := requireMap(t, contextArgs, "deck")
		require.Equal(t, "docs/decks/kitsoki-pitch.slidey.json", deckArg["spec_path"])
		require.Equal(t, "kitsoki-pitch.slidey.json", deckArg["workspace_spec_path"])

		workspaceSpecPath, _ := deckArg["workspace_spec_path"].(string)
		deckPath := filepath.Join(repoRoot, "docs", "decks", workspaceSpecPath)
		wrongPath := filepath.Join(repoRoot, "docs", "decks", "docs", "decks", "kitsoki-pitch.slidey.json")
		require.NoFileExists(t, wrongPath, "agent must not be guided toward workspace+repo-relative double path")

		require.Equal(t, "El Nombre Es", readSceneEyebrow(t, deckPath, 1))
		writeSceneEyebrow(t, deckPath, 1, "About the Name")

		return host.Result{Data: map[string]any{
			"ok": true,
			"submitted": map[string]any{
				"spec_path": "docs/decks/kitsoki-pitch.slidey.json",
				"summary":   "Changed slide 2 title to About the Name.",
				"edited":    []any{"1/eyebrow"},
			},
		}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	started, err := orch.SubmitDirectFromInput(ctx, sid, "start", map[string]any{
		"feedback": "edit kitsoki-pitch",
	}, "edit kitsoki-pitch")
	require.NoError(t, err)
	require.Equal(t, app.StatePath("reviewing"), started.NewState, started.View)
	require.Equal(t, 1, renderCount)
	require.Equal(t, "El Nombre Es", readSceneEyebrow(t, filepath.Join(repoRoot, "docs", "decks", "kitsoki-pitch.slidey.json"), 1))
	requireSlideyEditAnnotateURL(t, started, sid)

	refined, err := orch.SubmitDirectFromInput(ctx, sid, "refine", map[string]any{
		"feedback":      "Change this to About the Name",
		"current_scene": "1",
		"annotation_anchor": map[string]any{
			"kind":  "semantic_element",
			"label": "El Nombre Es",
			"semantic_element": map[string]any{
				"plugin": "slidey",
				"ref":    "1/eyebrow",
				"bbox":   []any{120, 80, 420, 90},
			},
		},
	}, "Change this to About the Name")
	require.NoError(t, err)
	require.Equal(t, app.StatePath("reviewing"), refined.NewState, refined.View)
	require.Equal(t, 2, renderCount)
	require.Equal(t, 1, refineAgentCalls)

	deckPath := filepath.Join(repoRoot, "docs", "decks", "kitsoki-pitch.slidey.json")
	require.Equal(t, "About the Name", readSceneEyebrow(t, deckPath, 1))
	require.NoFileExists(t, filepath.Join(repoRoot, "docs", "decks", "docs", "decks", "kitsoki-pitch.slidey.json"))
	require.Contains(t, refined.View, "Changed slide 2 title to About the Name.")
	requireSlideyEditAnnotateURL(t, refined, sid)
}

func kitsokiRootFromOrchestratorTest(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(cwd, "..", ".."))
}

func requireMap(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key]
	require.Truef(t, ok, "missing map key %q in %#v", key, m)
	out, ok := v.(map[string]any)
	require.Truef(t, ok, "map key %q has type %T, want map[string]any", key, v)
	return out
}

func readSceneEyebrow(t *testing.T, deckPath string, scene int) string {
	t.Helper()
	var deck map[string]any
	b, err := os.ReadFile(deckPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &deck))
	scenes, ok := deck["scenes"].([]any)
	require.True(t, ok, "deck scenes must be an array")
	require.Greater(t, len(scenes), scene)
	sceneObj, ok := scenes[scene].(map[string]any)
	require.True(t, ok, "scene %d must be an object", scene)
	eyebrow, _ := sceneObj["eyebrow"].(string)
	return eyebrow
}

func writeSceneEyebrow(t *testing.T, deckPath string, scene int, eyebrow string) {
	t.Helper()
	var deck map[string]any
	b, err := os.ReadFile(deckPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &deck))
	scenes := deck["scenes"].([]any)
	sceneObj := scenes[scene].(map[string]any)
	sceneObj["eyebrow"] = eyebrow
	out, err := json.MarshalIndent(deck, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(deckPath, append(out, '\n'), 0o644))
}

func requireSlideyEditAnnotateURL(t *testing.T, out *orchestrator.TurnOutcome, sid app.SessionID) {
	t.Helper()
	wantURL := "http://127.0.0.1:7777/#/s/" + string(sid) + "/chat?visual_annotate=1"
	require.Contains(t, out.View, "annotate → "+wantURL, "TUI render must expose the web spatial-oracle handoff URL")
	require.NotNil(t, out.TypedView, "web transport must receive the structured typed view")
	webView, err := renderElements.EvalElements(*out.TypedView, out.RenderEnv, out.Renderer)
	require.NoError(t, err, "web transport typed_view evaluation")

	var media *app.ViewElement
	for i := range webView.Elements {
		el := &webView.Elements[i]
		if el.Kind == "media" {
			media = el
			break
		}
	}
	require.NotNil(t, media, "reviewing view must expose the deck as a media element")
	require.Equal(t, "refine", media.AnnotateIntent)
	require.Equal(t, "feedback", media.AnnotateFeedbackSlot)
	require.Equal(t, wantURL, media.AnnotateURL)
}
