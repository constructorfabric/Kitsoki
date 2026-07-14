package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kitsokimcp "kitsoki/internal/mcp"
)

func TestValidatorRejectsSchemaValidInformationCollapse(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "captured.json")
	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		OutputPath: outPath,
	})
	defer done()

	richSummary := strings.Repeat("Detailed evidence links the failing retry loop to the captured validator state and preserves the proposed repair. ", 8)
	first, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "submit",
		Arguments: map[string]any{
			"summary":       richSummary,
			"files_changed": []string{"internal/mcp/validator.go"},
			// confidence intentionally omitted: structurally invalid, substantively rich.
		},
	})
	require.NoError(t, err)
	require.True(t, first.IsError)

	placeholder, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "submit",
		Arguments: map[string]any{
			"summary":       "placeholder",
			"confidence":    "high",
			"files_changed": []string{"todo"},
		},
	})
	require.NoError(t, err)
	require.True(t, placeholder.IsError, "schema-valid placeholder must not erase a much richer attempt")
	text := placeholder.Content[0].(*mcpsdk.TextContent).Text
	assert.Contains(t, text, "payload information regressed")
	assert.Contains(t, text, "session maximum")
	_, statErr := os.Stat(outPath)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "rejected placeholder must not reach the side channel")

	accepted, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "submit",
		Arguments: map[string]any{
			"summary":       richSummary,
			"confidence":    "high",
			"files_changed": []string{"internal/mcp/validator.go"},
		},
	})
	require.NoError(t, err)
	require.False(t, accepted.IsError)
	assert.Equal(t, kitsokimcp.OutcomeSuccess, srv.Outcome())
}

func TestValidatorInformationPolicyIsConfigurable(t *testing.T) {
	zero := 0.0
	highFloor := 1_000_000.0
	for _, tc := range []struct {
		name string
		cfg  kitsokimcp.ValidatorConfig
	}{
		{name: "ratio zero disables", cfg: kitsokimcp.ValidatorConfig{MinInformationRatio: &zero}},
		{name: "high floor defers", cfg: kitsokimcp.ValidatorConfig{MinInformationBits: &highFloor}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, _, done := connectValidatorWithCfg(t, tc.cfg)
			defer done()
			_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
				Name: "submit", Arguments: map[string]any{"summary": strings.Repeat("substantive prior attempt ", 30)},
			})
			require.NoError(t, err)
			result, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "submit", Arguments: validPayload()})
			require.NoError(t, err)
			assert.False(t, result.IsError)
		})
	}
}

func TestValidatorInformationMaximumPersistsAcrossResumedProcesses(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	rich := map[string]any{"summary": strings.Repeat("preserve this detailed diagnostic evidence ", 30)}
	cs1, _, done1 := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{StateFilePath: statePath})
	_, err := cs1.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "submit", Arguments: rich})
	require.NoError(t, err)
	done1()

	raw, err := os.ReadFile(statePath)
	require.NoError(t, err)
	var persisted map[string]any
	require.NoError(t, json.Unmarshal(raw, &persisted))
	assert.Greater(t, persisted["max_information_bits"].(float64), kitsokimcp.DefaultMinInformationBits)

	cs2, _, done2 := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{StateFilePath: statePath})
	defer done2()
	result, err := cs2.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "submit", Arguments: validPayload()})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcpsdk.TextContent).Text, "payload information regressed")
}
