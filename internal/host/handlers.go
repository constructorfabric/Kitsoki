// Package host — built-in handler implementations.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"kitsoki/internal/effect"
)

// hostRunForkRetries bounds how many times RunHandler re-attempts a child
// process that failed to even START with a TRANSIENT OS resource error
// (EAGAIN / ENOMEM — "resource temporarily unavailable" / "cannot allocate
// memory"). Under a heavily-loaded host — e.g. `go test ./...` forking many
// subprocesses across packages on a small CI runner — fork/clone can transiently
// fail; that is NOT a command failure and must not surface as an on_error: arc.
// A non-zero EXIT (the command ran and failed) is never retried — only a
// failure to spawn. See isTransientSpawnError.
const hostRunForkRetries = 4

// isTransientSpawnError reports whether err is a transient failure to SPAWN a
// child (as opposed to a non-zero exit, which is *exec.ExitError, or a context
// cancellation). These are the kernel's back-pressure signals under fork/memory
// load and are safe to retry.
func isTransientSpawnError(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false // the command ran; a non-zero exit is a real result, not a spawn failure
	}
	if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.ENOMEM) {
		return true
	}
	// Fallback string match for wrapped errors that lose the errno identity.
	msg := err.Error()
	return strings.Contains(msg, "resource temporarily unavailable") ||
		strings.Contains(msg, "cannot allocate memory")
}

// ClassifyDispatchedCall returns the default (effect, deterministic) pair
// for a dispatched host verb — the builtin classification table
// (internal/effect.ClassifyVerb), consulted with the call's args so
// multi-op verbs (host.git, host.gh.ticket, host.local, and the other
// prefix-fallback handlers registered below) resolve per-op.
//
// This is the SAME table internal/machine uses to stamp the HostInvoked
// event before a handler is ever looked up; exporting it here (rather than
// duplicating a second lookup) keeps namespace -> classification a single
// definition, per the effect package's own leaf-package rationale. Callers
// that need the effect class for recording on HostDispatched/HostReturned
// (internal/orchestrator/host_dispatch.go) use this rather than reaching into
// internal/effect directly, so the "consult args for an op" convention lives
// in one place.
func ClassifyDispatchedCall(namespace string, args map[string]any) (effect.Effect, bool) {
	return effect.ClassifyVerb(namespace, args)
}

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
//   - script       (string, optional): app-relative script path inserted as
//     argv[0] before args. Imported-story paths are rebased by the loader, so
//     the child script is used even when KITSOKI_APP_DIR names the parent app.
//   - cwd          (string, optional): working directory
//   - timeout      (number|string, optional): wall-clock cap on the child
//     process.  A bare number is seconds (e.g. 120); a string is parsed as a
//     Go duration (e.g. "90s", "5m").  When the cap is hit the child (and its
//     process group) is killed and Result.Error is set ("host.run: timed out
//     after …") so the on_enter `on_error:` arc fires instead of the turn
//     blocking forever.  Without it a child that never exits — e.g. an HTTP
//     client wedged on a half-closed proxy socket with no read timeout —
//     hangs the handler, which holds the session's driver write-lock and
//     freezes every subsequent turn with nothing surfaced to the UI or trace.
//     Off by default so legitimately long phases (image builds, e2e) are
//     uncapped; set it on any call that touches a flaky network boundary.
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
//     field exposed by host.agent.ask_with_mcp.
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

	// An optional timeout caps the child's wall-clock time.  We derive a
	// child context so cancellation kills the process (exec.CommandContext
	// SIGKILLs on ctx.Done), and remember the deadline so a timeout can be
	// distinguished from an ordinary non-zero exit below.
	timeout, terr := parseTimeout(args["timeout"])
	if terr != nil {
		return Result{Error: fmt.Sprintf("host.run: %v", terr)}, nil
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Coerce argv once; the same vector is reused across spawn retries.
	var argv []string
	if rawArgs, hasArgs := args["args"]; hasArgs && rawArgs != nil {
		var err error
		argv, err = coerceArgs(rawArgs)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.run: %v", err)}, nil
		}
	}
	if rawScript, hasScript := args["script"]; hasScript {
		script, ok := rawScript.(string)
		if !ok || strings.TrimSpace(script) == "" {
			return Result{Error: fmt.Sprintf("host.run: script must be a non-empty string, got %T", rawScript)}, nil
		}
		argv = append([]string{resolveRunScriptPath(ctx, script)}, argv...)
	}
	cwd, _ := args["cwd"].(string)

	// buildCmd constructs a FRESH exec.Cmd for each spawn attempt — a Cmd's pipes
	// can only be wired once, so a retry needs a new Cmd, not a re-run of the old.
	buildCmd := func() *exec.Cmd {
		var c *exec.Cmd
		if argv != nil {
			c = exec.CommandContext(ctx, cmd, argv...)
		} else {
			c = exec.CommandContext(ctx, "bash", "-c", cmd)
		}
		if cwd != "" {
			c.Dir = cwd
		}
		// Prepend the kitsoki binary's own directory to PATH so a host.run that
		// shells out to `kitsoki <subcommand>` (e.g. project onboarding's
		// `kitsoki project-tools install`) resolves the SAME binary that is
		// running this session — independent of the operator's login-shell PATH.
		// Same treatment the agent runner already applies; a no-op when the dir is
		// already on PATH.
		c.Env = envWithKitsokiBinOnPath(os.Environ())
		return c
	}

	// Run with bounded retry on a TRANSIENT failure to spawn the child. A flaky
	// fork/clone under host load (EAGAIN/ENOMEM) would otherwise bubble up as a
	// host.run infra error and trip the calling room's on_error: arc — the root
	// cause behind the punch-list studio test flake (host.run → needs_human on a
	// loaded CI runner). A real command result (non-zero exit) is NOT retried.
	var out []byte
	var err error
	for attempt := 0; ; attempt++ {
		out, err = buildCmd().CombinedOutput()
		if err == nil || ctx.Err() != nil || !isTransientSpawnError(err) || attempt >= hostRunForkRetries {
			if isTransientSpawnError(err) {
				// Exhausted retries on a transient spawn failure — log loudly so the
				// trace shows WHY a host.run that should have run never did. The
				// error is also returned (below) and folded into host_error.
				slog.ErrorContext(ctx, "host.run.spawn_failed",
					slog.String("cmd", cmd),
					slog.Int("attempts", attempt+1),
					slog.String("err", err.Error()),
				)
			}
			break
		}
		slog.WarnContext(ctx, "host.run.spawn_retry",
			slog.String("cmd", cmd),
			slog.Int("attempt", attempt+1),
			slog.String("err", err.Error()),
		)
		// Linear backoff; the contention window is short. Honor ctx cancellation.
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(attempt+1) * 20 * time.Millisecond):
		}
	}
	exitCode := 0
	if err != nil {
		// A hit timeout cancels ctx, which kills the child and surfaces here
		// as a non-ExitError.  Report it as a loud, on_error-routable failure
		// rather than an opaque infra error — and keep any partial output so
		// the error room can render what the command managed to emit.
		if timeout > 0 && ctx.Err() == context.DeadlineExceeded {
			return Result{
				Data: map[string]any{
					"stdout":    string(out),
					"exit_code": -1,
					"ok":        false,
					"timed_out": true,
				},
				Error: fmt.Sprintf("host.run: timed out after %s", timeout),
			}, nil
		}
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

	// Best-effort JSON envelope parse. host.run uses CombinedOutput, so a
	// subcommand's stderr logs land on stdout AHEAD of its JSON envelope;
	// trailingJSONValue extracts the LAST JSON value from the output,
	// tolerating both those leading log lines AND an envelope that spans
	// multiple lines. The latter matters because the default `jq -n '{...}'`
	// (no -c) pretty-prints, so the envelope's last line is a bare "}".
	// The previous last-line-only parse bound nothing for pretty-printed
	// envelopes — silently, since `bind: <slot>: stdout_json` tolerates an
	// absent value — which stranded git-ops's real (non-mocked) host.run
	// routing. A parse miss is not an error: it leaves stdout_json absent so
	// `on_error` stays pinned to real failures. We still surface a
	// parse-error when the tail clearly INTENDED JSON but won't parse.
	if v, ok := trailingJSONValue(stdout); ok {
		res.Data["stdout_json"] = v
	} else if last := lastNonEmptyLine(stdout); looksLikeJSON(last) {
		var probe any
		res.Data["stdout_json_parse_error"] = json.Unmarshal([]byte(last), &probe).Error()
	}

	if exitCode != 0 {
		failOnError, _ := args["fail_on_error"].(bool)
		if failOnError {
			res.Error = fmt.Sprintf("host.run: command exited %d", exitCode)
		}
	}

	return res, nil
}

// resolveRunScriptPath makes host.run's explicit script path story-relative.
// Imported effects already arrive with an absolute, child-rooted path because
// app.rebaseEffectPaths rewrites with.script while folding the import. Direct
// stories retain a relative path, so use the per-orchestrator renderer root
// first and the legacy process-global app dir only as a compatibility fallback.
func resolveRunScriptPath(ctx context.Context, script string) string {
	if filepath.IsAbs(script) {
		return script
	}
	if pr := PromptRendererFromCtx(ctx); pr != nil && strings.TrimSpace(pr.RootDir()) != "" {
		return filepath.Join(pr.RootDir(), script)
	}
	if appDir := strings.TrimSpace(os.Getenv(AppDirEnv)); appDir != "" {
		return filepath.Join(appDir, script)
	}
	return script
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

// trailingJSONValue extracts the last complete JSON value from s and reports
// whether one was found. It tolerates leading log lines (host.run's
// CombinedOutput mixes a subcommand's stderr into stdout) AND a JSON envelope
// that spans multiple lines (pretty-printed `jq` output).
//
// Strategy: the last non-empty line is tried first — the single-line envelope
// fast path. If that doesn't parse, scan backward for a line that OPENS a
// JSON value (`{` or `[`) and parse from there to the end; the first such
// suffix that parses is the trailing value. Scanning backward means a nested
// opener is tried before the real outer one, but a nested suffix is
// unbalanced and fails to parse, so the outermost complete value wins.
func trailingJSONValue(s string) (any, bool) {
	if s == "" {
		return nil, false
	}
	lines := strings.Split(s, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if end == 0 {
		return nil, false
	}
	var v any
	// Fast path: a complete single-line envelope.
	if err := json.Unmarshal([]byte(strings.TrimSpace(lines[end-1])), &v); err == nil {
		return v, true
	}
	// Multi-line: find the opener of the trailing value.
	for start := end - 1; start >= 0; start-- {
		t := strings.TrimSpace(lines[start])
		if t == "" || (t[0] != '{' && t[0] != '[') {
			continue
		}
		blob := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if err := json.Unmarshal([]byte(blob), &v); err == nil {
			return v, true
		}
	}
	return nil, false
}

// coerceArgs converts a YAML-decoded args list into the []string form
// exec.CommandContext expects.  Accepts a Go []any (the shape produced by
// goccy/go-yaml for sequence nodes) and stringifies each element with
// fmt.Sprint, so numeric/boolean YAML scalars don't require explicit
// stringification by the author.  A nil element becomes the empty string.
//
// Map/slice values (i.e. world-slot objects bound from a previous
// host.agent.ask_with_mcp call) are serialised to compact JSON.  This
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

// parseTimeout interprets the host.run `timeout` arg.  Nil/absent yields 0
// (no cap).  A numeric scalar is seconds (YAML decodes these as int/int64/
// float64).  A string is a Go duration ("90s", "5m"); a bare numeric string
// is also accepted as seconds for author convenience.  A non-positive or
// unparseable value is an error so a typo'd cap is loud, not silently
// ignored (which would re-introduce the unbounded-hang it exists to prevent).
func parseTimeout(raw any) (time.Duration, error) {
	switch v := raw.(type) {
	case nil:
		return 0, nil
	case int:
		return secondsToDuration(float64(v))
	case int64:
		return secondsToDuration(float64(v))
	case float64:
		return secondsToDuration(v)
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, nil
		}
		if d, err := time.ParseDuration(s); err == nil {
			if d <= 0 {
				return 0, fmt.Errorf("timeout must be positive, got %q", s)
			}
			return d, nil
		}
		// Fall back to bare-seconds interpretation ("120" → 120s).
		var secs float64
		if _, err := fmt.Sscanf(s, "%g", &secs); err != nil {
			return 0, fmt.Errorf("timeout: cannot parse %q as duration or seconds", s)
		}
		return secondsToDuration(secs)
	default:
		return 0, fmt.Errorf("timeout must be a number or duration string, got %T", raw)
	}
}

func secondsToDuration(secs float64) (time.Duration, error) {
	if secs <= 0 {
		return 0, fmt.Errorf("timeout must be positive, got %g", secs)
	}
	return time.Duration(secs * float64(time.Second)), nil
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
	r.Register("host.corpus.prove", CorpusProofHandler(nil))
	r.Register("host.corpus.freeze_receipt", CorpusReceiptHandler(nil))
	r.Register("host.punch.verify", PunchVerifyHandler)
	r.Register("host.proposal.publish", ProposalPublishHandler)
	r.Register("host.dev.profile_setup", ProfileSetupHandler)
	r.Register("host.dev.onboarding", DevOnboardingHandler)
	r.Register("host.decomposition.update", DecompositionUpdateHandler)
	r.Register("host.tour.plan", TourPlanHandler)
	r.Register("host.tour.validate", TourValidateHandler)
	r.Register("host.product_journey.run", ProductJourneyRunHandler)
	r.Register("host.bakeoff.run", BakeoffRunHandler)
	r.Register("host.session_mining.run", SessionMiningRunHandler)
	r.Register("host.ui_qa.run", UIQARunHandler)
	r.Register("host.agent.ask", AgentAskHandler)
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
	r.RegisterTicketProvider("host.local_files.ticket", LocalFilesTicketHandler)
	r.RegisterTicketProvider("host.local_github.ticket", LocalGitHubTicketHandler)
	r.Register("host.ticket_federation", TicketFederationHandler(r))
	r.Register("host.git", GitVCSHandler)
	r.Register("host.local", LocalCIHandler)
	r.Register("host.capsule_ci.project_checks", CapsuleCIProjectChecksHandler)
	r.Register("host.git_worktree", GitWorktreeHandler)
	r.Register("host.capsule_workspace", CapsuleWorkspaceHandler)
	r.Register("host.append_to_file", AppendFileTransportHandler)
	r.Register("host.artifacts_dir", ArtifactsDirTransportHandler)
	r.Register("host.inbox.add", InboxAddHandler)
	r.Register("host.fs.writable_dir", FSWritableDirHandler)

	// Wave 3 / Phase 5 — GitHub Issues + cypilot artifact providers.
	// `host.gh.ticket` backs the `ticket` iface with native GitHub Issues API
	// calls; `host.git` routes PR operations through local git plus native
	// GitHub API calls. `host.cypilot_artifacts` shells out to cpt for the SDLC
	// artifact iface.
	r.RegisterTicketProvider("host.gh.ticket", GitHubTicketHandler)
	r.Register("host.cypilot_artifacts", CypilotArtifactsHandler)

	// Agent five verbs.
	// host.agent.ask is registered above.
	r.Register("host.agent.extract", AgentExtractHandler)
	r.Register("host.agent.decide", AgentDecideHandler)
	r.Register("host.agent.task", AgentTaskHandler)
	r.Register("host.agent.converse", AgentConverseHandler)
	r.Register("host.agent.codeact", AgentCodeactHandler)

	// IDE link (host.ide.*) — editor awareness over the MCP-over-ws Link.
	// Resolve the link from ctx; a nil/disconnected link returns the typed
	// not-connected Result (data.connected==false), never a Go error.
	r.Register("host.ide.get_diagnostics", IDEGetDiagnosticsHandler)
	r.Register("host.ide.get_selection", IDEGetSelectionHandler)
	r.Register("host.ide.get_open_editors", IDEGetOpenEditorsHandler)
	r.Register("host.ide.open_file", IDEOpenFileHandler)
	r.Register("host.ide.open_diff", IDEOpenDiffHandler)

	// host.diff.open — the front door for "open this change for review, and
	// tell me what they decided." Resolves the best diff surface by capability
	// (connected IDE → system difftool → none) and captures the operator's
	// accept/reject verdict only when the surface (the IDE) can produce one;
	// the difftool fallback is view-only. host.ide.open_diff stays unchanged and
	// is the IDE path this calls into. See diff_open.go / docs/architecture/hosts.md.
	r.Register("host.diff.open", DiffOpenHandler)

	// Deterministic Starlark glue (host.starlark.run). Registered at the full
	// name so the registry's longest-prefix fallback resolves it exactly. The
	// handler is a thin adapter over internal/host/starlark; see starlark_run.go.
	// Registered as a closure over r (rather than the bare func value) so the
	// handler can inject a ctx.host caller that invokes back into THIS SAME
	// registry (S3d narrow allow-listed ctx.host) — see NewStarlarkRunHandler.
	r.Register("host.starlark.run", NewStarlarkRunHandler(r))

	// Visual output producers (visual-outputs epic, Slice 2).
	// host.slidey.render — validate + render a JSON scene spec via slidey.
	// host.contact_sheet — PNG montage of frames via ffmpeg tile filter.
	r.Register("host.slidey.render", SlideyRenderHandler)
	r.Register("host.contact_sheet", ContactSheetHandler)

	// Mockup-video-studio epic, Slice 1 — host.video.frame.
	// Deterministic single-frame still grab over internal/video.Frame (the
	// one extractor shared with the slice-2 web RPC); no LLM.
	r.Register("host.video.frame", VideoFrameHandler)

	// Embeddings epic, Slice 2 — host.agent.search.
	// The sentinel handler returns a configuration-required error; apps that
	// want a working embedder call NewAgentSearchHandler and re-register.
	r.Register("host.agent.search", AgentSearchHandler)

	// Kits epic, S5 — host.graph.* (project object graph engine substrate,
	// see graph_handlers.go). Registered bare so the registry's longest-
	// prefix fallback resolves every host.graph.<op> call here with <op>
	// injected into args["op"].
	r.Register("host.graph", GraphHandler)

	// Use-case loop A2 — host.demo.* (mockup/demo packet pipeline: create,
	// record, doctor; see demo_handlers.go). Registered bare so the
	// registry's longest-prefix fallback resolves every host.demo.<op>
	// call here with <op> injected into args["op"].
	r.Register("host.demo", DemoHandler)
}

// AgentExtractHandler is implemented in agent_extract.go.

// AgentDecideHandler is the implementation of host.agent.decide.
// See agent_decide.go for the full contract.

// AgentTaskHandler is defined in agent_task.go.
// AgentConverseHandler is defined in agent_converse.go.
