package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// mcpTestCmd starts a studio MCP server subprocess and drives it with the Go MCP
// client. This gives developers a tight edit/test loop for `kitsoki mcp`
// changes without reloading an attached LLM client.
func mcpTestCmd() *cobra.Command {
	var (
		serverCommand string
		serverArgs    []string
		storiesDir    string
		workspace     string
		readOnly      bool
		timeout       time.Duration
		listTools     bool
		toolName      string
		toolArgsJSON  string
		callsJSON     string
	)
	cmd := &cobra.Command{
		Use:   "mcp-test",
		Short: "Smoke-test the studio MCP server over stdio",
		Long: `Start a kitsoki studio MCP server as a subprocess, initialize it with the
official Go MCP client, list tools, and call one or more tools.

By default this launches the current kitsoki executable with the 'mcp'
subcommand, then calls studio.ping and studio.handles. Pass --tool with
--tool-args to exercise a specific studio tool during development, or --calls
with a JSON array to run a handle-preserving workflow in one MCP client session.

Examples:
  kitsoki mcp-test --stories-dir ./stories
  kitsoki mcp-test --tool story.validate --tool-args '{"dir":"stories/bugfix"}'
  kitsoki mcp-test --calls '[{"tool":"session.new","args":{"story_path":"testdata/apps/cloak/app.yaml","key":"smoke"}},{"tool":"session.inspect","args":{"handle":"smoke"},"expect":{"structuredContent.state":"foyer"}}]'
  kitsoki mcp-test --calls '[{"tool":"session.inspect","args":{"handle":"smoke"},"save":{"notification_id":"structuredContent.notifications.0.id"}},{"tool":"session.teleport","args":{"handle":"smoke","notification_id":"${notification_id}"}}]'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return fmt.Errorf("mcp-test: --timeout must be positive")
			}
			if serverCommand == "" {
				exe, err := os.Executable()
				if err != nil {
					return fmt.Errorf("mcp-test: resolve current executable: %w", err)
				}
				serverCommand = exe
			}
			var toolArgs map[string]any
			if toolArgsJSON != "" {
				if err := json.Unmarshal([]byte(toolArgsJSON), &toolArgs); err != nil {
					return fmt.Errorf("mcp-test: --tool-args must be a JSON object: %w", err)
				}
			}
			var calls []studioMCPTestCall
			if callsJSON != "" {
				if toolName != "" {
					return fmt.Errorf("mcp-test: --calls cannot be combined with --tool")
				}
				if err := json.Unmarshal([]byte(callsJSON), &calls); err != nil {
					return fmt.Errorf("mcp-test: --calls must be a JSON array: %w", err)
				}
				if len(calls) == 0 {
					return fmt.Errorf("mcp-test: --calls must include at least one call")
				}
				for i, call := range calls {
					if call.Name == "" {
						return fmt.Errorf("mcp-test: --calls[%d].tool is required", i)
					}
					if call.Retries < 0 {
						return fmt.Errorf("mcp-test: --calls[%d].retries must be non-negative", i)
					}
					if call.IntervalMS < 0 {
						return fmt.Errorf("mcp-test: --calls[%d].interval_ms must be non-negative", i)
					}
				}
			}
			opts := studioMCPTestOptions{
				ServerCommand: serverCommand,
				ServerArgs:    studioMCPTestServerArgs(serverArgs, storiesDir, workspace, readOnly),
				ListTools:     listTools,
				ToolName:      toolName,
				ToolArgs:      toolArgs,
				Calls:         calls,
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			return runStudioMCPTest(ctx, opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&serverCommand, "server-command", "",
		"command to run as the studio MCP server (default: the current kitsoki executable)")
	cmd.Flags().StringArrayVar(&serverArgs, "server-arg", nil,
		"extra/override server argument; when set, these replace the default generated 'mcp' args")
	cmd.Flags().StringVar(&storiesDir, "stories-dir", "",
		"forwarded to the default server args as `kitsoki mcp --stories-dir <dir>`")
	cmd.Flags().StringVar(&workspace, "workspace", "",
		"forwarded to the default server args as `kitsoki mcp --workspace <dir-or-app.yaml>`")
	cmd.Flags().BoolVar(&readOnly, "read-only", false,
		"forwarded to the default server args as `kitsoki mcp --read-only`")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second,
		"overall timeout for server startup, initialize, and tool calls")
	cmd.Flags().BoolVar(&listTools, "list-tools", true,
		"include tools/list in the smoke run")
	cmd.Flags().StringVar(&toolName, "tool", "",
		"single tool to call instead of the default studio.ping + studio.handles calls")
	cmd.Flags().StringVar(&toolArgsJSON, "tool-args", "",
		"JSON object arguments for --tool")
	cmd.Flags().StringVar(&callsJSON, "calls", "",
		"JSON array of sequential tool calls: [{\"tool\":\"session.new\",\"args\":{...}}, ...]")
	return cmd
}

type studioMCPTestOptions struct {
	ServerCommand string              `json:"server_command,omitempty"`
	ServerArgs    []string            `json:"server_args,omitempty"`
	ListTools     bool                `json:"list_tools"`
	ToolName      string              `json:"tool,omitempty"`
	ToolArgs      map[string]any      `json:"tool_args,omitempty"`
	Calls         []studioMCPTestCall `json:"calls,omitempty"`
}

type studioMCPTestCall struct {
	Name           string            `json:"tool"`
	Args           map[string]any    `json:"args,omitempty"`
	Expect         map[string]any    `json:"expect,omitempty"`
	ExpectContains map[string]string `json:"expect_contains,omitempty"`
	ExpectExists   []string          `json:"expect_exists,omitempty"`
	Save           map[string]string `json:"save,omitempty"`
	Retries        int               `json:"retries,omitempty"`
	IntervalMS     int               `json:"interval_ms,omitempty"`
}

type studioMCPTestReport struct {
	OK       bool                  `json:"ok"`
	Server   []string              `json:"server"`
	Tools    []string              `json:"tools,omitempty"`
	ToolRuns []studioMCPToolReport `json:"tool_runs"`
}

type studioMCPToolReport struct {
	Name     string                 `json:"name"`
	IsError  bool                   `json:"is_error"`
	Attempts int                    `json:"attempts,omitempty"`
	Result   map[string]interface{} `json:"result"`
}

func studioMCPTestServerArgs(override []string, storiesDir, workspace string, readOnly bool) []string {
	if len(override) > 0 {
		return append([]string(nil), override...)
	}
	args := []string{"mcp"}
	if storiesDir != "" {
		args = append(args, "--stories-dir", storiesDir)
	}
	if workspace != "" {
		args = append(args, "--workspace", workspace)
	}
	if readOnly {
		args = append(args, "--read-only")
	}
	return args
}

func runStudioMCPTest(ctx context.Context, opts studioMCPTestOptions, out io.Writer) error {
	if opts.ServerCommand == "" {
		return fmt.Errorf("mcp-test: server command is required")
	}
	child := exec.CommandContext(ctx, opts.ServerCommand, opts.ServerArgs...)
	child.Stderr = os.Stderr

	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "kitsoki-mcp-test",
		Version: version,
	}, nil)
	cs, err := client.Connect(ctx, &mcpsdk.CommandTransport{Command: child}, nil)
	if err != nil {
		return fmt.Errorf("mcp-test: connect: %w", err)
	}
	defer func() { _ = cs.Close() }()

	report, err := runStudioMCPTestSession(ctx, cs, opts)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("mcp-test: encode report: %w", err)
	}
	if !report.OK {
		return fmt.Errorf("mcp-test: one or more tool calls returned errors")
	}
	return nil
}

func runStudioMCPTestSession(ctx context.Context, cs *mcpsdk.ClientSession, opts studioMCPTestOptions) (studioMCPTestReport, error) {
	report := studioMCPTestReport{
		OK:     true,
		Server: append([]string{opts.ServerCommand}, opts.ServerArgs...),
	}
	if opts.ListTools {
		res, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
		if err != nil {
			return report, fmt.Errorf("mcp-test: tools/list: %w", err)
		}
		for _, tool := range res.Tools {
			report.Tools = append(report.Tools, tool.Name)
		}
	}

	calls := []struct {
		name           string
		args           map[string]any
		expect         map[string]any
		expectContains map[string]string
		expectExists   []string
		save           map[string]string
		retries        int
		intervalMS     int
	}{
		{name: "studio.ping"},
		{name: "studio.handles"},
	}
	if opts.ToolName != "" {
		calls = []struct {
			name           string
			args           map[string]any
			expect         map[string]any
			expectContains map[string]string
			expectExists   []string
			save           map[string]string
			retries        int
			intervalMS     int
		}{{name: opts.ToolName, args: opts.ToolArgs}}
	} else if len(opts.Calls) > 0 {
		calls = make([]struct {
			name           string
			args           map[string]any
			expect         map[string]any
			expectContains map[string]string
			expectExists   []string
			save           map[string]string
			retries        int
			intervalMS     int
		}, 0, len(opts.Calls))
		for _, call := range opts.Calls {
			calls = append(calls, struct {
				name           string
				args           map[string]any
				expect         map[string]any
				expectContains map[string]string
				expectExists   []string
				save           map[string]string
				retries        int
				intervalMS     int
			}{name: call.Name, args: call.Args, expect: call.Expect, expectContains: call.ExpectContains, expectExists: call.ExpectExists, save: call.Save, retries: call.Retries, intervalMS: call.IntervalMS})
		}
	}
	vars := map[string]string{}
	for _, call := range calls {
		args, err := expandMCPTestValue(call.args, vars)
		if err != nil {
			return report, err
		}
		expect, err := expandMCPTestValue(call.expect, vars)
		if err != nil {
			return report, err
		}
		expectContains, err := expandMCPTestStringMap(call.expectContains, vars)
		if err != nil {
			return report, err
		}
		result, isError, attempts, err := runStudioMCPTestCall(ctx, cs, call.name, asStringAnyMap(args), asStringAnyMap(expect), expectContains, call.expectExists, call.retries, call.intervalMS)
		if err != nil {
			return report, err
		}
		if len(call.save) > 0 {
			if err := saveMCPTestVars(call.name, result, call.save, vars); err != nil {
				return report, err
			}
		}
		report.ToolRuns = append(report.ToolRuns, studioMCPToolReport{
			Name:     call.name,
			IsError:  isError,
			Attempts: attempts,
			Result:   result,
		})
		if isError {
			report.OK = false
		}
	}
	return report, nil
}

func runStudioMCPTestCall(ctx context.Context, cs *mcpsdk.ClientSession, name string, args map[string]any, expect map[string]any, expectContains map[string]string, expectExists []string, retries, intervalMS int) (map[string]interface{}, bool, int, error) {
	attempts := 0
	maxAttempts := retries + 1
	var lastResult map[string]interface{}
	var lastIsError bool
	var lastErr error
	for attempts < maxAttempts {
		attempts++
		res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      name,
			Arguments: args,
		})
		if err != nil {
			lastErr = fmt.Errorf("mcp-test: tools/call %s: %w", name, err)
		} else {
			lastIsError = res.IsError
			lastResult, lastErr = decodeMCPToolResult(name, res)
			if lastErr == nil && len(expect) > 0 {
				lastErr = assertMCPExpectations(name, lastResult, expect)
			}
			if lastErr == nil && len(expectContains) > 0 {
				lastErr = assertMCPContainsExpectations(name, lastResult, expectContains)
			}
			if lastErr == nil && len(expectExists) > 0 {
				lastErr = assertMCPExistsExpectations(name, lastResult, expectExists)
			}
			if lastErr == nil {
				return lastResult, lastIsError, attempts, nil
			}
		}
		if attempts >= maxAttempts {
			break
		}
		wait := time.Duration(intervalMS) * time.Millisecond
		if wait <= 0 {
			wait = 100 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return lastResult, lastIsError, attempts, ctx.Err()
		case <-time.After(wait):
		}
	}
	if lastErr != nil {
		return lastResult, lastIsError, attempts, lastErr
	}
	return lastResult, lastIsError, attempts, nil
}

func decodeMCPToolResult(name string, res *mcpsdk.CallToolResult) (map[string]interface{}, error) {
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("mcp-test: marshal %s result: %w", name, err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp-test: decode %s result: %w", name, err)
	}
	return result, nil
}

func assertMCPExpectations(name string, result map[string]interface{}, expect map[string]any) error {
	for path, want := range expect {
		got, ok := lookupDotPath(result, path)
		if !ok {
			return fmt.Errorf("mcp-test: %s expectation %q missing", name, path)
		}
		if !jsonEqual(got, want) {
			return fmt.Errorf("mcp-test: %s expectation %q: got %v, want %v", name, path, got, want)
		}
	}
	return nil
}

func assertMCPContainsExpectations(name string, result map[string]interface{}, expect map[string]string) error {
	for path, want := range expect {
		got, ok := lookupDotPath(result, path)
		if !ok {
			return fmt.Errorf("mcp-test: %s contains expectation %q missing", name, path)
		}
		gotString, ok := got.(string)
		if !ok {
			return fmt.Errorf("mcp-test: %s contains expectation %q: got %T, want string containing %q", name, path, got, want)
		}
		if !strings.Contains(gotString, want) {
			return fmt.Errorf("mcp-test: %s contains expectation %q: got %q, want containing %q", name, path, gotString, want)
		}
	}
	return nil
}

func assertMCPExistsExpectations(name string, result map[string]interface{}, paths []string) error {
	for _, path := range paths {
		if path == "" {
			return fmt.Errorf("mcp-test: %s exists expectation path is empty", name)
		}
		if _, ok := lookupDotPath(result, path); !ok {
			return fmt.Errorf("mcp-test: %s exists expectation %q missing", name, path)
		}
	}
	return nil
}

func lookupDotPath(root any, path string) (any, bool) {
	if path == "" {
		return root, true
	}
	cur := root
	for _, part := range strings.Split(path, ".") {
		switch typed := cur.(type) {
		case map[string]interface{}:
			next, ok := typed[part]
			if !ok {
				return nil, false
			}
			cur = next
		case []interface{}:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(typed) {
				return nil, false
			}
			cur = typed[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

func saveMCPTestVars(tool string, result map[string]interface{}, save map[string]string, vars map[string]string) error {
	for name, path := range save {
		if name == "" {
			return fmt.Errorf("mcp-test: %s save name is empty", tool)
		}
		value, ok := lookupDotPath(result, path)
		if !ok {
			return fmt.Errorf("mcp-test: %s save %q path %q missing", tool, name, path)
		}
		vars[name] = fmt.Sprint(value)
	}
	return nil
}

func expandMCPTestValue(v any, vars map[string]string) (any, error) {
	switch typed := v.(type) {
	case nil:
		return nil, nil
	case string:
		return expandMCPTestString(typed, vars)
	case map[string]interface{}:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			expanded, err := expandMCPTestValue(v, vars)
			if err != nil {
				return nil, err
			}
			out[k] = expanded
		}
		return out, nil
	case []interface{}:
		out := make([]any, 0, len(typed))
		for _, v := range typed {
			expanded, err := expandMCPTestValue(v, vars)
			if err != nil {
				return nil, err
			}
			out = append(out, expanded)
		}
		return out, nil
	default:
		return v, nil
	}
}

func expandMCPTestString(s string, vars map[string]string) (string, error) {
	var missing string
	out := mcpTestVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		if value, ok := vars[name]; ok {
			return value
		}
		missing = name
		return ""
	})
	if missing != "" {
		return "", fmt.Errorf("mcp-test: unknown saved value %q", missing)
	}
	return out, nil
}

func expandMCPTestStringMap(values map[string]string, vars map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for path, value := range values {
		expanded, err := expandMCPTestString(value, vars)
		if err != nil {
			return nil, err
		}
		out[path] = expanded
	}
	return out, nil
}

var mcpTestVarPattern = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)

func asStringAnyMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

func jsonEqual(got, want any) bool {
	var gotNorm any
	var wantNorm any
	gotBytes, gotErr := json.Marshal(got)
	wantBytes, wantErr := json.Marshal(want)
	if gotErr != nil || wantErr != nil {
		return reflect.DeepEqual(got, want)
	}
	if err := json.Unmarshal(gotBytes, &gotNorm); err != nil {
		return reflect.DeepEqual(got, want)
	}
	if err := json.Unmarshal(wantBytes, &wantNorm); err != nil {
		return reflect.DeepEqual(got, want)
	}
	return reflect.DeepEqual(gotNorm, wantNorm)
}
