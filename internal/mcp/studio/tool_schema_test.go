package studio_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tool_schema_test.go — guards the studio MCP tool-list against the schema
// shape that silently strands every kitsoki tool from an attached coding agent.
//
// Root cause (dogfood, 2026-06-23): host.run's `timeout` field is a polymorphic
// `any` (number-of-seconds OR a Go duration string). The jsonschema reflector
// emits an `any` field as the bare boolean schema `true`. Claude Code's
// tools/list validator rejects a non-object property schema with
//
//	tools/list failed ([{ "path": ["tools", 1, "inputSchema", "properties",
//	  "timeout"], "message": "Invalid input" }]); retrying once
//	Failed to fetch tools
//
// and then drops the ENTIRE 27-tool list for the session — so an agent sees zero
// mcp__kitsoki__ tools and either narrates "tool not available" or confabulates
// fake tool output. One `any` field stranded the whole studio surface.

// TestToolSchemas_NoBooleanPropertySchemas walks every registered tool's input
// schema and asserts no property is a boolean schema (`true`/`false`). A boolean
// is a valid JSON Schema ("match anything") but Claude Code's stricter validator
// rejects it, and the failure mode is total (all tools dropped), so this is a
// hard load-time invariant for the studio surface — not a host.run-only check.
func TestToolSchemas_NoBooleanPropertySchemas(t *testing.T) {
	ctx := context.Background()
	cs := newStudioHostRunner(ctx, t)

	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err, "tools/list")
	require.NotEmpty(t, res.Tools, "expected the studio surface to expose tools")

	for _, tool := range res.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		require.NoErrorf(t, err, "marshal %q inputSchema", tool.Name)
		var m map[string]any
		require.NoErrorf(t, json.Unmarshal(raw, &m), "unmarshal %q inputSchema", tool.Name)

		props, _ := m["properties"].(map[string]any)
		for pn, ps := range props {
			_, isBool := ps.(bool)
			assert.Falsef(t, isBool,
				"tool %q property %q has a boolean schema (%v): Claude Code's tools/list "+
					"validator rejects non-object property schemas and drops the ENTIRE tool "+
					"list for the session. Give the field an explicit object schema (see "+
					"hostRunInputSchema for the `any`-field fix).",
				tool.Name, pn, ps)
		}
	}
}

// TestHostRun_TimeoutSchemaIsObject pins the specific fix: host.run's `timeout`
// property is a JSON object schema, never the bare boolean the reflector would
// otherwise emit for its `any` Go type.
func TestHostRun_TimeoutSchemaIsObject(t *testing.T) {
	ctx := context.Background()
	cs := newStudioHostRunner(ctx, t)

	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err, "tools/list")

	var found bool
	for _, tool := range res.Tools {
		if tool.Name != "host.run" {
			continue
		}
		found = true
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		props, ok := m["properties"].(map[string]any)
		require.True(t, ok, "host.run inputSchema has properties")
		timeout, ok := props["timeout"]
		require.True(t, ok, "host.run exposes a timeout property")
		_, isObject := timeout.(map[string]any)
		assert.Truef(t, isObject, "host.run timeout schema must be an object, got %T (%v)", timeout, timeout)
	}
	require.True(t, found, "host.run tool was not registered")
}
