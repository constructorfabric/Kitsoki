// Package host — the agy agentBackend.
//
// agyBackend drives Google's `agy` CLI as a drop-in alternative to `claude`
// for every agent verb. The verb handlers build a claude-shaped invocation
// and TranslateInvocation rewrites that into agy's flags.
package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AgyBinEnv overrides the `agy` binary path.
const AgyBinEnv = "KITSOKI_AGENT_AGY_BIN"

// agyBackend drives Google's `agy` CLI.
type agyBackend struct{}

func (agyBackend) Name() string { return "agy" }

func (agyBackend) ResolveBin(ctx context.Context) (string, error) {
	if AgyRunnerFromContext(ctx) != nil {
		return "stub://agy", nil
	}
	if bin := os.Getenv(AgyBinEnv); bin != "" {
		return bin, nil
	}
	path, err := exec.LookPath("agy")
	if err != nil {
		return "", fmt.Errorf("host.agent.converse: `agy` binary not found on PATH; install the Google Antigravity CLI: %w", ErrAgentUnavailable)
	}
	return path, nil
}

// TranslateInvocation rewrites a claude-shaped invocation into agy's CLI.
// We translate the prompt into --print. Since agy does not support a separate
// system prompt flag, we prepend the system prompt to the user text. We also
// isolate the execution by creating a temporary --app_data_dir and copying
// user credentials (google_accounts.json, oauth_creds.json, state.json,
// settings.json) into it, alongside mapping the --mcp-config if provided.
func (agyBackend) TranslateInvocation(claudeArgs []string, stdin, workingDir string) Invocation {
	var (
		out          []string
		systemPrompt string
		model        string
		mcpConfig    string
		sessionID    string
		resumeID     string
	)

	flagVal := func(a string) (flag, val string, inlined bool) {
		if i := strings.IndexByte(a, '='); i > 0 && strings.HasPrefix(a, "-") {
			return a[:i], a[i+1:], true
		}
		return a, "", false
	}

	for i := 0; i < len(claudeArgs); i++ {
		a := claudeArgs[i]
		flag, inlineVal, inlined := flagVal(a)

		val := inlineVal
		consumed := false
		if claudeValueFlags[flag] && !inlined {
			if i+1 < len(claudeArgs) {
				val = claudeArgs[i+1]
				consumed = true
			}
		}

		switch flag {
		case "-p", "--verbose", "--exclude-dynamic-system-prompt-sections", "--no-session-persistence",
			"--disable-slash-commands":
			// Dropped.
		case "--permission-mode", "--setting-sources", "--settings", "--effort",
			"--allowedTools", "--disallowedTools":
			// Dropped.
		case "--session-id":
			sessionID = val
		case "--resume":
			resumeID = val
		case "--add-dir":
			if strings.TrimSpace(val) != "" {
				out = append(out, "--add-dir", val)
			}
		case "--output-format":
			// Normalized to json.
		case "--system-prompt", "--append-system-prompt":
			systemPrompt = val
		case "--model":
			model = val
		case "--mcp-config":
			mcpConfig = val
		default:
			// Passthrough unknown flags.
			out = append(out, a)
			if claudeValueFlags[flag] && consumed {
				out = append(out, val)
			}
			continue
		}
		if consumed {
			i++
		}
	}

	var convID string
	prompt := stdin
	if resumeID != "" {
		convID = resumeID
		// Warm run: omit system prompt since it is already in the conversation.
	} else {
		convID = sessionID
		// Cold run: prepend system prompt.
		if sp := strings.TrimSpace(systemPrompt); sp != "" {
			prompt = sp + "\n\n---\n\n" + stdin
		}
	}

	args := []string{
		"--print", prompt,
		"--output-format", "json",
		"--dangerously-skip-permissions",
	}

	if m := strings.TrimSpace(model); m != "" && !isClaudeModelID(m) {
		args = append(args, "--model", m)
	}

	if strings.TrimSpace(convID) != "" {
		args = append(args, "--conversation", strings.TrimSpace(convID))
	}

	var cleanup func()
	tmpDir, err := os.MkdirTemp("", "kitsoki-agy-*")
	if err == nil {
		cleanup = func() {
			os.RemoveAll(tmpDir)
		}

		geminiDir := filepath.Join(tmpDir, ".gemini")
		cliDir := filepath.Join(geminiDir, "antigravity-cli")
		os.MkdirAll(cliDir, 0700)

		if home, hErr := os.UserHomeDir(); hErr == nil {
			realGemini := filepath.Join(home, ".gemini")
			realCli := filepath.Join(realGemini, "antigravity-cli")

			copyFile := func(src, dst string) {
				if data, rErr := os.ReadFile(src); rErr == nil {
					os.WriteFile(dst, data, 0600)
				}
			}

			copyFile(filepath.Join(realGemini, "google_accounts.json"), filepath.Join(geminiDir, "google_accounts.json"))
			copyFile(filepath.Join(realGemini, "oauth_creds.json"), filepath.Join(geminiDir, "oauth_creds.json"))
			copyFile(filepath.Join(realGemini, "state.json"), filepath.Join(geminiDir, "state.json"))
			copyFile(filepath.Join(realCli, "settings.json"), filepath.Join(cliDir, "settings.json"))

			if mcpConfig == "" {
				copyFile(filepath.Join(realCli, "mcp_config.json"), filepath.Join(cliDir, "mcp_config.json"))
			}
		}

		if mcpConfig != "" {
			if data, rErr := os.ReadFile(mcpConfig); rErr == nil {
				os.WriteFile(filepath.Join(cliDir, "mcp_config.json"), data, 0600)
			}
		}

		args = append(args, "--app_data_dir", tmpDir)
	}

	args = append(args, out...)

	return Invocation{
		Args:       args,
		Stdin:      "",
		WorkingDir: workingDir,
		Cleanup:    cleanup,
	}
}

func (agyBackend) Classify(ev map[string]any) classifiedEvent {
	status, _ := ev["status"].(string)
	response, _ := ev["response"].(string)
	convID, _ := ev["conversation_id"].(string)

	ce := classifiedEvent{
		Type:       "result",
		Subtype:    strings.ToLower(status),
		IsResult:   true,
		ResultText: response,
		SessionID:  convID,
	}

	if usageVal, ok := ev["usage"].(map[string]any); ok {
		ce.Usage = usageVal
	}

	return ce
}

func (agyBackend) TranscriptFormat() string { return "agy-jsonl" }

func (agyBackend) ValidatorToolName(server string) string {
	return "mcp__" + server + "__submit"
}

func (agyBackend) runnerFromContext(ctx context.Context) ClaudeRunner {
	return AgyRunnerFromContext(ctx)
}

// --- agy test-stub seam ---

type agyRunnerCtxKey struct{}

// WithAgyRunner installs a stub runner for the agy backend in tests.
func WithAgyRunner(ctx context.Context, r ClaudeRunner) context.Context {
	return context.WithValue(ctx, agyRunnerCtxKey{}, r)
}

// AgyRunnerFromContext returns the agy stub runner installed in ctx.
func AgyRunnerFromContext(ctx context.Context) ClaudeRunner {
	r, _ := ctx.Value(agyRunnerCtxKey{}).(ClaudeRunner)
	return r
}
