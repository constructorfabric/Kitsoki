// Package host — built-in handler implementations.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// WorkspaceManagerGetHandler implements host.workspace_manager.get.
// It shells out to the workspace-manager CLI binary and parses JSON output.
// Args:
//   - workspace_id (string, optional): if set, fetch that workspace; else fetch current
//
// Returns Result.Data with the parsed JSON from the CLI.
func WorkspaceManagerGetHandler(ctx context.Context, args map[string]any) (Result, error) {
	// Build the command: workspace-manager get [--id <id>]
	cmdArgs := []string{"get"}
	if id, ok := args["workspace_id"].(string); ok && id != "" {
		cmdArgs = append(cmdArgs, "--id", id)
	}

	out, err := exec.CommandContext(ctx, "workspace-manager", cmdArgs...).Output()
	if err != nil {
		// Check if it's an exit error with stderr
		if exitErr, ok := err.(*exec.ExitError); ok {
			return Result{Error: strings.TrimSpace(string(exitErr.Stderr))}, nil
		}
		// Binary not found or infra failure
		return Result{}, fmt.Errorf("host.workspace_manager.get: exec: %w", err)
	}

	var data map[string]any
	if err := json.Unmarshal(out, &data); err != nil {
		return Result{}, fmt.Errorf("host.workspace_manager.get: parse JSON: %w", err)
	}

	return Result{Data: data}, nil
}

// RunHandler implements host.run — executes either a shell command via bash
// or a program with an explicit argv list (no shell).
//
// Args:
//   - cmd  (string, required): the program (argv-mode) or shell command (bash-mode)
//   - args ([]any, optional):  if present, exec `cmd` directly with these
//     positional arguments — no shell, no word-splitting, no glob/tilde
//     expansion.  Use this whenever any argument is templated from world or
//     slot data: it passes the value through as a single argv element no
//     matter what shell metacharacters it contains.  Each element is
//     coerced to its string form (numbers/bools become their decimal/`true`
//     representation, nil becomes the empty string).
//   - cwd          (string, optional): working directory
//   - fail_on_error (bool, optional, default false): when true, a non-zero
//     exit code populates Result.Error so the on_enter `on_error:` arc
//     fires instead of the success `done` arc.  Off by default for
//     backwards compatibility — callers that want to inspect exit_code as
//     data leave it false; callers that treat the script as pass/fail
//     (e.g. the bugfix room's script-driven phases) set it true so a
//     failed deploy doesn't get treated as success.
//
// Returns Result.Data with:
//   - stdout (string):    combined stdout
//   - exit_code (int):    exit code
//   - ok (bool):          true if exit code == 0
//   - stdout_json (any):  parsed JSON when stdout's last non-empty line is
//     a single JSON document and parse succeeds.  Lets
//     CLI subcommands that emit a structured envelope
//     on their last stdout line (e.g.
//     tools/loopy/bugfix's `python3 -m bugfix <cmd>`)
//     be bound directly into a world slot via
//     `bind: <slot>: stdout_json`.  Mirrors the same
//     field exposed by host.oracle.ask_with_mcp.
//   - stdout_json_parse_error (string): present (and stdout_json absent)
//     when the last line looked like JSON but couldn't
//     be parsed; useful for diagnosing envelope drift.
//
// When fail_on_error=true and exit_code != 0, Result.Error is also set
// (Data is preserved so the error state can render stdout/exit_code).
func RunHandler(ctx context.Context, args map[string]any) (Result, error) {
	cmd, ok := args["cmd"].(string)
	if !ok || cmd == "" {
		return Result{Error: "host.run: cmd argument is required"}, nil
	}

	var execCmd *exec.Cmd
	if rawArgs, hasArgs := args["args"]; hasArgs && rawArgs != nil {
		argv, err := coerceArgs(rawArgs)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.run: %v", err)}, nil
		}
		execCmd = exec.CommandContext(ctx, cmd, argv...)
	} else {
		execCmd = exec.CommandContext(ctx, "bash", "-c", cmd)
	}

	if cwd, ok := args["cwd"].(string); ok && cwd != "" {
		execCmd.Dir = cwd
	}

	out, err := execCmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{}, fmt.Errorf("host.run: exec: %w", err)
		}
	}

	stdout := string(out)
	res := Result{
		Data: map[string]any{
			"stdout":    stdout,
			"exit_code": exitCode,
			"ok":        exitCode == 0,
		},
	}

	// Best-effort JSON envelope parse off the *last non-empty line* of
	// stdout.  Subcommands that follow the envelope contract emit logs
	// to stderr and a single JSON line on stdout; this lets the bound
	// world slot carry a structured object instead of a raw blob.
	// Failure here is not an error — `bind: <slot>: stdout_json` is
	// silently absent so `on_error` stays pinned to real failures.
	if last := lastNonEmptyLine(stdout); last != "" {
		var parsed any
		if jErr := json.Unmarshal([]byte(last), &parsed); jErr == nil {
			res.Data["stdout_json"] = parsed
		} else if looksLikeJSON(last) {
			res.Data["stdout_json_parse_error"] = jErr.Error()
		}
	}

	if exitCode != 0 {
		failOnError, _ := args["fail_on_error"].(bool)
		if failOnError {
			res.Error = fmt.Sprintf("host.run: command exited %d", exitCode)
		}
	}

	return res, nil
}

// lastNonEmptyLine returns the last line of s that contains non-whitespace,
// or "" if there is none.  Used by host.run's stdout_json parse to skip
// trailing newlines without scanning the whole output.
func lastNonEmptyLine(s string) string {
	if s == "" {
		return ""
	}
	// Walk backwards through the string, splitting on '\n' so we don't
	// allocate a slice for every line.
	end := len(s)
	for end > 0 {
		// Find the start of the current line.
		start := strings.LastIndexByte(s[:end], '\n') + 1
		line := strings.TrimSpace(s[start:end])
		if line != "" {
			return line
		}
		end = start - 1
		if end < 0 {
			return ""
		}
	}
	return ""
}

// coerceArgs converts a YAML-decoded args list into the []string form
// exec.CommandContext expects.  Accepts a Go []any (the shape produced by
// goccy/go-yaml for sequence nodes) and stringifies each element with
// fmt.Sprint, so numeric/boolean YAML scalars don't require explicit
// stringification by the author.  A nil element becomes the empty string.
//
// Map/slice values (i.e. world-slot objects bound from a previous
// host.oracle.ask_with_mcp call) are serialised to compact JSON.  This
// lets phase-runner cmds receive structured data on argv without the
// author having to pre-stringify it themselves — the bugfix room's
// `verify-impl` step depends on this so the post-submission verifier
// can read `world.phase_6_5_submitted` directly off the command line.
//
// Any non-list value, or a list element whose Go type is none of the
// above, yields an error so misuse is loud rather than silent.
func coerceArgs(raw any) ([]string, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("args must be a list, got %T", raw)
	}
	out := make([]string, len(list))
	for i, v := range list {
		switch x := v.(type) {
		case nil:
			out[i] = ""
		case string:
			out[i] = x
		case bool, int, int64, float64:
			out[i] = fmt.Sprint(x)
		case map[string]any, []any:
			b, jErr := json.Marshal(x)
			if jErr != nil {
				return nil, fmt.Errorf("args[%d]: json marshal: %w", i, jErr)
			}
			out[i] = string(b)
		default:
			return nil, fmt.Errorf("args[%d]: unsupported type %T", i, v)
		}
	}
	return out, nil
}

// looksLikeJSON reports whether s looks JSON-ish enough that a parse
// failure is interesting to surface as stdout_json_parse_error.  Avoids
// noisy errors when stdout is plain text — only interesting when the
// caller plausibly intended a JSON envelope.
func looksLikeJSON(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c == '{' || c == '[' || c == '"'
}

// RegisterBuiltins registers all built-in host handlers into the registry.
// Call at process startup before any app is loaded.
func RegisterBuiltins(r *Registry) {
	r.Register("host.workspace_manager.get", WorkspaceManagerGetHandler)
	r.Register("host.run", RunHandler)
	r.Register("host.oracle.ask", OracleAskHandler)
	r.Register("host.transport.post", TransportPostHandler)
	r.Register("host.jobs.answer_clarification", AnswerClarificationHandler)
	r.Register("host.chat.resolve", ChatResolveHandler)
	r.Register("host.chat.list", ChatListHandler)
	r.Register("host.chat.transcript", ChatTranscriptHandler)
	r.Register("host.chat.fork", ChatForkHandler)
	r.Register("host.chat.archive", ChatArchiveHandler)
	r.Register("host.chat.create", ChatCreateHandler)
	r.Register("host.chat.rename", ChatRenameHandler)
	r.Register("host.chat.suggest_title", ChatSuggestTitleHandler)
	r.Register("host.chat.resolve_ref", ChatResolveRefHandler)
	r.Register("host.chat.drive", ChatDriveHandler)

	// Dev-story / bugfix unify Slice β handlers — one prefix-fallback
	// handler per provider surface (the registry dispatches every
	// host.<name>.<op> call to the longest registered prefix).  See
	// docs/architecture/hosts.md.
	r.Register("host.local_files.ticket", LocalFilesTicketHandler)
	r.Register("host.git", GitVCSHandler)
	r.Register("host.local", LocalCIHandler)
	r.Register("host.git_worktree", GitWorktreeHandler)
	r.Register("host.append_to_file", AppendFileTransportHandler)
	r.Register("host.artifacts_dir", ArtifactsDirTransportHandler)
	r.Register("host.inbox.add", InboxAddHandler)

	// Wave 3 / Phase 5 — GitHub Issues + cypilot artifact providers.
	// `host.gh.ticket` backs the `ticket` iface against the gh CLI; the
	// existing `host.git` already routes PR ops through gh.  `host.cypilot_artifacts`
	// shells out to cpt for the SDLC artifact iface.
	r.Register("host.gh.ticket", GitHubTicketHandler)
	r.Register("host.cypilot_artifacts", CypilotArtifactsHandler)

	// Oracle five verbs.
	// host.oracle.ask is registered above.
	r.Register("host.oracle.extract", OracleExtractHandler)
	r.Register("host.oracle.decide", OracleDecideHandler)
	r.Register("host.oracle.task", OracleTaskHandler)
	r.Register("host.oracle.converse", OracleConverseHandler)

	// IDE link (host.ide.*) — editor awareness over the MCP-over-ws Link.
	// Resolve the link from ctx; a nil/disconnected link returns the typed
	// not-connected Result (data.connected==false), never a Go error.
	r.Register("host.ide.get_diagnostics", IDEGetDiagnosticsHandler)
	r.Register("host.ide.get_selection", IDEGetSelectionHandler)
	r.Register("host.ide.get_open_editors", IDEGetOpenEditorsHandler)
	r.Register("host.ide.open_file", IDEOpenFileHandler)
	r.Register("host.ide.open_diff", IDEOpenDiffHandler)

	// Deterministic Starlark glue (host.starlark.run). Registered at the full
	// name so the registry's longest-prefix fallback resolves it exactly. The
	// handler is a thin adapter over internal/host/starlark; see starlark_run.go.
	r.Register("host.starlark.run", StarlarkRunHandler)

	// Visual output producers (visual-outputs epic, Slice 2).
	// host.slidey.render — validate + render a JSON scene spec via slidey.
	// host.contact_sheet — PNG montage of frames via ffmpeg tile filter.
	r.Register("host.slidey.render", SlideyRenderHandler)
	r.Register("host.contact_sheet", ContactSheetHandler)

	// Mockup-video-studio epic, Slice 1 — host.video.frame.
	// Deterministic single-frame still grab over internal/video.Frame (the
	// one extractor shared with the slice-2 web RPC); no LLM.
	r.Register("host.video.frame", VideoFrameHandler)

	// Embeddings epic, Slice 2 — host.oracle.search.
	// The sentinel handler returns a configuration-required error; apps that
	// want a working embedder call NewOracleSearchHandler and re-register.
	r.Register("host.oracle.search", OracleSearchHandler)
}

// OracleExtractHandler is implemented in oracle_extract.go.

// OracleDecideHandler is the implementation of host.oracle.decide.
// See oracle_decide.go for the full contract.

// OracleTaskHandler is defined in oracle_task.go.
// OracleConverseHandler is defined in oracle_converse.go.
