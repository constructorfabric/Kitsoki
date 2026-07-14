package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/capsuletest"
	"kitsoki/internal/corpusproof"
	"kitsoki/internal/corpusreceipt"
	studio "kitsoki/internal/mcp/studio"
)

// TestCorpusForgeDogfoodOverMCP proves the production-shaped local runtime,
// not a flow stub: a synthetic Git fixture is independently checked out at its
// RED and GREEN refs, its argv oracle is executed through the OS network-denial
// sandbox, and the resulting calibration receipt is frozen through FileStore.
// A second, distinct Studio session then attempts to freeze the same candidate
// as heldout and must be rejected by the shared durable receipt registry.
func TestCorpusForgeDogfoodOverMCP(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Corpus Forge dogfood uses the Darwin sandbox-exec adapter; Linux coverage belongs on a Bubblewrap-capable runner")
	}
	const sandboxBinary = "/usr/bin/sandbox-exec"
	if _, err := os.Stat(sandboxBinary); err != nil {
		t.Skipf("Corpus Forge dogfood requires %s: %v", sandboxBinary, err)
	}

	ctx := context.Background()
	repositoryRoot := capsuletest.Open(t, "corpus-forge-red-green")
	runRoot := t.TempDir()
	configPath := writeDogfoodCorpusRuntimeConfig(t, runRoot, repositoryRoot, sandboxBinary)
	configure, err := loadCorpusRuntimeConfigurer(configPath, nil)
	require.NoError(t, err)

	// This is the same Studio MCP registry construction path the `mcp` command
	// uses after parsing --corpus-runtime-config. Replay is explicit and no
	// session drives free text, so no harness can call an LLM.
	session := studio.NewStudioSession(studio.DefaultHarnessBuilder)
	session.SetHostRegistryConfigurer(configure)
	server := studio.NewServer(session)
	client := connectStudioTestClient(ctx, t, server)

	candidate := dogfoodCandidate()
	calibration := openCorpusForgeSession(t, ctx, client, "calibration", candidate, "calibration-red-green")
	driveCorpusForge(t, ctx, client, calibration)

	store, err := corpusreceipt.NewFileStore(filepath.Join(runRoot, "receipts"))
	require.NoError(t, err)
	receipts, err := store.List(ctx)
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	require.Equal(t, corpusreceipt.RoleCalibration, receipts[0].CorpusRole)
	require.Equal(t, "calibration-red-green", receipts[0].SelectionID)
	require.Len(t, receipts[0].Proofs, 1)
	require.NotZero(t, receipts[0].Proofs[0].Baseline.ExitCode, "baseline oracle must be observed RED")
	require.Zero(t, receipts[0].Proofs[0].Fix.ExitCode, "fix oracle must be observed GREEN")
	require.Equal(t, []string{"/bin/test", "-f", "fixed.txt"}, receipts[0].Proofs[0].Baseline.Command)
	require.Contains(t, receipts[0].Proofs[0].EnvironmentFingerprint, "sandbox-exec-sha256:")

	// A fresh MCP driving handle is intentionally used here. The overlap check
	// must come from FileStore, not from session-local world state.
	heldout := openCorpusForgeSession(t, ctx, client, "heldout", candidate, "heldout-overlap")
	driveCorpusForge(t, ctx, client, heldout)
	state, lastError := corpusForgeState(t, ctx, client, heldout)
	require.Equal(t, "failed", state)
	require.Contains(t, lastError, `already belongs to calibration receipt "calibration-red-green"`)

	receipts, err = store.List(ctx)
	require.NoError(t, err)
	require.Len(t, receipts, 1, "heldout overlap must not create a second durable receipt")
}

func writeDogfoodCorpusRuntimeConfig(t *testing.T, root, repositoryRoot, sandboxBinary string) string {
	t.Helper()
	path := filepath.Join(root, "corpus-runtime.yaml")
	config := "schema: corpus-runtime/v1\n" +
		"repository_root: " + repositoryRoot + "\n" +
		"repository_identity: fixtures/corpus-forge-red-green\n" +
		"workspace_root: workspaces\n" +
		"receipt_store: receipts\n" +
		"sandbox:\n" +
		"  kind: sandbox-exec\n" +
		"  binary: " + sandboxBinary + "\n"
	require.NoError(t, os.WriteFile(path, []byte(config), 0o600))
	return path
}

func dogfoodCandidate() map[string]any {
	return map[string]any{
		"kind":         corpusproof.CorpusCaseV1,
		"id":           "corpus-forge-red-green",
		"repo":         "fixtures/corpus-forge-red-green",
		"source":       "local-git-history",
		"baseline_ref": "HEAD~1",
		"fix_ref":      "HEAD",
		"ticket":       "synthetic-red-green",
		"archetype":    "bugfix",
		"oracle": map[string]any{
			"command": []string{"/bin/test", "-f", "fixed.txt"},
		},
		"provenance": map[string]any{
			"adapter":       "synthetic-capsule",
			"locator":       "capsules/corpus-forge-red-green",
			"source_digest": "sha256:corpus-forge-red-green-v1",
		},
	}
}

func openCorpusForgeSession(t *testing.T, ctx context.Context, client *mcpsdk.ClientSession, role string, candidate map[string]any, selectionID string) string {
	t.Helper()
	result, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "session.new", Arguments: map[string]any{
		"story_path": "../../stories/corpus-forge/app.yaml",
		"harness":    "replay",
		"key":        selectionID,
		"trace":      filepath.Join(t.TempDir(), selectionID+".jsonl"),
		"initial_world": map[string]any{
			"source_candidates":   []any{candidate},
			"task_limit":          1,
			"selection_id":        selectionID,
			"corpus_role":         role,
			"source_manifest_ref": "capsules/corpus-forge-red-green@HEAD",
		},
	}})
	require.NoError(t, err)
	require.False(t, result.IsError, "session.new: %s", studioMCPText(result))
	var opened studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(studioMCPText(result)), &opened))
	require.True(t, opened.OK)
	return opened.Handle
}

func driveCorpusForge(t *testing.T, ctx context.Context, client *mcpsdk.ClientSession, handle string) {
	t.Helper()
	for _, intent := range []string{"start", "accept", "accept", "accept", "accept"} {
		result, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "session.submit", Arguments: map[string]any{
			"handle": handle,
			"intent": intent,
		}})
		require.NoError(t, err)
		require.False(t, result.IsError, "session.submit %s: %s", intent, studioMCPText(result))
	}
}

func corpusForgeState(t *testing.T, ctx context.Context, client *mcpsdk.ClientSession, handle string) (string, string) {
	t.Helper()
	inspect, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "session.inspect", Arguments: map[string]any{"handle": handle}})
	require.NoError(t, err)
	require.False(t, inspect.IsError, "session.inspect: %s", studioMCPText(inspect))
	var snapshot struct {
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal([]byte(studioMCPText(inspect)), &snapshot))

	lastError, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "session.world", Arguments: map[string]any{"handle": handle, "key": "last_error"}})
	require.NoError(t, err)
	require.False(t, lastError.IsError, "session.world: %s", studioMCPText(lastError))
	var value struct {
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal([]byte(studioMCPText(lastError)), &value))
	return snapshot.State, value.Value
}

func studioMCPText(result *mcpsdk.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	if text, ok := result.Content[0].(*mcpsdk.TextContent); ok {
		return text.Text
	}
	return ""
}
