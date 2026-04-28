package host_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hally/internal/host"
)

// fakeOneShotMCPBin returns the path to testdata/fake-oneshot-mcp.sh.
func fakeOneShotMCPBin(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(thisFile), "testdata", "fake-oneshot-mcp.sh")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("fake-oneshot-mcp.sh not found at %s: %v", path, err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("fake-oneshot-mcp.sh is not executable")
	}
	return path
}

// TestOracleAskWithMCP_RegisteredAsBuiltin verifies the handler is wired in.
func TestOracleAskWithMCP_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.oracle.ask_with_mcp"); !ok {
		t.Fatal("host.oracle.ask_with_mcp was not registered by RegisterBuiltins")
	}
}

// TestOracleAskWithMCP_NoServers behaves identically to host.oracle.ask when
// mcp_servers is missing — no --mcp-config is passed, prompt is echoed back.
func TestOracleAskWithMCP_NoServers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("hello {{ args.who }}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path": promptPath,
		"who":         "world",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	out, _ := res.Data["stdout"].(string)
	if !strings.Contains(out, "hello world") {
		t.Fatalf("stdout missing rendered prompt: %q", out)
	}
	// No --mcp-config should be passed → fake binary echoes mcp_config= empty.
	if !strings.Contains(out, "mcp_config=\n") && !strings.HasSuffix(strings.TrimSpace(out), "mcp_config=") {
		// Allow either "mcp_config=\n" or trailing "mcp_config=" without newline.
		if !strings.Contains(out, "mcp_config=\n") {
			t.Fatalf("expected empty mcp_config in stdout, got %q", out)
		}
	}
}

// TestOracleAskWithMCP_ServersMaterialized verifies that mcp_servers is written
// to a temp --mcp-config JSON and passed to the binary.
func TestOracleAskWithMCP_ServersMaterialized(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("propose a fix"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	mcpServers := map[string]any{
		"wiggum": map[string]any{
			"command": "python3",
			"args":    []any{"tools/loopy/wiggum-mcp.py", "--schema", "schemas/03.json"},
		},
	}

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path":   promptPath,
		"mcp_servers":   mcpServers,
		"output_format": "json",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}

	// JSON parse exposed via stdout_json.
	parsed, ok := res.Data["stdout_json"].(map[string]any)
	if !ok {
		t.Fatalf("stdout_json missing or wrong shape: %T %v", res.Data["stdout_json"], res.Data["stdout_json"])
	}
	mcpCfgPath, _ := parsed["mcp_config_path"].(string)
	if mcpCfgPath == "" {
		t.Fatal("expected mcp_config_path to be set; --mcp-config was not passed")
	}

	// The fake binary captured the body — assert the wrapping under "mcpServers".
	body, ok := parsed["mcp_body"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_body missing: %v", parsed["mcp_body"])
	}
	servers, ok := body["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level mcpServers wrapping, got: %v", body)
	}
	wiggum, ok := servers["wiggum"].(map[string]any)
	if !ok {
		t.Fatalf("wiggum entry missing: %v", servers)
	}
	if wiggum["command"] != "python3" {
		t.Fatalf("wiggum.command = %v, want python3", wiggum["command"])
	}
}

// TestOracleAskWithMCP_TempFileCleanedUp verifies the temp config file is
// removed after the handler returns.
func TestOracleAskWithMCP_TempFileCleanedUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path":   promptPath,
		"mcp_servers":   map[string]any{"wiggum": map[string]any{"command": "true"}},
		"output_format": "json",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	parsed, _ := res.Data["stdout_json"].(map[string]any)
	mcpCfgPath, _ := parsed["mcp_config_path"].(string)
	if mcpCfgPath == "" {
		t.Fatal("expected mcp_config_path")
	}
	if _, err := os.Stat(mcpCfgPath); !os.IsNotExist(err) {
		t.Fatalf("temp mcp config %q not cleaned up: %v", mcpCfgPath, err)
	}
}

// TestOracleAskWithMCP_PromptAlias accepts `prompt:` as alias for `prompt_path:`.
func TestOracleAskWithMCP_PromptAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("via prompt alias"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt": promptPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	out, _ := res.Data["stdout"].(string)
	if !strings.Contains(out, "via prompt alias") {
		t.Fatalf("stdout missing rendered prompt: %q", out)
	}
}

// TestOracleAskWithMCP_BinaryMissing returns Result.Error.
func TestOracleAskWithMCP_BinaryMissing(t *testing.T) {
	t.Setenv(host.OracleBinEnv, "/definitely/does/not/exist/claude")

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path": promptPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error when binary is missing")
	}
}

// TestOracleAskWithMCP_NonZeroExit propagates exit_code and Result.Error.
func TestOracleAskWithMCP_NonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("FAIL on purpose"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path": promptPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("Result.Error should be set on non-zero exit")
	}
	if code, _ := res.Data["exit_code"].(int); code == 0 {
		t.Fatal("exit_code should be non-zero")
	}
}

// TestOracleAskWithMCP_AutoAttachesValidatorForSchema verifies that setting
// `schema:` without an explicit mcp_servers.validator entry causes the
// handler to materialize a validator entry pointing at the running binary.
// We assert by reading back the temp --mcp-config that the fake binary
// echoes via the JSON envelope.
func TestOracleAskWithMCP_AutoAttachesValidatorForSchema(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))
	// Pretend hally lives at /usr/local/bin/hally so we can assert the
	// validator entry's command field deterministically.
	t.Setenv("HALLY_BIN", "/usr/local/bin/hally")

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("propose a fix"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path":   promptPath,
		"schema":        schemaPath,
		"output_format": "json",
	})
	require.NoError(t, err)
	require.Empty(t, res.Error)

	parsed, ok := res.Data["stdout_json"].(map[string]any)
	require.True(t, ok, "stdout_json missing: %v", res.Data["stdout_json"])
	body, _ := parsed["mcp_body"].(map[string]any)
	servers, _ := body["mcpServers"].(map[string]any)
	v, ok := servers["validator"].(map[string]any)
	require.True(t, ok, "validator entry missing: %v", servers)
	assert.Equal(t, "/usr/local/bin/hally", v["command"])
	args, _ := v["args"].([]any)
	require.GreaterOrEqual(t, len(args), 3)
	assert.Equal(t, "mcp-validator", args[0])
	assert.Equal(t, "--schema", args[1])
	assert.Equal(t, schemaPath, args[2])
}

// TestOracleAskWithMCP_NoAutoAttachWhenMcpServersValidatorPresent verifies
// that if the caller already provides an mcp_servers.validator entry, the
// handler leaves it alone (no overwrite).
func TestOracleAskWithMCP_NoAutoAttachWhenMcpServersValidatorPresent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))
	t.Setenv("HALLY_BIN", "/usr/local/bin/hally")

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("x"), 0o644))
	schemaPath := filepath.Join(dir, "schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type":"object"}`), 0o644))

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path": promptPath,
		"schema":      schemaPath,
		"mcp_servers": map[string]any{
			"validator": map[string]any{
				"command": "/opt/custom-validator",
				"args":    []any{"--mode", "strict"},
			},
		},
		"output_format": "json",
	})
	require.NoError(t, err)
	require.Empty(t, res.Error)

	parsed, ok := res.Data["stdout_json"].(map[string]any)
	require.True(t, ok)
	body, _ := parsed["mcp_body"].(map[string]any)
	servers, _ := body["mcpServers"].(map[string]any)
	v, _ := servers["validator"].(map[string]any)
	require.Equal(t, "/opt/custom-validator", v["command"], "caller-provided validator must not be overwritten")
}

// TestOracleAskWithMCP_NoSchemaMeansNoValidator verifies that without a
// schema arg, no validator entry appears (back-compat with existing callers).
func TestOracleAskWithMCP_NoSchemaMeansNoValidator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("x"), 0o644))

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path":   promptPath,
		"output_format": "json",
	})
	require.NoError(t, err)
	parsed, _ := res.Data["stdout_json"].(map[string]any)
	require.NotNil(t, parsed)
	// mcp_config_path is empty when no servers were attached.
	cfg, _ := parsed["mcp_config_path"].(string)
	assert.Empty(t, cfg, "no schema arg means no --mcp-config should be passed")
}

// TestOracleAskWithMCP_SchemaResolvedAgainstAppDir verifies that a relative
// schema path is resolved against HALLY_APP_DIR (mirroring resolvePromptPath).
func TestOracleAskWithMCP_SchemaResolvedAgainstAppDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))
	t.Setenv("HALLY_BIN", "/usr/local/bin/hally")

	appDir := t.TempDir()
	t.Setenv(host.AppDirEnv, appDir)

	schemasDir := filepath.Join(appDir, "schemas")
	require.NoError(t, os.MkdirAll(schemasDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(schemasDir, "p.json"), []byte(`{"type":"object"}`), 0o644))

	promptPath := filepath.Join(appDir, "p.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("x"), 0o644))

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path":   promptPath,
		"schema":        "schemas/p.json", // relative
		"output_format": "json",
	})
	require.NoError(t, err)
	require.Empty(t, res.Error)
	parsed, _ := res.Data["stdout_json"].(map[string]any)
	body, _ := parsed["mcp_body"].(map[string]any)
	servers, _ := body["mcpServers"].(map[string]any)
	v, _ := servers["validator"].(map[string]any)
	args, _ := v["args"].([]any)
	require.GreaterOrEqual(t, len(args), 3)
	assert.Equal(t, filepath.Join(appDir, "schemas/p.json"), args[2])
}

// TestOracleAskWithMCP_SubmittedBindCapturesValidatedPayload verifies the
// canonical-payload side channel: when the auto-attached validator captures
// a submit() to its --output file, the host handler reads it back and
// exposes it as Result.Data["submitted"], which authors bind to e.g.
// `proposal: submitted` instead of relying on the LLM's stdout text.
func TestOracleAskWithMCP_SubmittedBindCapturesValidatedPayload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))
	t.Setenv("HALLY_BIN", "/usr/local/bin/hally")

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type":"object"}`), 0o644))

	// The fake binary reads SIMULATE_SUBMIT=<json> from the prompt and
	// writes that JSON to the validator's --output path, mimicking what
	// claude does when it makes a successful submit() call.
	promptPath := filepath.Join(dir, "p.md")
	require.NoError(t, os.WriteFile(promptPath, []byte(
		`propose a fix SIMULATE_SUBMIT={"summary":"fix double-Close","confidence":"high","files_changed":["a.go"]}`,
	), 0o644))

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path":   promptPath,
		"schema":        schemaPath,
		"output_format": "json",
	})
	require.NoError(t, err)
	require.Empty(t, res.Error, "no error expected, got: %s", res.Error)

	submitted, ok := res.Data["submitted"].(map[string]any)
	require.True(t, ok, "Result.Data[\"submitted\"] missing or wrong shape: %T %v",
		res.Data["submitted"], res.Data["submitted"])
	assert.Equal(t, "fix double-Close", submitted["summary"])
	assert.Equal(t, "high", submitted["confidence"])
	files, _ := submitted["files_changed"].([]any)
	require.Len(t, files, 1)
	assert.Equal(t, "a.go", files[0])
}

// TestOracleAskWithMCP_NoSubmittedKeyWhenLLMNeverCalledSubmit verifies that
// if the LLM never makes a successful submit, Result.Data["submitted"] is
// absent — letting on_error: routing or guards observe "validator never
// captured" as a missing-binding condition.
func TestOracleAskWithMCP_NoSubmittedKeyWhenLLMNeverCalledSubmit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))
	t.Setenv("HALLY_BIN", "/usr/local/bin/hally")

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type":"object"}`), 0o644))
	promptPath := filepath.Join(dir, "p.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("just a prompt, no SIMULATE_SUBMIT"), 0o644))

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path":   promptPath,
		"schema":        schemaPath,
		"output_format": "json",
	})
	require.NoError(t, err)
	require.Empty(t, res.Error)

	_, present := res.Data["submitted"]
	assert.False(t, present, "submitted key must be absent when validator never captured anything")
}

// TestOracleAskWithMCP_MissingSchemaFile errors cleanly.
func TestOracleAskWithMCP_MissingSchemaFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("x"), 0o644))

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path": promptPath,
		"schema":      filepath.Join(dir, "missing.json"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.Error)
	assert.Contains(t, res.Error, "missing.json")
}

// TestOracleAskWithMCP_StdoutJSONParseError surfaces a parse-error sentinel
// when output_format=json and the binary returns non-JSON.
func TestOracleAskWithMCP_StdoutJSONParseError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	// Use the plain fake-oneshot.sh which always echoes plain text — no JSON.
	t.Setenv(host.OracleBinEnv, fakeOneShotBin(t))

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := host.OracleAskWithMCPHandler(context.Background(), map[string]any{
		"prompt_path":   promptPath,
		"output_format": "json",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if _, ok := res.Data["stdout_json"]; ok {
		t.Fatal("stdout_json should be absent when parse fails")
	}
	if _, ok := res.Data["stdout_json_parse_error"].(string); !ok {
		t.Fatal("expected stdout_json_parse_error sentinel")
	}
	// Sanity: the raw stdout is still available.
	out, _ := res.Data["stdout"].(string)
	if !strings.Contains(out, "not json") {
		t.Fatalf("stdout missing prompt echo: %q", out)
	}

	// Sanity: ensure the stdout is not accidentally valid JSON via some quirk.
	var any any
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &any) == nil {
		t.Fatalf("test premise broken: stdout %q is valid JSON", out)
	}
}
