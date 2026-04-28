package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hallymcp "hally/internal/mcp"
)

func osStat(p string) (os.FileInfo, error)    { return os.Stat(p) }
func osReadFile(p string) ([]byte, error)     { return os.ReadFile(p) }
func isNotExist(err error) bool               { return errors.Is(err, fs.ErrNotExist) }

// fixProposalSchema is a wiggum-style schema for a phase 3 "fix proposal"
// artifact. The required fields and enum constraints are typical of the
// shapes the bug-fix room will throw at the validator.
var fixProposalSchema = []byte(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["summary", "confidence", "files_changed"],
  "properties": {
    "summary":    { "type": "string", "minLength": 10 },
    "confidence": { "type": "string", "enum": ["low", "medium", "high"] },
    "files_changed": {
      "type":     "array",
      "minItems": 1,
      "items":    { "type": "string" }
    }
  }
}`)

// connectValidator wires up an in-process client + server pair using
// InMemoryTransports so tests can exercise tool calls without spawning a
// subprocess.
func connectValidator(t *testing.T, schema []byte) (*mcpsdk.ClientSession, func()) {
	t.Helper()
	srv, err := hallymcp.NewValidatorServer(hallymcp.ValidatorConfig{SchemaJSON: schema})
	require.NoError(t, err)

	clientT, serverT := mcpsdk.NewInMemoryTransports()

	ctx := context.Background()
	go func() {
		if _, err := srv.Connect(ctx, serverT, nil); err != nil {
			t.Logf("server connect error: %v", err)
		}
	}()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "test-client",
		Version: "0",
	}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)

	return cs, func() {
		_ = cs.Close()
	}
}

func TestValidator_ListsSubmitTool(t *testing.T) {
	cs, done := connectValidator(t, fixProposalSchema)
	defer done()

	res, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, res.Tools, 1)
	assert.Equal(t, "submit", res.Tools[0].Name)
	require.NotNil(t, res.Tools[0].InputSchema)

	// The tool's InputSchema is the schema the validator was constructed with.
	// Confirm a top-level required key shows through.
	rawSchema, err := json.Marshal(res.Tools[0].InputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(rawSchema), `"required"`)
	assert.Contains(t, string(rawSchema), `"summary"`)
}

func TestValidator_AcceptsValidPayload(t *testing.T) {
	cs, done := connectValidator(t, fixProposalSchema)
	defer done()

	args := map[string]any{
		"summary":       "Replace double-Close on the rpc client connection.",
		"confidence":    "high",
		"files_changed": []string{"internal/rpc/client.go"},
	}
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: args,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "valid payload must not be marked as error")
	require.NotEmpty(t, res.Content)
	textContent, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "OK:")
	assert.Contains(t, textContent.Text, "captured")
}

// TestValidator_WritesOutputOnSuccessfulSubmit verifies that the
// side-channel --output file gets written atomically with the submitted
// JSON when the payload passes validation.
func TestValidator_WritesOutputOnSuccessfulSubmit(t *testing.T) {
	dir := t.TempDir()
	outPath := dir + "/captured.json"

	srv, err := hallymcp.NewValidatorServer(hallymcp.ValidatorConfig{
		SchemaJSON: fixProposalSchema,
		OutputPath: outPath,
	})
	require.NoError(t, err)

	clientT, serverT := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _, _ = srv.Connect(ctx, serverT, nil) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)
	defer cs.Close()

	// File must NOT exist yet.
	_, err = osStat(outPath)
	require.True(t, isNotExist(err), "output file must not exist before submit")

	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "submit",
		Arguments: map[string]any{
			"summary":       "fix double-Close on rpc client",
			"confidence":    "high",
			"files_changed": []string{"a.go"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Read back and verify it's valid JSON matching what we sent.
	raw, err := osReadFile(outPath)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "high", got["confidence"])
	assert.Equal(t, "fix double-Close on rpc client", got["summary"])
}

// TestValidator_DoesNotWriteOutputOnInvalidSubmit verifies that a
// validation failure leaves the side-channel file untouched.
func TestValidator_DoesNotWriteOutputOnInvalidSubmit(t *testing.T) {
	dir := t.TempDir()
	outPath := dir + "/captured.json"

	srv, err := hallymcp.NewValidatorServer(hallymcp.ValidatorConfig{
		SchemaJSON: fixProposalSchema,
		OutputPath: outPath,
	})
	require.NoError(t, err)

	clientT, serverT := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _, _ = srv.Connect(ctx, serverT, nil) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)
	defer cs.Close()

	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "submit",
		Arguments: map[string]any{
			"summary":    "missing files_changed",
			"confidence": "high",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)

	_, err = osStat(outPath)
	require.True(t, isNotExist(err), "output file must not exist after a rejected submit")
}

// TestValidator_LastSuccessfulSubmitWins covers the case where the LLM
// makes one bad submit, sees the error, then a good submit. The
// side-channel must hold the second (valid) payload.
func TestValidator_LastSuccessfulSubmitWins(t *testing.T) {
	dir := t.TempDir()
	outPath := dir + "/captured.json"

	srv, err := hallymcp.NewValidatorServer(hallymcp.ValidatorConfig{
		SchemaJSON: fixProposalSchema,
		OutputPath: outPath,
	})
	require.NoError(t, err)

	clientT, serverT := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _, _ = srv.Connect(ctx, serverT, nil) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)
	defer cs.Close()

	// First call: invalid (missing files_changed).
	r1, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: map[string]any{"summary": "bad attempt", "confidence": "low"},
	})
	require.NoError(t, err)
	require.True(t, r1.IsError)

	// Second call: valid.
	r2, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "submit",
		Arguments: map[string]any{
			"summary":       "good attempt now with all required fields",
			"confidence":    "medium",
			"files_changed": []string{"x.go"},
		},
	})
	require.NoError(t, err)
	require.False(t, r2.IsError)

	raw, err := osReadFile(outPath)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "medium", got["confidence"])
}

func TestValidator_RejectsMissingRequired(t *testing.T) {
	cs, done := connectValidator(t, fixProposalSchema)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "submit",
		Arguments: map[string]any{
			"summary":    "long enough summary text here",
			"confidence": "high",
			// files_changed missing
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "missing required field must be marked as error")
	require.NotEmpty(t, res.Content)

	textContent, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "schema validation failed")
	assert.Contains(t, strings.ToLower(textContent.Text), "files_changed")
}

func TestValidator_RejectsBadEnum(t *testing.T) {
	cs, done := connectValidator(t, fixProposalSchema)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "submit",
		Arguments: map[string]any{
			"summary":       "long enough summary text here",
			"confidence":    "extreme", // not in enum
			"files_changed": []string{"x.go"},
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	textContent, _ := res.Content[0].(*mcpsdk.TextContent)
	assert.Contains(t, textContent.Text, "/confidence")
	// The v6 library renders enum failures as "value must be one of …".
	assert.Contains(t, strings.ToLower(textContent.Text), "must be one of")
}

func TestValidator_RejectsAdditionalProperty(t *testing.T) {
	cs, done := connectValidator(t, fixProposalSchema)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "submit",
		Arguments: map[string]any{
			"summary":       "long enough summary text here",
			"confidence":    "high",
			"files_changed": []string{"x.go"},
			"unexpected":    "should be rejected",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	textContent, _ := res.Content[0].(*mcpsdk.TextContent)
	// jsonschema/v6 phrases this as "additional properties" or "not allowed"
	// depending on version; check for the key.
	assert.Contains(t, textContent.Text, "unexpected")
}

func TestValidator_RejectsNonObjectSchema(t *testing.T) {
	_, err := hallymcp.NewValidatorServer(hallymcp.ValidatorConfig{
		SchemaJSON: []byte(`{"type": "array"}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `must be "object"`)
}

func TestValidator_CustomToolName(t *testing.T) {
	srv, err := hallymcp.NewValidatorServer(hallymcp.ValidatorConfig{
		SchemaJSON: fixProposalSchema,
		ToolName:   "submit_phase_3",
	})
	require.NoError(t, err)

	clientT, serverT := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _, _ = srv.Connect(ctx, serverT, nil) }()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)
	defer cs.Close()

	res, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, res.Tools, 1)
	assert.Equal(t, "submit_phase_3", res.Tools[0].Name)
}
