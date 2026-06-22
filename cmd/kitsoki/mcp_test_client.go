package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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
  kitsoki mcp-test --calls '[{"tool":"session.new","args":{"story_path":"testdata/apps/cloak/app.yaml","key":"smoke"}},{"tool":"session.inspect","args":{"handle":"smoke"}}]'`,
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
	Name string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

type studioMCPTestReport struct {
	OK       bool                  `json:"ok"`
	Server   []string              `json:"server"`
	Tools    []string              `json:"tools,omitempty"`
	ToolRuns []studioMCPToolReport `json:"tool_runs"`
}

type studioMCPToolReport struct {
	Name    string                 `json:"name"`
	IsError bool                   `json:"is_error"`
	Result  map[string]interface{} `json:"result"`
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
		name string
		args map[string]any
	}{
		{name: "studio.ping"},
		{name: "studio.handles"},
	}
	if opts.ToolName != "" {
		calls = []struct {
			name string
			args map[string]any
		}{{name: opts.ToolName, args: opts.ToolArgs}}
	} else if len(opts.Calls) > 0 {
		calls = make([]struct {
			name string
			args map[string]any
		}, 0, len(opts.Calls))
		for _, call := range opts.Calls {
			calls = append(calls, struct {
				name string
				args map[string]any
			}{name: call.Name, args: call.Args})
		}
	}
	for _, call := range calls {
		res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      call.name,
			Arguments: call.args,
		})
		if err != nil {
			return report, fmt.Errorf("mcp-test: tools/call %s: %w", call.name, err)
		}
		raw, err := json.Marshal(res)
		if err != nil {
			return report, fmt.Errorf("mcp-test: marshal %s result: %w", call.name, err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal(raw, &result); err != nil {
			return report, fmt.Errorf("mcp-test: decode %s result: %w", call.name, err)
		}
		report.ToolRuns = append(report.ToolRuns, studioMCPToolReport{
			Name:    call.name,
			IsError: res.IsError,
			Result:  result,
		})
		if res.IsError {
			report.OK = false
		}
	}
	return report, nil
}
