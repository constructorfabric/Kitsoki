// Package host — per-verb fake helpers for flow tests.
//
// The five new agent verbs (extract, decide, ask, task, converse) each get a
// factory function that returns a scripted ClaudeRunner stub. Flow tests inject
// these via WithClaudeRunner so the handler under test receives controlled
// outputs without spawning real subprocesses.
//
// Pattern matches the existing stubAgentRunner / stubOneShotRunner shape in
// agent_test.go. Each fake inspects the cli args it receives (specifically
// --append-system-prompt, --model, --allowedTools) so tests can assert that
// the handler threaded the agent fields through correctly.
//
// Usage in a test:
//
//	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("verdict text"))
//	res, err := host.AgentDecideHandler(ctx, args)
package host

import (
	"context"
	"encoding/json"
	"os"
	"strings"
)

// FakeExtract returns a ClaudeRunner that yields result as its stdout.
// Tests that need to verify allowedTools / system-prompt threading should
// inspect the args slice passed to the runner.
func FakeExtract(result string) ClaudeRunner {
	return makeFakeRunner(result)
}

// FakeDecide returns a ClaudeRunner that yields result as its stdout.
func FakeDecide(result string) ClaudeRunner {
	return makeFakeRunner(result)
}

// FakeAsk returns a ClaudeRunner that yields result as its stdout.
func FakeAsk(result string) ClaudeRunner {
	return makeFakeRunner(result)
}

// FakeTask returns a ClaudeRunner that yields result as its stdout.
func FakeTask(result string) ClaudeRunner {
	return makeFakeRunner(result)
}

// FakeConverse returns a ClaudeRunner that yields result as its stdout.
func FakeConverse(result string) ClaudeRunner {
	return makeFakeRunner(result)
}

// FakeExtractWithMeta returns a ClaudeRunner that echoes metadata about the
// flags it received in its reply. This lets tests assert that --allowedTools,
// --append-system-prompt, and --model were forwarded correctly for extract calls.
//
// The reply format is:
//
//	RESULT:<result> system=[<sp>] model=[<m>] tools=[<csv>]
//
// where each bracket pair is present only when the corresponding flag was set.
func FakeExtractWithMeta(result string) ClaudeRunner {
	return fakeRunnerWithMeta(result)
}

// FakeDecideWithMeta returns a ClaudeRunner that echoes metadata about the
// flags it received in its reply. This lets tests assert that --allowedTools,
// --append-system-prompt, and --model were forwarded correctly.
//
// The reply format is:
//
//	RESULT:<result> system=[<sp>] model=[<m>] tools=[<csv>]
//
// where each bracket pair is present only when the corresponding flag was set.
func FakeDecideWithMeta(result string) ClaudeRunner {
	return fakeRunnerWithMeta(result)
}

// FakeAskWithMeta is the equivalent of FakeDecideWithMeta for ask calls.
func FakeAskWithMeta(result string) ClaudeRunner {
	return fakeRunnerWithMeta(result)
}

// FakeExtractJSON returns a ClaudeRunner whose stdout is the JSON encoding of
// v. It also simulates the kitsoki mcp-validator behaviour: when the args
// contain --mcp-config, the runner parses the config file to find the
// --output path and writes the JSON payload there. This makes FakeExtractJSON
// work correctly with the M5-updated LLM tier that reads submitted output from
// the validator tempfile rather than from stdout.
func FakeExtractJSON(v any) ClaudeRunner {
	b, _ := json.Marshal(v)
	payload := string(b)
	return func(_ context.Context, args []string, _, _ string) (ClaudeRun, error) {
		// Simulate the mcp-validator writing the submitted JSON to the output file.
		if outputPath := fakeExtractSubmitPath(args); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(payload), 0o600)
		}
		return ClaudeRun{Stdout: payload}, nil
	}
}

// fakeExtractSubmitPath is an alias for ParseMCPConfigSubmitOutput for use
// within this package.
func fakeExtractSubmitPath(args []string) string {
	return ParseMCPConfigSubmitOutput(args)
}

// ParseMCPConfigSubmitOutput reads an MCP config JSON file and returns the
// --output argument passed to the validator MCP server. Exported so tests
// outside this package can simulate the mcp-validator write in custom runners.
func ParseMCPConfigSubmitOutput(args []string) string {
	for i, a := range args {
		if a == "--mcp-config" && i+1 < len(args) {
			// Scan every config flag: dispatches can carry several
			// (contract/bash/operator-ask + validator) and only the
			// validator's declares an --output.
			if out := parseMCPConfigFile(args[i+1]); out != "" {
				return out
			}
		}
	}
	return ""
}

// parseMCPConfigFile reads an MCP config JSON file and returns the
// --output argument passed to the validator MCP server.
func parseMCPConfigFile(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	var cfg struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	for _, server := range cfg.MCPServers {
		for i, a := range server.Args {
			if a == "--output" && i+1 < len(server.Args) {
				return server.Args[i+1]
			}
		}
	}
	return ""
}

// FakeDecideJSON returns a ClaudeRunner whose stdout is the JSON encoding of v.
func FakeDecideJSON(v any) ClaudeRunner {
	b, _ := json.Marshal(v)
	return makeFakeRunner(string(b))
}

// makeFakeRunner returns a ClaudeRunner that always replies with result.
// When --mcp-config is present it also simulates the validator submit by
// writing result as the captured payload, so the safety-net check in
// buildDecideResult (model exited without submit) does not fire.
func makeFakeRunner(result string) ClaudeRunner {
	return func(_ context.Context, args []string, _, _ string) (ClaudeRun, error) {
		if outputPath := fakeExtractSubmitPath(args); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(`"`+result+`"`), 0o600)
		}
		return ClaudeRun{Stdout: result}, nil
	}
}

// fakeRunnerWithMeta returns a ClaudeRunner that echoes the key flags it
// received so tests can assert forwarding without spawning real subprocesses.
func fakeRunnerWithMeta(result string) ClaudeRunner {
	return func(_ context.Context, args []string, _, _ string) (ClaudeRun, error) {
		var systemPrompt, model, allowedTools string
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--append-system-prompt", "--system-prompt":
				if i+1 < len(args) {
					systemPrompt = args[i+1]
					i++
				}
			case "--model":
				if i+1 < len(args) {
					model = args[i+1]
					i++
				}
			case "--allowedTools":
				if i+1 < len(args) {
					allowedTools = args[i+1]
					i++
				}
			}
		}

		out := "RESULT:" + result
		if systemPrompt != "" {
			out += " system=[" + systemPrompt + "]"
		}
		if model != "" {
			out += " model=[" + model + "]"
		}
		if allowedTools != "" {
			out += " tools=[" + allowedTools + "]"
		}
		if outputPath := fakeExtractSubmitPath(args); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(`"`+result+`"`), 0o600)
		}
		return ClaudeRun{Stdout: out}, nil
	}
}

// ParseFakeMetaReply extracts the metadata fields from a reply produced by
// fakeRunnerWithMeta. Returns (result, systemPrompt, model, allowedTools).
// The "RESULT:" prefix is stripped from result.
func ParseFakeMetaReply(reply string) (result, systemPrompt, model, allowedTools string) {
	if !strings.HasPrefix(reply, "RESULT:") {
		return reply, "", "", ""
	}
	rest := strings.TrimPrefix(reply, "RESULT:")

	// Extract each " key=[value]" field by scanning forward. The fields are
	// appended in order: system, model, tools. We parse each independently so
	// order doesn't matter.
	extract := func(src, prefix string) string {
		i := strings.Index(src, prefix)
		if i < 0 {
			return ""
		}
		after := src[i+len(prefix):]
		j := strings.Index(after, "]")
		if j < 0 {
			return after
		}
		return after[:j]
	}

	systemPrompt = extract(rest, " system=[")
	model = extract(rest, " model=[")
	allowedTools = extract(rest, " tools=[")

	// result is everything before the first metadata field tag.
	result = rest
	for _, tag := range []string{" system=[", " model=[", " tools=["} {
		if i := strings.Index(result, tag); i >= 0 {
			result = result[:i]
		}
	}
	return
}
