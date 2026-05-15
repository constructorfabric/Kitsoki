// Package host — AskStructured: ask Claude for a schema-validated JSON
// payload via the kitsoki mcp-validator side-channel.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrNoValidatedPayload signals that `claude -p` exited without the LLM
// ever making a successful submit() call against the validator MCP
// server. Callers can errors.Is() this to distinguish "binary missing"
// from "model never submitted".
var ErrNoValidatedPayload = errors.New("host.ask_structured: claude exited without a validated submit() payload")

// AskStructuredOptions configures a single AskStructured call.
type AskStructuredOptions struct {
	ClaudeBin    string
	Model        string
	Prompt       string
	WorkingDir   string
	Schema       []byte
	MaxRetries   int
	SystemPrompt string
}

// askStructuredFunc is the test seam — swap in tests to feed canned
// payloads without spawning claude + mcp-validator subprocesses.
var askStructuredFunc = AskStructured

// AskStructured spawns `claude -p` with kitsoki mcp-validator attached
// against opts.Schema and returns the raw JSON the LLM submitted.
func AskStructured(ctx context.Context, opts AskStructuredOptions) (json.RawMessage, error) {
	if len(opts.Schema) == 0 {
		return nil, fmt.Errorf("host.ask_structured: Schema is required")
	}

	bin := opts.ClaudeBin
	if bin == "" {
		resolved, err := resolveOracleBin(ctx)
		if err != nil {
			return nil, err
		}
		bin = resolved
	}

	schemaFile, err := os.CreateTemp("", "kitsoki-ask-schema-*.json")
	if err != nil {
		return nil, fmt.Errorf("host.ask_structured: create schema tempfile: %w", err)
	}
	schemaPath := schemaFile.Name()
	defer os.Remove(schemaPath)
	if _, err := schemaFile.Write(opts.Schema); err != nil {
		_ = schemaFile.Close()
		return nil, fmt.Errorf("host.ask_structured: write schema: %w", err)
	}
	if err := schemaFile.Close(); err != nil {
		return nil, fmt.Errorf("host.ask_structured: close schema: %w", err)
	}

	outFile, err := os.CreateTemp("", "kitsoki-ask-validated-*.json")
	if err != nil {
		return nil, fmt.Errorf("host.ask_structured: create output tempfile: %w", err)
	}
	outputPath := outFile.Name()
	_ = outFile.Close()
	// Remove so we can detect "validator never wrote" via stat-not-found.
	_ = os.Remove(outputPath)
	defer os.Remove(outputPath)

	validatorEntry, err := buildValidatorMCPServer(schemaPath, outputPath, validatorOptions{MaxRetries: opts.MaxRetries})
	if err != nil {
		return nil, fmt.Errorf("host.ask_structured: build validator entry: %w", err)
	}

	mcpConfig := map[string]any{"mcpServers": map[string]any{"validator": validatorEntry}}
	mcpBytes, err := json.Marshal(mcpConfig)
	if err != nil {
		return nil, fmt.Errorf("host.ask_structured: marshal mcp config: %w", err)
	}
	cfgFile, err := os.CreateTemp("", "kitsoki-ask-mcp-*.json")
	if err != nil {
		return nil, fmt.Errorf("host.ask_structured: create mcp config tempfile: %w", err)
	}
	cfgPath := cfgFile.Name()
	defer os.Remove(cfgPath)
	if _, err := cfgFile.Write(mcpBytes); err != nil {
		_ = cfgFile.Close()
		return nil, fmt.Errorf("host.ask_structured: write mcp config: %w", err)
	}
	if err := cfgFile.Close(); err != nil {
		return nil, fmt.Errorf("host.ask_structured: close mcp config: %w", err)
	}

	cliArgs := []string{
		"-p",
		"--output-format", "text",
		"--permission-mode", "bypassPermissions",
		"--mcp-config", cfgPath,
	}
	if strings.TrimSpace(opts.Model) != "" {
		cliArgs = append(cliArgs, "--model", opts.Model)
	}
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		cliArgs = append(cliArgs, "--append-system-prompt", opts.SystemPrompt)
	}

	cr, runErr := runClaudeOneShot(ctx, bin, cliArgs, opts.Prompt, opts.WorkingDir)
	if runErr != nil {
		return nil, runErr
	}
	if cr.Infra != nil {
		msg := fmt.Sprintf("host.ask_structured: claude exec failed: %v", cr.Infra)
		if s := strings.TrimSpace(cr.Stderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		return nil, errors.New(msg)
	}

	payload, readErr := os.ReadFile(outputPath)
	if readErr != nil || len(payload) == 0 {
		// File-not-found / empty = validator never captured a submit.
		if cr.ExitCode != 0 {
			return nil, fmt.Errorf("host.ask_structured: %s", claudeExitErrorMessage(cr.ExitCode, cr.Stderr, cr.Stdout))
		}
		return nil, ErrNoValidatedPayload
	}
	// Validate it's parseable JSON (the validator only writes schema-passed
	// payloads, so a parse error here is a real bug).
	var probe any
	if jErr := json.Unmarshal(payload, &probe); jErr != nil {
		return nil, fmt.Errorf("host.ask_structured: parse validator output: %w", jErr)
	}
	return json.RawMessage(payload), nil
}
