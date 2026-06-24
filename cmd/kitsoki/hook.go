// hook.go — implements `kitsoki hook install` and `kitsoki hook run`, the
// agent-side adapters that wire kitsoki's pre-LLM intercept engine (Stage 2,
// intercept.go) into a coding agent's prompt-submit lifecycle.
//
// Today only Claude Code has a true pre-model hook (UserPromptSubmit). The shim
// reuses runInterceptEngine IN-PROCESS — no subprocess, no second classify — so
// the latency budget is a single in-memory OneShot.
//
// THE CONTRACT (Claude Code UserPromptSubmit):
//
//	stdin : {"prompt": "...", "session_id": "...", "cwd": "..."}
//	stdout: {"decision":"block","reason":<report>}  ⇒ kitsoki answered; the
//	        prompt is NOT sent to the model and <reason> is shown to the user.
//	stdout: <empty> + exit 0                         ⇒ pass-through; the prompt
//	        proceeds to the model untouched.
//
// FAIL-OPEN is the cardinal rule: a misconfigured, slow, erroring, or panicking
// interceptor must NEVER wedge the agent. Only a clean, confident no-LLM match
// blocks; everything else (no intercept block, escape prefix, pass-through,
// rejected execute, infra error, panic) exits 0 silently so the prompt flows on.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/webconfig"
)

// hookRunTimeout caps the in-process intercept engine so a slow classify/execute
// can never stall the agent's prompt. On timeout the shim fails open (silent
// pass-through), consistent with every other non-clean-match outcome.
const hookRunTimeout = 5 * time.Second

// hookCmd is the `kitsoki hook` command group: the agent-side adapters for the
// pre-LLM intercept engine.
func hookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Wire the pre-LLM intercept gate into a coding agent's prompt lifecycle",
		Long: `hook sub-commands:
  kitsoki hook run --agent claude        — UserPromptSubmit shim (reads prompt JSON on stdin)
  kitsoki hook install --agent claude    — add the run hook to .claude/settings.json

Only Claude Code has a true pre-model hook today (UserPromptSubmit). The shim
classifies the prompt through kitsoki's no-LLM routing tiers and, on a clean
confident match, executes it directly and blocks the prompt with the result —
otherwise it fails open and the prompt proceeds to the model untouched.

See docs/architecture/prompt-intercept.md.`,
	}
	cmd.AddCommand(hookRunCmd())
	cmd.AddCommand(hookInstallCmd())
	return cmd
}

// hookPromptInput is the UserPromptSubmit JSON Claude Code pipes to the run
// shim on stdin. Only the three fields the shim uses are decoded; unknown
// fields are ignored.
type hookPromptInput struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

// hookBlockDecision is the JSON the run shim writes to stdout on a clean
// intercept. {"decision":"block","reason":<report>} tells Claude Code to NOT
// send the prompt to the model and to surface <reason> to the user.
type hookBlockDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// hookRunCmd implements `kitsoki hook run --agent claude`.
func hookRunCmd() *cobra.Command {
	var (
		agentFlag  string
		configFlag string
	)

	cmd := &cobra.Command{
		Use:           "run --agent claude",
		Short:         "UserPromptSubmit shim: intercept a piped prompt or pass it through (fail-open)",
		SilenceErrors: true, // every non-clean-match outcome is a silent exit-0 pass-through
		SilenceUsage:  true,
		Long: `Read Claude Code's UserPromptSubmit JSON ({"prompt","session_id","cwd"}) from
stdin, classify the prompt through kitsoki's no-LLM routing tiers against the
intercept: binding in .kitsoki.yaml (resolved from the JSON's cwd), and:

  • on a clean confident no-LLM match — execute it directly (no LLM) and print
    {"decision":"block","reason":<report>} so the agent shows kitsoki's answer
    instead of calling the model;
  • on anything else (no intercept block / disabled, escape prefix, pass-through,
    rejected execute, OR any error/timeout/panic) — exit 0 with empty stdout so
    the prompt proceeds to the model untouched.

This is FAIL-OPEN by design: a misconfigured, slow, or crashing interceptor
must never wedge the agent. Only a clean, confident match blocks.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if agentFlag != "claude" {
				// No other agent has a pre-model hook to run against today.
				// Fail open: do nothing, let the prompt proceed.
				return nil
			}
			report, ok := runClaudeHook(cmd.Context(), cmd.InOrStdin(), configFlag)
			if !ok {
				// Pass-through / error / not-applicable: silent exit 0.
				return nil
			}
			// Clean intercept: emit the block decision.
			enc := json.NewEncoder(cmd.OutOrStdout())
			if err := enc.Encode(hookBlockDecision{Decision: "block", Reason: report}); err != nil {
				// Even an encode failure must fail open: swallow and pass through.
				return nil
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&agentFlag, "agent", "claude", "coding agent whose prompt-submit hook this serves (claude)")
	cmd.Flags().StringVar(&configFlag, "config", webconfig.DefaultConfigFile, "config file (relative to the prompt cwd) carrying the intercept: binding")
	return cmd
}

// runClaudeHook performs the Claude UserPromptSubmit decision over stdin. It
// returns (report, true) ONLY on a clean confident intercept; every other
// outcome — including any error or panic — returns ("", false) so the caller
// fails open. The named recover is the last line of the fail-open guarantee:
// even a panic in the engine resolves to a silent pass-through.
func runClaudeHook(ctx context.Context, stdin io.Reader, configFlag string) (report string, blocked bool) {
	// Fail-open panic guard: a panic anywhere below resolves to pass-through.
	defer func() {
		if r := recover(); r != nil {
			report, blocked = "", false
		}
	}()

	raw, err := io.ReadAll(stdin)
	if err != nil {
		return "", false
	}
	var in hookPromptInput
	if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Prompt) == "" {
		// Malformed payload or empty prompt: nothing to intercept, pass through.
		return "", false
	}

	// Resolve the config relative to the prompt's cwd (the agent's project dir),
	// falling back to the process cwd when the JSON omits it. We do NOT chdir —
	// the engine takes absolute/relative app paths from the resolved config.
	cwd := in.Cwd
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	configPath := configFlag
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(cwd, configFlag)
	}

	cfg, err := webconfig.Load(configPath)
	if err != nil {
		// A broken/unreadable config must not wedge the agent: pass through.
		return "", false
	}
	ic := cfg.Intercept
	if ic == nil || !ic.Enabled {
		// No binding / disabled: the hook is a no-op. Pass through silently.
		return "", false
	}

	// Forced pass-through: an escape prefix opts the prompt out of interception.
	if ic.EscapePrefix != "" && strings.HasPrefix(in.Prompt, ic.EscapePrefix) {
		return "", false
	}

	// App paths in the config are written relative to the config file; resolve
	// them against the prompt cwd so the engine loads the right story.
	appPath := ic.App
	if !filepath.IsAbs(appPath) {
		appPath = filepath.Join(cwd, ic.App)
	}

	// Cap the engine so a slow classify/execute fails open rather than stalls.
	runCtx, cancel := context.WithTimeout(ctx, hookRunTimeout)
	defer cancel()

	res, err := runInterceptEngine(runCtx, interceptEngineInput{
		AppPath: appPath,
		Room:    ic.Room,
		Input:   in.Prompt,
		Bar:     ic.ConfidenceBar, // resolveIntercept defaulted a zero bar to 0.90
		// Logger nil ⇒ engine discards intercept.* events (the hook has no trace sink).
	})
	if err != nil {
		// Infra error (missing app, bad world, classify/execute failure): the
		// CARDINAL fail-open. Never block on an interceptor that itself failed.
		return "", false
	}

	if !res.Matched {
		return "", false
	}

	// Multi-turn binding: don't OneShot — drive a persisted session to rest
	// SYNCHRONOUSLY (the hook blocks) under kitsoki's OWN budget, decoupled from
	// the 5s classify cap. The classify above ran fast under runCtx; the drive
	// gets a fresh, generous context of its own. Any setup failure fails open.
	if res.MultiTurn {
		return runClaudeHookDrive(ctx, appPath, in, ic, res)
	}

	// Stateless fast path: only a clean confident match that actually executed
	// blocks. Pass-through (Exit 10) or rejected (Exit 1) reaches the model.
	if res.OneShot == nil {
		return "", false
	}
	switch res.Exit {
	case 0, turnExitTerminal: // transitioned or terminal: a real outcome to show
		return composeInterceptReport(res.Intent, ic.EscapePrefix, res.OneShot), true
	default: // rejected (1), defensive-clarify (10), anything else: pass through
		return "", false
	}
}

// runClaudeHookDrive handles the multi-turn intercept path: it loads the bound
// app, drives the matched command to rest on a persisted session under
// interceptDriveBudget (NOT the 5s classify cap), and returns the composed
// drive report to block the prompt with. Fail-open governs every setup error.
// The drive blocks for as long as the resolution takes — the installed Claude
// hook `timeout` (interceptHookTimeoutSeconds) must exceed it.
func runClaudeHookDrive(ctx context.Context, appPath string, in hookPromptInput, ic *webconfig.InterceptConfig, res interceptResult) (string, bool) {
	def, err := loadAppWithEnv(appPath)
	if err != nil {
		return "", false // fail open
	}

	driveCtx, cancel := context.WithTimeout(ctx, interceptDriveBudget)
	defer cancel()

	out, err := driveInterceptToRest(driveCtx, driveConfig{
		AppPath:    appPath,
		Def:        def,
		DBPath:     defaultDBPath(),
		Input:      in.Prompt,
		Intent:     res.Intent,
		Slots:      res.Slots,
		WorkingDir: in.Cwd, // run git in the prompt's repo
	})
	if err != nil {
		// Drive infrastructure failure (couldn't build the runtime / boot): fail
		// open. DriveToRest already safe-aborts every NON-infra failure internally
		// and returns a normal outcome, so a clean tree is still guaranteed.
		return "", false
	}

	// A rejected command (guard failed / not allowed in the booted hub) started
	// nothing and is not a real outcome to show — fail open so the prompt reaches
	// the model, mirroring the fast path's rejected-execute pass-through.
	if !out.Resolved && !out.Aborted {
		return "", false
	}

	return composeDriveReport(res.Intent, ic.EscapePrefix, out), true
}

// composeInterceptReport renders the marked report a clean intercept blocks the
// prompt with. The first line is always the marked attribution so the user
// knows kitsoki — not the agent's model — answered; bullets summarise each
// host.* side-effect; the outcome line is the first non-empty line of the
// rendered view (fallback "done."), tagged with the trace note. When an
// escape_prefix is configured, a final line tells the user how to bypass kitsoki
// and reach the agent next time (the block reason is the user's only window into
// the intercept, so the escape has to be discoverable from it).
//
//	⌁ kitsoki handled this (no LLM) — <intent>
//	  • <namespace> <short summary>
//	  • …
//	<first non-empty view line>   ·   ⟲ recorded in the kitsoki trace
//	↳ prefix "<escape>" to skip kitsoki and send the prompt to the agent
func composeInterceptReport(intent, escapePrefix string, res *orchestrator.OneShotResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⌁ kitsoki handled this (no LLM) — %s\n", intent)
	for _, hc := range res.HostCalls {
		fmt.Fprintf(&b, "  • %s\n", summarizeHostCall(hc))
	}
	fmt.Fprintf(&b, "%s   ·   ⟲ recorded in the kitsoki trace", firstNonEmptyLine(res.View, "done."))
	// When an escape is configured, surface it: a blocked prompt is the user's
	// only window into the intercept, so the bypass must be visible right here.
	if escapePrefix != "" {
		fmt.Fprintf(&b, "\n↳ prefix %q to skip kitsoki and send the prompt to the agent", escapePrefix)
	}
	return b.String()
}

// summarizeHostCall renders one host.* invocation as a terse "<namespace>
// <key=value …>" bullet body. Args win as the summary source (they describe the
// call's intent); when empty, the result Data is summarised instead; an Error is
// appended so a failed host call is visible in the report. Maps are rendered
// with sorted keys so the bullet is deterministic.
func summarizeHostCall(hc orchestrator.HostCallSummary) string {
	parts := []string{hc.Namespace}
	if kv := summarizeArgs(hc.Args); kv != "" {
		parts = append(parts, kv)
	} else if kv := summarizeArgs(hc.Data); kv != "" {
		parts = append(parts, "→ "+kv)
	}
	if hc.Error != "" {
		parts = append(parts, "(error: "+hc.Error+")")
	}
	return strings.Join(parts, " ")
}

// summarizeArgs renders a map as space-joined key=value pairs with sorted keys,
// truncating long values so a bullet stays one terse line. Returns "" for an
// empty map.
func summarizeArgs(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+truncateValue(m[k]))
	}
	return strings.Join(pairs, " ")
}

// truncateValue renders a value compactly (strings unquoted) and clips it to a
// short cap so one bullet never sprawls across the report.
func truncateValue(v any) string {
	s := fmt.Sprintf("%v", v)
	s = strings.ReplaceAll(s, "\n", " ")
	const cap = 48
	if len(s) > cap {
		return s[:cap-1] + "…"
	}
	return s
}

// firstNonEmptyLine returns the first line of s that is non-empty after
// trimming, or fallback when s has no such line.
func firstNonEmptyLine(s, fallback string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return fallback
}

// sortStrings sorts a string slice in place (a tiny local helper kept here so
// the report composer has no import-cycle temptation; the slice is always small).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// hookInstallCmd implements `kitsoki hook install --agent <agent>`.
func hookInstallCmd() *cobra.Command {
	var (
		agentFlag    string
		settingsFlag string
		writeFlag    bool
	)

	cmd := &cobra.Command{
		Use:           "install --agent claude [--write] [--settings <path>]",
		Short:         "Install the pre-LLM intercept hook into a coding agent's settings",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Compute the settings entry that runs the kitsoki pre-LLM intercept shim on
every prompt, and (with --write) merge it idempotently into the agent's settings.

Default behavior prints a dry-run diff and writes nothing; pass --write to apply.

Only Claude Code has a pre-model hook today (UserPromptSubmit). For --agent codex
or --agent copilot this prints an honest "no pre-model hook" message and writes
nothing. See docs/architecture/prompt-intercept.md.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch agentFlag {
			case "claude":
				return installClaudeHook(out, settingsFlag, writeFlag)
			case "codex":
				fmt.Fprintln(out, codexNoHookMessage)
				return nil
			case "copilot":
				fmt.Fprintln(out, copilotNoHookMessage)
				return nil
			default:
				return fmt.Errorf("unknown --agent %q (want claude|codex|copilot)", agentFlag)
			}
		},
	}

	cmd.Flags().StringVar(&agentFlag, "agent", "claude", "coding agent to install the hook into (claude|codex|copilot)")
	cmd.Flags().StringVar(&settingsFlag, "settings", "", "settings.json path (default: .claude/settings.json under cwd)")
	cmd.Flags().BoolVar(&writeFlag, "write", false, "apply the change (default: print a dry-run diff and write nothing)")
	return cmd
}

const codexNoHookMessage = `Codex has no pre-model interception hook today: its hook surface is
PreToolUse/PostToolUse only (a tool has already been chosen by the model by
then), so a prompt can't be answered no-LLM before the model sees it.
Nothing was installed. See docs/architecture/prompt-intercept.md.`

const copilotNoHookMessage = `Copilot has no pre-model interception hook today: its userPromptSubmitted
event is observe-only — it can't block or replace the prompt before the model
runs. Nothing was installed. See docs/architecture/prompt-intercept.md.`

// hookCommandString is the command Claude Code runs for every prompt. It is the
// identity used for idempotent install (an entry with this command is "already
// present").
const hookCommandString = "kitsoki hook run --agent claude"

// defaultClaudeSettingsPath returns the project-local Claude settings path under
// the process cwd when --settings is unset.
func defaultClaudeSettingsPath(settingsFlag string) (string, error) {
	if settingsFlag != "" {
		return settingsFlag, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return filepath.Join(wd, ".claude", "settings.json"), nil
}

// installClaudeHook computes the UserPromptSubmit entry and either prints a
// dry-run diff (write=false) or idempotently merges it into the settings JSON
// (write=true), creating the file/dirs when absent and preserving unrelated keys.
func installClaudeHook(out io.Writer, settingsFlag string, write bool) error {
	path, err := defaultClaudeSettingsPath(settingsFlag)
	if err != nil {
		return err
	}

	existing, hadFile, err := readSettings(path)
	if err != nil {
		return err
	}

	updated, changed := mergeClaudeHook(existing)

	if !write {
		// Dry-run: print a unified-ish diff of the JSON and DO NOT touch the file.
		printSettingsDiff(out, path, existing, updated, hadFile, changed)
		return nil
	}

	if !changed {
		fmt.Fprintf(out, "%s already contains the kitsoki UserPromptSubmit hook; nothing to do.\n", path)
		return nil
	}

	if err := writeSettings(path, updated); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote the kitsoki UserPromptSubmit hook to %s\n", path)
	return nil
}

// readSettings reads and parses a Claude settings.json into a generic map. A
// missing file yields an empty map and hadFile=false (install will create it);
// a present-but-malformed file is a hard error (we will not clobber a file we
// can't safely merge into).
func readSettings(path string) (settings map[string]any, hadFile bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, false, nil
		}
		return nil, false, fmt.Errorf("read settings %q: %w", path, err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, true, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, true, fmt.Errorf("parse settings %q: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, true, nil
}

// writeSettings marshals the settings map (indented, trailing newline) and
// writes it, creating parent dirs as needed.
func writeSettings(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write settings %q: %w", path, err)
	}
	return nil
}

// interceptHookTimeoutSeconds is the Claude `timeout` (seconds) written onto the
// installed UserPromptSubmit entry. Claude defaults a UserPromptSubmit hook to a
// 30s timeout and KILLS the process past it — which would strand a tree mid-
// rebase during a multi-turn conflict drive. So the installer raises it well
// past kitsoki's own interceptDriveBudget (15m), leaving headroom for kitsoki to
// reach safe-abort first. See docs/architecture/prompt-intercept.md §"Multi-turn
// commands" and the stderr spike.
const interceptHookTimeoutSeconds = 1200 // 20m > interceptDriveBudget (15m)

// mergeClaudeHook returns a deep-ish copy of settings with the kitsoki
// UserPromptSubmit hook merged in, plus changed=false when the entry was already
// present WITH the right timeout (idempotent). The Claude hooks shape is:
//
//	"hooks": { "UserPromptSubmit": [ { "hooks": [ { "type":"command",
//	          "command":"kitsoki hook run --agent claude",
//	          "timeout": 1200 } ] } ] }
//
// The `timeout` is load-bearing (see interceptHookTimeoutSeconds): a pre-existing
// entry missing it — or carrying a too-short one — is reconciled in place to the
// required value (changed=true), so re-running install upgrades an older install.
// Unrelated keys (other hook events, other top-level settings) are preserved.
func mergeClaudeHook(settings map[string]any) (updated map[string]any, changed bool) {
	updated = cloneJSONMap(settings)

	hooks, _ := updated["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	events, _ := hooks["UserPromptSubmit"].([]any)

	// Reconcile an existing entry's timeout in place if the command is present.
	if h := findClaudeHookEntry(events); h != nil {
		if hookTimeoutSeconds(h["timeout"]) == interceptHookTimeoutSeconds {
			return updated, false // fully present, correct timeout: idempotent
		}
		h["timeout"] = interceptHookTimeoutSeconds
		hooks["UserPromptSubmit"] = events
		updated["hooks"] = hooks
		return updated, true
	}

	entry := map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": hookCommandString,
				"timeout": interceptHookTimeoutSeconds,
			},
		},
	}
	events = append(events, entry)
	hooks["UserPromptSubmit"] = events
	updated["hooks"] = hooks
	return updated, true
}

// hookTimeoutSeconds coerces a settings `timeout` value to an int regardless of
// whether it arrived as a JSON number (float64, from a re-read settings file) or
// an int (from an in-memory merge). A missing/odd value reads as 0.
func hookTimeoutSeconds(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	default:
		return 0
	}
}

// findClaudeHookEntry returns the inner command-hook map running
// hookCommandString in the UserPromptSubmit event list, or nil. The returned map
// is a live reference into events (a clone of the caller's settings), so callers
// may mutate it in place to reconcile fields like `timeout`.
func findClaudeHookEntry(events []any) map[string]any {
	for _, ev := range events {
		evMap, ok := ev.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := evMap["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range inner {
			hMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, _ := hMap["command"].(string); cmd == hookCommandString {
				return hMap
			}
		}
	}
	return nil
}

// claudeHookPresent reports whether the UserPromptSubmit event list already
// carries an inner command hook running hookCommandString.
func claudeHookPresent(events []any) bool {
	return findClaudeHookEntry(events) != nil
}

// printSettingsDiff prints a terse unified-ish before/after of the settings JSON
// for the dry-run path. When nothing would change it says so explicitly.
func printSettingsDiff(out io.Writer, path string, before, after map[string]any, hadFile, changed bool) {
	if !changed {
		fmt.Fprintf(out, "%s already contains the kitsoki UserPromptSubmit hook; no changes.\n", path)
		return
	}
	beforeJSON := renderSettings(before, hadFile)
	afterJSON := renderSettings(after, true)

	fmt.Fprintf(out, "dry-run (no --write): would update %s\n", path)
	fmt.Fprintf(out, "--- %s%s\n", path, fileNote(hadFile))
	fmt.Fprintf(out, "+++ %s (after)\n", path)
	for _, line := range strings.Split(beforeJSON, "\n") {
		fmt.Fprintf(out, "- %s\n", line)
	}
	for _, line := range strings.Split(afterJSON, "\n") {
		fmt.Fprintf(out, "+ %s\n", line)
	}
	fmt.Fprintf(out, "\nre-run with --write to apply; the hook command is: %s\n", hookCommandString)
}

// renderSettings pretty-prints a settings map, or "(file does not exist)" for an
// absent before-state.
func renderSettings(m map[string]any, present bool) string {
	if !present && len(m) == 0 {
		return "(file does not exist)"
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Sprintf("(unrenderable: %v)", err)
	}
	return string(b)
}

func fileNote(hadFile bool) string {
	if hadFile {
		return " (before)"
	}
	return " (before — file does not exist)"
}

// cloneJSONMap deep-copies a JSON-shaped map (maps, slices, scalars) so the
// merge never mutates the caller's parsed settings in place.
func cloneJSONMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneJSONValue(v)
	}
	return out
}

func cloneJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneJSONMap(t)
	case []any:
		s := make([]any, len(t))
		for i, e := range t {
			s[i] = cloneJSONValue(e)
		}
		return s
	default:
		return v
	}
}
