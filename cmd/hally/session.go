// session.go — implements `hally session ...` subcommands (proposal §3.4).
//
// The `loop.py` ↔ hally contract is exactly these subcommands, keyed by
// (transport, thread). One session per (transport, thread); a writer lock
// serializes concurrent invocations.
//
// Output is JSON to stdout for orchestrator-friendliness; human-readable
// summaries are written to stderr where applicable.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hally/internal/app"
	"hally/internal/host"
	"hally/internal/machine"
	"hally/internal/orchestrator"
	"hally/internal/store"
	"hally/internal/transport"
)

// EX_TEMPFAIL is the BSD/sysexits.h "temporary failure" exit code that
// loop.py-style orchestrators can recognize as "back off and retry"
// (proposal §3.3).
const EX_TEMPFAIL = 75

// sessionCmd is the parent of session create/continue/show/list/bind-key.
func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage persistent singleton sessions keyed by (transport, thread)",
		Long: `Sessions are persistent singletons addressed by an external key of
the form transport:thread (e.g. jira:PLTFRM-12345). Used as the
contract between hally and external orchestrators (loop.py, future
webhook receivers).

Subcommands:
  hally session create   --app <path> --key <transport:thread>
  hally session continue --app <path> --key <transport:thread> --intent <name> [--slots JSON]
  hally session continue --app <path> --key <transport:thread> --raw "<reply body>"
  hally session show     --app <path> (--key <transport:thread> | --id <session-id>)
  hally session list     --app <path> [--transport <name>]
  hally session bind-key --app <path> --id <session-id> --key <transport:thread>

Exit codes:
  0   success
  1   generic error
  75  EX_TEMPFAIL: another process holds the writer lock for this session.
      Orchestrators should back off and retry.`,
	}
	cmd.AddCommand(sessionCreateCmd())
	cmd.AddCommand(sessionContinueCmd())
	cmd.AddCommand(sessionShowCmd())
	cmd.AddCommand(sessionListCmd())
	cmd.AddCommand(sessionBindKeyCmd())
	cmd.AddCommand(sessionDeleteCmd())
	return cmd
}

// ─── session delete ───────────────────────────────────────────────────────────

func sessionDeleteCmd() *cobra.Command {
	var (
		dbPath string
		key    string
		idFlag string
	)
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a session and all of its associated rows",
		Long: `Delete removes a session — its events, snapshots, external-key bindings,
and locks — atomically.  Intended for testing and operator-driven cleanup
of abandoned sessions; production code should prefer ` + "`session continue --intent quit`" + `
or the equivalent terminal-state path so the audit trail is preserved.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (key == "") == (idFlag == "") {
				return fmt.Errorf("exactly one of --key or --id must be set")
			}

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()
			sid, err := resolveSessionID(ctx, s, key, idFlag)
			if err != nil {
				return err
			}

			if err := s.DeleteSession(ctx, sid); err != nil {
				return fmt.Errorf("delete session %s: %w", sid, err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"deleted":    true,
				"session_id": string(sid),
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/hally/sessions.db)")
	cmd.Flags().StringVar(&key, "key", "", "external key transport:thread")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID")
	return cmd
}

// ─── session create ───────────────────────────────────────────────────────────

func sessionCreateCmd() *cobra.Command {
	var (
		appPath string
		dbPath  string
		key     string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new session and optionally bind an external key",
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := app.Load(appPath)
			if err != nil {
				return fmt.Errorf("load app %q: %w", appPath, err)
			}
			publishAppDir(appPath)

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()
			sid, err := s.CreateSession(ctx, def)
			if err != nil {
				return fmt.Errorf("create session: %w", err)
			}

			out := map[string]any{
				"session_id": string(sid),
				"app_id":     def.App.ID,
			}

			if key != "" {
				transport, thread, parseErr := parseExternalKey(key)
				if parseErr != nil {
					return parseErr
				}
				if err := s.BindExternalKey(ctx, sid, transport, thread); err != nil {
					return fmt.Errorf("bind key %q: %w", key, err)
				}
				out["key"] = key
				out["transport"] = transport
				out["thread"] = thread
			}

			return writeJSON(cmd.OutOrStdout(), out)
		},
	}
	cmd.Flags().StringVar(&appPath, "app", "", "path to app.yaml (required)")
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/hally/sessions.db)")
	cmd.Flags().StringVar(&key, "key", "", "external key transport:thread (e.g. jira:PLTFRM-12345)")
	_ = cmd.MarkFlagRequired("app")
	return cmd
}

// ─── session continue ─────────────────────────────────────────────────────────

func sessionContinueCmd() *cobra.Command {
	var (
		appPath     string
		dbPath      string
		key         string
		idFlag      string
		intentName  string
		slotsFlag   string
		rawText     string
		harnessType string
		claudeModel string
		oraclePath  string
	)
	cmd := &cobra.Command{
		Use:   "continue",
		Short: "Continue a session with one inbound event (intent or raw reply body)",
		Long: `Run one turn against an existing session, identified by --key
or --id. Exactly one of --intent or --raw must be set.

--intent <name> [--slots JSON] takes the direct path: the call goes
straight to the machine, bypassing the LLM harness. Use this when the
orchestrator has already mapped the inbound event to a known intent.

--raw "<body>" takes the LLM-routed path: hally's harness maps the
text to one of the current state's allowed intents using each intent's
examples and slot schema. Use this for free-form replies (Jira/Bitbucket
comment bodies).

The session writer lock is held for the duration of one turn. If
another process holds it, this command exits 75 (EX_TEMPFAIL).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (intentName == "") == (rawText == "") {
				return fmt.Errorf("exactly one of --intent or --raw must be set")
			}
			if (key == "") == (idFlag == "") {
				return fmt.Errorf("exactly one of --key or --id must be set")
			}

			def, err := app.Load(appPath)
			if err != nil {
				return fmt.Errorf("load app %q: %w", appPath, err)
			}
			publishAppDir(appPath)

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()

			sid, err := resolveSessionID(ctx, s, key, idFlag)
			if err != nil {
				return err
			}

			slotVals, err := decodeJSONFlag(slotsFlag, "slots")
			if err != nil {
				return err
			}

			m, err := machine.New(def)
			if err != nil {
				return fmt.Errorf("build machine: %w", err)
			}

			h, err := buildTurnHarness(harnessType, oraclePath, def, intentName != "")
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()
			_ = claudeModel // reserved for future per-call tuning

			hostReg := host.NewRegistry()
			host.RegisterBuiltins(hostReg)
			if err := hostReg.ValidateAllowList(def.Hosts); err != nil {
				return fmt.Errorf("validate hosts: %w", err)
			}

			transportReg, transportErr := buildTransportRegistry()
			if transportErr != nil {
				return transportErr
			}
			defer func() { _ = transportReg.Close() }()

			orch := orchestrator.New(def, m, s, h,
				orchestrator.WithHostRegistry(hostReg),
				orchestrator.WithTransportRegistry(transportReg),
			)

			var outcome *orchestrator.TurnOutcome
			lockErr := s.WithWriterLock(ctx, sid, func() error {
				var inner error
				if intentName != "" {
					outcome, inner = orch.SubmitDirect(ctx, sid, intentName, slotVals)
				} else {
					// --slots, when provided alongside --raw, is forwarded
					// as supplemental slots: the harness still classifies
					// the text and resolves the intent, but the supplied
					// slots are merged into the resulting call (without
					// overwriting any keys the harness produced).  Lets an
					// orchestrator attach per-turn metadata such as
					// `last_reply_author` for ACL guards.
					var turnOpts []orchestrator.TurnOption
					if len(slotVals) > 0 {
						turnOpts = append(turnOpts, orchestrator.WithSupplementSlots(slotVals))
					}
					outcome, inner = orch.Turn(ctx, sid, rawText, turnOpts...)
				}
				return inner
			})
			if errors.Is(lockErr, store.ErrSessionBusy) {
				fmt.Fprintf(cmd.ErrOrStderr(), "session busy: another process holds the writer lock for %s\n", sid)
				os.Exit(EX_TEMPFAIL)
			}
			if lockErr != nil {
				return lockErr
			}

			return writeJSON(cmd.OutOrStdout(), turnOutcomeView(sid, outcome))
		},
	}
	cmd.Flags().StringVar(&appPath, "app", "", "path to app.yaml (required)")
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/hally/sessions.db)")
	cmd.Flags().StringVar(&key, "key", "", "external key transport:thread (e.g. jira:PLTFRM-12345)")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID (alternative to --key)")
	cmd.Flags().StringVar(&intentName, "intent", "", "intent name to dispatch directly (no LLM)")
	cmd.Flags().StringVar(&slotsFlag, "slots", "", "intent slots as JSON or @file. With --intent: full slot set passed to SubmitDirect. With --raw: supplemental slots merged into the harness-resolved intent (existing keys are preserved).")
	cmd.Flags().StringVar(&rawText, "raw", "", "raw inbound reply body, routed through the harness")
	cmd.Flags().StringVar(&harnessType, "harness", "", "harness for --raw: claude|live|replay (default auto)")
	cmd.Flags().StringVar(&claudeModel, "claude-model", "", "model passed to claude -p --model")
	cmd.Flags().StringVar(&oraclePath, "oracle", "", "oracle YAML for --harness replay")
	_ = cmd.MarkFlagRequired("app")
	return cmd
}

// ─── session show ─────────────────────────────────────────────────────────────

func sessionShowCmd() *cobra.Command {
	var (
		appPath string
		dbPath  string
		key     string
		idFlag  string
	)
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show a session's current state, world, and bound external keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			if (key == "") == (idFlag == "") {
				return fmt.Errorf("exactly one of --key or --id must be set")
			}

			def, err := app.Load(appPath)
			if err != nil {
				return fmt.Errorf("load app %q: %w", appPath, err)
			}
			publishAppDir(appPath)

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()
			sid, err := resolveSessionID(ctx, s, key, idFlag)
			if err != nil {
				return err
			}

			m, err := machine.New(def)
			if err != nil {
				return fmt.Errorf("build machine: %w", err)
			}
			orch := orchestrator.New(def, m, s, &noRunHarness{})

			journey, err := orch.LoadJourney(sid)
			if err != nil {
				return fmt.Errorf("load journey: %w", err)
			}

			keys, err := s.ListExternalKeys(ctx, sid)
			if err != nil {
				return fmt.Errorf("list keys: %w", err)
			}
			view, _ := orch.RenderState(journey.State, journey.World)

			out := map[string]any{
				"session_id":    string(sid),
				"app_id":        def.App.ID,
				"state":         string(journey.State),
				"world":         journey.World.Vars,
				"turn":          int64(journey.Turn),
				"view":          view,
				"external_keys": externalKeysView(keys),
			}
			return writeJSON(cmd.OutOrStdout(), out)
		},
	}
	cmd.Flags().StringVar(&appPath, "app", "", "path to app.yaml (required)")
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/hally/sessions.db)")
	cmd.Flags().StringVar(&key, "key", "", "external key transport:thread")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID")
	_ = cmd.MarkFlagRequired("app")
	return cmd
}

// ─── session list ─────────────────────────────────────────────────────────────

func sessionListCmd() *cobra.Command {
	var (
		appPath   string
		dbPath    string
		transport string
		limit     int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sessions; optionally filtered by transport",
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := app.Load(appPath)
			if err != nil {
				return fmt.Errorf("load app %q: %w", appPath, err)
			}
			publishAppDir(appPath)

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()
			var summaries []store.SessionSummary
			if transport != "" {
				summaries, err = s.ListSessionsByTransport(ctx, transport, limit)
			} else {
				summaries, err = s.ListSessions(ctx, def.App.ID, limit)
			}
			if err != nil {
				return fmt.Errorf("list sessions: %w", err)
			}

			rows := make([]map[string]any, 0, len(summaries))
			for _, sum := range summaries {
				keys, _ := s.ListExternalKeys(ctx, sum.ID)
				rows = append(rows, map[string]any{
					"session_id":    string(sum.ID),
					"app_id":        sum.AppID,
					"app_version":   sum.AppVersion,
					"started_at":    sum.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
					"last_turn":     int64(sum.LastTurn),
					"status":        sum.Status,
					"external_keys": externalKeysView(keys),
				})
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"sessions": rows})
		},
	}
	cmd.Flags().StringVar(&appPath, "app", "", "path to app.yaml (required)")
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/hally/sessions.db)")
	cmd.Flags().StringVar(&transport, "transport", "", "filter by transport (e.g. jira)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max rows to return (0 = no limit)")
	_ = cmd.MarkFlagRequired("app")
	return cmd
}

// ─── session bind-key ─────────────────────────────────────────────────────────

func sessionBindKeyCmd() *cobra.Command {
	var (
		appPath string
		dbPath  string
		idFlag  string
		key     string
	)
	cmd := &cobra.Command{
		Use:   "bind-key",
		Short: "Bind an additional (transport, thread) key to an existing session",
		RunE: func(cmd *cobra.Command, args []string) error {
			if idFlag == "" || key == "" {
				return fmt.Errorf("--id and --key are both required")
			}

			_, err := app.Load(appPath)
			if err != nil {
				return fmt.Errorf("load app %q: %w", appPath, err)
			}
			publishAppDir(appPath)

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			transport, thread, err := parseExternalKey(key)
			if err != nil {
				return err
			}
			if err := s.BindExternalKey(cmd.Context(), app.SessionID(idFlag), transport, thread); err != nil {
				return fmt.Errorf("bind: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"session_id": idFlag,
				"key":        key,
				"transport":  transport,
				"thread":     thread,
			})
		},
	}
	cmd.Flags().StringVar(&appPath, "app", "", "path to app.yaml (required)")
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/hally/sessions.db)")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID (required)")
	cmd.Flags().StringVar(&key, "key", "", "external key transport:thread (required)")
	_ = cmd.MarkFlagRequired("app")
	return cmd
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// parseExternalKey splits a "transport:thread" string. Returns an error if the
// shape is invalid or either side is empty.
func parseExternalKey(key string) (transport, thread string, err error) {
	idx := strings.Index(key, ":")
	if idx <= 0 || idx == len(key)-1 {
		return "", "", fmt.Errorf("--key %q must be in transport:thread form (e.g. jira:PLTFRM-12345)", key)
	}
	return key[:idx], key[idx+1:], nil
}

// resolveSessionID converts either --key or --id into a session ID.
// Exactly one must be non-empty (caller verifies).
func resolveSessionID(ctx context.Context, s store.Store, key, idFlag string) (app.SessionID, error) {
	if idFlag != "" {
		return app.SessionID(idFlag), nil
	}
	transport, thread, err := parseExternalKey(key)
	if err != nil {
		return "", err
	}
	sid, err := s.LookupByKey(ctx, transport, thread)
	if errors.Is(err, store.ErrSessionNotFound) {
		return "", fmt.Errorf("no session bound to %s", key)
	}
	return sid, err
}

// publishAppDir sets HALLY_APP_DIR so host handlers can resolve relative paths.
func publishAppDir(appPath string) {
	if absPath, err := filepath.Abs(appPath); err == nil {
		_ = os.Setenv(host.AppDirEnv, filepath.Dir(absPath))
	}
}

// openSessionStore opens the session DB at the given path or the default.
func openSessionStore(dbPath string) (store.Store, error) {
	if dbPath == "" {
		dbPath = defaultDBPath()
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	return s, nil
}

// writeJSON encodes v as indented JSON.
func writeJSON(w interface{ Write([]byte) (int, error) }, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// externalKeysView projects a []ExternalKey into JSON-friendly rows.
func externalKeysView(keys []store.ExternalKey) []map[string]any {
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{
			"transport":  k.Transport,
			"thread":     k.Thread,
			"created_at": k.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	return out
}

// buildTransportRegistry constructs the transport.Registry used during a
// `hally session continue` invocation. Always includes a TUITransport
// (in-process buffer); adds a JiraTransport when the JIRA_URL,
// JIRA_USERNAME, and JIRA_API_TOKEN env vars are all set.
//
// Setting JIRA_INSECURE_SKIP_VERIFY=1 disables TLS verification on the
// Jira HTTP client.  This is needed for internal/self-hosted instances
// behind a proxy with a self-signed certificate (e.g. Acronis's ZTA
// proxy at 127.0.0.1:3128 with `zta-proxy-ca.crt` not on the system
// trust store).  Off by default — only opt in deliberately.
func buildTransportRegistry() (*transport.Registry, error) {
	reg := transport.NewRegistry()
	reg.Register(transport.NewTUITransport())

	jiraURL := os.Getenv("JIRA_URL")
	jiraUser := os.Getenv("JIRA_USERNAME")
	jiraToken := os.Getenv("JIRA_API_TOKEN")
	if jiraURL != "" && jiraUser != "" && jiraToken != "" {
		cfg := transport.JiraConfig{
			BaseURL:  jiraURL,
			Username: jiraUser,
			APIToken: jiraToken,
		}
		if os.Getenv("JIRA_INSECURE_SKIP_VERIFY") == "1" {
			cfg.HTTPClient = &http.Client{
				Timeout: 30 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				},
			}
		}
		jt, err := transport.NewJiraTransport(cfg)
		if err != nil {
			return nil, fmt.Errorf("build jira transport: %w", err)
		}
		reg.Register(jt)
	}
	return reg, nil
}

// turnOutcomeView projects a TurnOutcome into a stable JSON shape suitable
// for orchestrators to ingest.
func turnOutcomeView(sid app.SessionID, o *orchestrator.TurnOutcome) map[string]any {
	if o == nil {
		return map[string]any{"session_id": string(sid)}
	}
	out := map[string]any{
		"session_id":      string(sid),
		"mode":            o.Mode.String(),
		"new_state":       string(o.NewState),
		"view":            o.View,
		"allowed_intents": o.AllowedIntents,
		"turn":            int64(o.TurnNumber),
	}
	if o.Mode == orchestrator.ModeClarify {
		out["pending_intent"] = o.PendingIntent
		out["pending_slots"] = o.PendingSlots
		out["slots_needed"] = o.SlotsNeeded
	}
	if o.Mode == orchestrator.ModeRejected {
		out["error_code"] = string(o.ErrorCode)
		out["error_message"] = o.ErrorMessage
		if o.GuardHint != "" {
			out["guard_hint"] = o.GuardHint
		}
	}
	return out
}
