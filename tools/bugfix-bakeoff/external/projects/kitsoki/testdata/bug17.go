package host_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

// TestRepro_RecoveredCodeBlockMustSatisfySchema is an independent oracle for
// the recovered-code-block path in host.agent.decide.
func TestRepro_RecoveredCodeBlockMustSatisfySchema(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "verdict.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{
  "type":"object",
  "properties":{"verdict":{"type":"string","minLength":1}},
  "required":["verdict"]
}`), 0o644))
	ctx := host.WithClaudeRunner(context.Background(), func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		return host.ClaudeRun{Stdout: "```json\n{}\n```\n", ExitCode: 0}, nil
	})
	res, err := host.AgentDecideHandler(ctx, map[string]any{"prompt": "decide", "schema": schemaPath})
	require.NoError(t, err)
	require.NotEmpty(t, res.Error, "a schema-invalid recovered code block must not take the success arc")
}
