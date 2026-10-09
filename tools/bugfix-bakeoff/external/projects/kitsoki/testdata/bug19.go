package host_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	"kitsoki/internal/render"
	"kitsoki/internal/render/sourcecolor"
)

// TestRepro_ValidatorSchemaUsesPerCallRenderer proves that a dispatch retains
// its own story-relative validator schema when another Studio session has most
// recently set the process-global app directory.
func TestRepro_ValidatorSchemaUsesPerCallRenderer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.AgentBinEnv, fakeOneShotMCPBin(t))
	t.Setenv("KITSOKI_BIN", "/usr/local/bin/kitsoki")

	storyA := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(storyA, "schemas"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(storyA, "schemas", "p.json"), []byte(`{"type":"object"}`), 0o644))
	prompt := filepath.Join(storyA, "p.md")
	require.NoError(t, os.WriteFile(prompt, []byte("x"), 0o644))

	storyB := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(storyB, "schemas"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(storyB, "schemas", "p.json"), []byte(`{"type":"object"}`), 0o644))
	t.Setenv(host.AppDirEnv, storyB)

	renderer, err := render.NewPromptRenderer(render.PromptPath{Story: storyA}, true)
	require.NoError(t, err)
	res, err := host.AgentAskWithMCPHandler(host.WithPromptRenderer(context.Background(), renderer), map[string]any{
		"prompt_path": prompt, "schema": "schemas/p.json", "output_format": "json",
	})
	require.NoError(t, err)
	require.Empty(t, res.Error)
	parsed, _ := res.Data["stdout_json"].(map[string]any)
	body, _ := parsed["mcp_body"].(map[string]any)
	servers, _ := body["mcpServers"].(map[string]any)
	validator, _ := servers["validator"].(map[string]any)
	args, _ := validator["args"].([]any)
	require.GreaterOrEqual(t, len(args), 3)
	require.Equal(t, filepath.Join(storyA, "schemas", "p.json"), sourcecolor.Strip(args[2].(string)),
		"schema must resolve against the dispatching session, not the globally last-opened story")
}
