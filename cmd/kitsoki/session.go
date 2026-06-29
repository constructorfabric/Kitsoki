// session.go — implements `kitsoki session ...` subcommands (proposal §3.4).
//
// The `loop.py` ↔ kitsoki contract is exactly these subcommands, keyed by
// (transport, thread). One session per (transport, thread); a writer lock
// serializes concurrent invocations.
//
// Output is JSON to stdout for orchestrator-friendliness; human-readable
// summaries are written to stderr where applicable.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/transport"
	"kitsoki/internal/world"
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
contract between kitsoki and external orchestrators (loop.py, future
webhook receivers).

Subcommands:
  kitsoki session create   --app <path> --key <transport:thread>
  kitsoki session continue --app <path> --key <transport:thread> --intent <name> [--slots JSON]
  kitsoki session continue --app <path> --key <transport:thread> --raw "<reply body>"
  kitsoki session show     --app <path> (--key <transport:thread> | --id <session-id>)
  kitsoki session list     --app <path> [--transport <name>]
  kitsoki session bind-key --app <path> --id <session-id> --key <transport:thread>

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
	cmd.AddCommand(sessionCheckpointCmd())
	cmd.AddCommand(sessionJournalCmd())
	cmd.AddCommand(sessionForgetCmd())
	cmd.AddCommand(sessionDetachCmd())
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
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&key, "key", "", "external key transport:thread")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID")
	return cmd
}

// ─── session checkpoint ───────────────────────────────────────────────────────

// sessionCheckpointCmd forces a checkpoint on the journal for one session.
// For each live document (world, state) it writes a "<doc>.checkpoint" row
// into the journal table carrying the full current document value.
// With --doc only that single document is checkpointed.
//
// Phase-A note: chats/<id> and jobs/<id> checkpoint entries are not yet emitted
// because the SQLite-backed journal Writer lands in a parallel wave; the
// checkpoint rows for world/state are written directly via raw SQL so this
// command is already functional once the journal table exists.
func sessionCheckpointCmd() *cobra.Command {
	var (
		appPath string
		dbPath  string
		idFlag  string
		keyFlag string
		docFlag string
	)
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Force a journal checkpoint for a session (continue-mode §7.2)",
		Long: `Write a full-document checkpoint entry to the journal table for the
named session. Useful for seeding test fixtures and bounding replay cost.

One checkpoint row per document (world, state) is inserted with kind
"world.checkpoint" / "state.checkpoint" and body {"full": <current value>}.
With --doc only that one document is checkpointed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (idFlag == "") == (keyFlag == "") {
				return fmt.Errorf("exactly one of --id or --key must be set")
			}

			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()
			sid, err := resolveSessionID(ctx, s, keyFlag, idFlag)
			if err != nil {
				return err
			}

			m, err := machine.New(def)
			if err != nil {
				return fmt.Errorf("build machine: %w", err)
			}
			orch := orchestrator.New(def, m, s, &noRunHarness{})

			var outcome []map[string]any
			lockErr := s.WithWriterLock(ctx, sid, func() error {
				journey, jErr := orch.LoadJourney(sid)
				if jErr != nil {
					return fmt.Errorf("load journey: %w", jErr)
				}

				db := s.DB()

				// Determine which docs to checkpoint.
				type docSpec struct {
					doc      string
					kind     string
					bodyJSON []byte
				}
				var docs []docSpec

				maybeAdd := func(doc, kind string, value any) error {
					if docFlag != "" && docFlag != doc {
						return nil
					}
					b, mErr := json.Marshal(map[string]any{"full": value})
					if mErr != nil {
						return fmt.Errorf("marshal %s checkpoint body: %w", doc, mErr)
					}
					docs = append(docs, docSpec{doc: doc, kind: kind, bodyJSON: b})
					return nil
				}

				if err := maybeAdd("world", "world.checkpoint", journey.World.Vars); err != nil {
					return err
				}
				if err := maybeAdd("state", "state.checkpoint", string(journey.State)); err != nil {
					return err
				}

				if len(docs) == 0 {
					return fmt.Errorf("unknown --doc value %q; expected world or state", docFlag)
				}

				tsNow := time.Now().UnixMicro()
				turnN := int64(journey.Turn)

				for i, d := range docs {
					// Determine next doc_version for this (session, doc) pair.
					var maxVer sql.NullInt64
					_ = db.QueryRowContext(ctx,
						`SELECT MAX(doc_version) FROM journal WHERE session_id = ? AND doc = ?`,
						string(sid), d.doc,
					).Scan(&maxVer)
					nextVer := int64(1)
					if maxVer.Valid {
						nextVer = maxVer.Int64 + 1
					}

					// Find next seq for this (session, turn) pair.
					var maxSeq sql.NullInt64
					_ = db.QueryRowContext(ctx,
						`SELECT MAX(seq) FROM journal WHERE session_id = ? AND turn = ?`,
						string(sid), turnN,
					).Scan(&maxSeq)
					seq := int64(0)
					if maxSeq.Valid {
						seq = maxSeq.Int64 + 1
					} else {
						seq = int64(i)
					}

					_, execErr := db.ExecContext(ctx,
						`INSERT INTO journal (session_id, turn, seq, ts, kind, doc, doc_version, body_json)
						 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
						string(sid), turnN, seq, tsNow, d.kind, d.doc, nextVer, string(d.bodyJSON),
					)
					if execErr != nil {
						return fmt.Errorf("insert checkpoint for %s: %w", d.doc, execErr)
					}
					outcome = append(outcome, map[string]any{
						"doc":         d.doc,
						"doc_version": nextVer,
					})
				}
				return nil
			})
			if errors.Is(lockErr, store.ErrSessionBusy) {
				fmt.Fprintf(cmd.ErrOrStderr(), "session busy: another process holds the writer lock for %s\n", sid)
				return errTempFail
			}
			if lockErr != nil {
				return lockErr
			}

			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"session_id":  string(sid),
				"checkpoints": outcome,
			})
		},
	}
	cmd.Flags().StringVar(&appPath, "app", "", "path to app.yaml (required)")
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID")
	cmd.Flags().StringVar(&keyFlag, "key", "", "external key transport:thread")
	cmd.Flags().StringVar(&docFlag, "doc", "", "checkpoint only this document (world|state); default: all")
	_ = cmd.MarkFlagRequired("app")
	return cmd
}

// ─── session journal ──────────────────────────────────────────────────────────

// sessionJournalCmd dumps journal entries for one session as JSONL to stdout.
// Each output line is a JSON object matching the journal.Entry shape (§2.2).
// Flags --from and --doc narrow the result set.
func sessionJournalCmd() *cobra.Command {
	var (
		dbPath      string
		idFlag      string
		keyFlag     string
		fromVersion int64
		docFlag     string
	)
	cmd := &cobra.Command{
		Use:   "journal",
		Short: "Dump journal entries for a session as JSONL (continue-mode §7.2)",
		Long: `Stream journal entries for the named session to stdout, one JSON object
per line. Suitable for piping into jq or diffing against fixtures.

--from <version>  emit only entries with doc_version > <version> (exclusive)
--doc <doc>       emit only entries for the named document`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (idFlag == "") == (keyFlag == "") {
				return fmt.Errorf("exactly one of --id or --key must be set")
			}

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()
			sid, err := resolveSessionID(ctx, s, keyFlag, idFlag)
			if err != nil {
				return err
			}

			q := `SELECT session_id, turn, seq, ts, kind,
			             COALESCE(doc, ''), COALESCE(doc_version, 0), body_json
			      FROM journal
			      WHERE session_id = ?`
			queryArgs := []any{string(sid)}

			if docFlag != "" {
				q += " AND doc = ?"
				queryArgs = append(queryArgs, docFlag)
			}
			if fromVersion > 0 {
				q += " AND doc_version > ?"
				queryArgs = append(queryArgs, fromVersion)
			}
			q += " ORDER BY turn ASC, seq ASC"

			rows, err := s.DB().QueryContext(ctx, q, queryArgs...)
			if err != nil {
				return fmt.Errorf("query journal: %w", err)
			}
			defer rows.Close()

			enc := json.NewEncoder(cmd.OutOrStdout())
			for rows.Next() {
				var (
					sessionID  string
					turn       int64
					seq        int
					tsUS       int64
					kind       string
					doc        string
					docVersion int64
					bodyJSON   string
				)
				if err := rows.Scan(&sessionID, &turn, &seq, &tsUS, &kind, &doc, &docVersion, &bodyJSON); err != nil {
					return fmt.Errorf("scan journal row: %w", err)
				}
				entry := map[string]any{
					"session_id": sessionID,
					"turn":       turn,
					"seq":        seq,
					"ts":         time.UnixMicro(tsUS).UTC().Format(time.RFC3339Nano),
					"ev":         kind,
					"body":       json.RawMessage(bodyJSON),
				}
				if doc != "" {
					entry["doc"] = doc
				}
				if docVersion != 0 {
					entry["doc_version"] = docVersion
				}
				if err := enc.Encode(entry); err != nil {
					return fmt.Errorf("encode journal entry: %w", err)
				}
			}
			return rows.Err()
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID")
	cmd.Flags().StringVar(&keyFlag, "key", "", "external key transport:thread")
	cmd.Flags().Int64Var(&fromVersion, "from", 0, "emit entries with doc_version > this value (exclusive lower bound)")
	cmd.Flags().StringVar(&docFlag, "doc", "", "filter to entries for this document (e.g. world, state)")
	return cmd
}

// ─── session forget ───────────────────────────────────────────────────────────

// sessionForgetCmd deletes a session and ALL related data: the narrow
// DeleteSession tables plus journal, timeouts, chats/chat_messages/chat_locks
// (for chats whose session_id = this session), jobs/notifications (by
// session_id), inside a single atomic transaction.
//
// Requires --yes or interactive confirmation on stdin.
func sessionForgetCmd() *cobra.Command {
	var (
		dbPath  string
		idFlag  string
		keyFlag string
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   "forget",
		Short: "Wide-delete a session and all related data (continue-mode §6.9)",
		Long: `Permanently delete a session and every row that references it:
  events, snapshots, external_keys, session_locks, sessions
  journal (if the table exists)
  timeouts (rows for this session)
  chats + chat_messages + chat_locks (chats whose session_id = this session)
  jobs + notifications (rows for this session)

All deletes run inside a single transaction.  Requires --yes or interactive
confirmation to prevent accidental data loss.

Output JSON: {session_id, deleted_tables: {events: N, snapshots: N, ...}}`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (idFlag == "") == (keyFlag == "") {
				return fmt.Errorf("exactly one of --id or --key must be set")
			}

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()
			sid, err := resolveSessionID(ctx, s, keyFlag, idFlag)
			if err != nil {
				return err
			}

			// Require confirmation unless --yes was passed.
			if !yes {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Permanently delete session %s and all related data? [y/N]: ", sid)
				scanner := bufio.NewScanner(cmd.InOrStdin())
				if !scanner.Scan() {
					// EOF / I/O error (e.g. piped or closed stdin): treat as a
					// non-confirmation and abort the destructive delete, but
					// surface the I/O condition rather than reporting a bare
					// "Aborted." that looks like a deliberate decline.
					if err := scanner.Err(); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(),
							"Aborted: cannot read confirmation from stdin: %v\n", err)
					} else {
						fmt.Fprintln(cmd.ErrOrStderr(),
							"Aborted: no confirmation on stdin (EOF); not deleting.")
					}
					return nil
				}
				answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
				if answer != "y" && answer != "yes" {
					fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}

			db := s.DB()
			counts := map[string]int64{}

			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("begin transaction: %w", err)
			}
			defer func() { _ = tx.Rollback() }()

			// Helper: exec a DELETE and record the affected count. If the table
			// doesn't exist (SQLITE_ERROR / "no such table"), skip silently.
			execDelete := func(table, query string, args ...any) error {
				res, execErr := tx.ExecContext(ctx, query, args...)
				if execErr != nil {
					if strings.Contains(execErr.Error(), "no such table") {
						return nil
					}
					return fmt.Errorf("delete %s: %w", table, execErr)
				}
				n, _ := res.RowsAffected()
				counts[table] += n
				return nil
			}

			sidStr := string(sid)

			// Core session tables (existing DeleteSession scope).
			if err := execDelete("events", `DELETE FROM events WHERE session_id = ?`, sidStr); err != nil {
				return err
			}
			if err := execDelete("snapshots", `DELETE FROM snapshots WHERE session_id = ?`, sidStr); err != nil {
				return err
			}
			if err := execDelete("external_keys", `DELETE FROM external_keys WHERE session_id = ?`, sidStr); err != nil {
				return err
			}
			if err := execDelete("session_locks", `DELETE FROM session_locks WHERE session_id = ?`, sidStr); err != nil {
				return err
			}

			// Journal (may not exist yet if the parallel DDL hasn't landed).
			if err := execDelete("journal", `DELETE FROM journal WHERE session_id = ?`, sidStr); err != nil {
				return err
			}

			// Timeouts (owned by the orchestrator's own table, session_id FK).
			if err := execDelete("timeouts", `DELETE FROM timeouts WHERE session_id = ?`, sidStr); err != nil {
				return err
			}

			// Chats: chats.session_id is an "audit only" nullable column that
			// records the last kitsoki session that drove a chat. This is the
			// closest FK we have for §6.9 "any chat whose app_id+session_id
			// references this session". We delete chat_messages and chat_locks
			// for matching chat IDs first, then the chat rows themselves.
			//
			// NOTE: the chats schema stores session_id as TEXT (nullable); there
			// is no hard FK constraint. We treat a non-null session_id match as
			// the ownership signal per the proposal's intent.
			chatMsgDel := `DELETE FROM chat_messages WHERE chat_id IN
			               (SELECT id FROM chats WHERE session_id = ?)`
			if err := execDelete("chat_messages", chatMsgDel, sidStr); err != nil {
				return err
			}
			chatLockDel := `DELETE FROM chat_locks WHERE chat_id IN
			                (SELECT id FROM chats WHERE session_id = ?)`
			if err := execDelete("chat_locks", chatLockDel, sidStr); err != nil {
				return err
			}
			// chat_pty_sessions + chat_input_queue (added by
			// claude-code-sessions). Soft-skip via "no such table" so
			// forget keeps working on databases predating that feature.
			chatPTYDel := `DELETE FROM chat_pty_sessions WHERE chat_id IN
			               (SELECT id FROM chats WHERE session_id = ?)`
			if err := execDelete("chat_pty_sessions", chatPTYDel, sidStr); err != nil {
				return err
			}
			// chat_input_queue carries origin_session_id directly — the kitsoki
			// session that enqueued the drive — so we can delete by it, not by
			// chat membership. (A single drive's lifetime is owned by the
			// session that spawned it.)
			chatQueueDel := `DELETE FROM chat_input_queue WHERE origin_session_id = ?`
			if err := execDelete("chat_input_queue", chatQueueDel, sidStr); err != nil {
				return err
			}
			if err := execDelete("chats", `DELETE FROM chats WHERE session_id = ?`, sidStr); err != nil {
				return err
			}

			// Jobs and notifications.
			if err := execDelete("notifications", `DELETE FROM notifications WHERE session_id = ?`, sidStr); err != nil {
				return err
			}
			if err := execDelete("jobs", `DELETE FROM jobs WHERE session_id = ?`, sidStr); err != nil {
				return err
			}

			// Sessions row last (so a partial failure leaves the session resolvable).
			res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sidStr)
			if err != nil {
				return fmt.Errorf("delete sessions row: %w", err)
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return store.ErrSessionNotFound
			}
			counts["sessions"] = n

			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit forget transaction: %w", err)
			}

			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"session_id":     sidStr,
				"deleted_tables": counts,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID")
	cmd.Flags().StringVar(&keyFlag, "key", "", "external key transport:thread")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip interactive confirmation")
	return cmd
}

// ─── session detach ───────────────────────────────────────────────────────────

// sessionDetachCmd breaks a stale writer lock on a session.
// It reads the session_locks row and refuses to delete it if the owner PID is
// alive on the current host. If the owning process is dead (or from a
// different host), it removes the lock row.
func sessionDetachCmd() *cobra.Command {
	var (
		dbPath  string
		idFlag  string
		keyFlag string
	)
	cmd := &cobra.Command{
		Use:   "detach",
		Short: "Break a stale writer lock on a session (continue-mode §7.2)",
		Long: `Read the session_locks row for the named session and remove it if the
owning process is dead (or the lock was acquired on a different host).

If the lock owner is alive on this host, the command refuses with an error.
If no lock row exists, exits 0 with {"detached":false}.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (idFlag == "") == (keyFlag == "") {
				return fmt.Errorf("exactly one of --id or --key must be set")
			}

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()
			sid, err := resolveSessionID(ctx, s, keyFlag, idFlag)
			if err != nil {
				return err
			}

			db := s.DB()

			var (
				ownerPID     int
				ownerHost    string
				acquiredAtUS int64
			)
			err = db.QueryRowContext(ctx,
				`SELECT owner_pid, owner_host, acquired_at FROM session_locks WHERE session_id = ?`,
				string(sid),
			).Scan(&ownerPID, &ownerHost, &acquiredAtUS)
			if errors.Is(err, sql.ErrNoRows) {
				fmt.Fprintln(cmd.ErrOrStderr(), "no lock held")
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"session_id": string(sid),
					"detached":   false,
				})
			}
			if err != nil {
				return fmt.Errorf("read session lock: %w", err)
			}

			priorOwner := map[string]any{
				"pid":         ownerPID,
				"host":        ownerHost,
				"acquired_at": time.UnixMicro(acquiredAtUS).UTC().Format(time.RFC3339),
			}

			// Determine this host.
			thisHost, _ := os.Hostname()

			if ownerHost == thisHost {
				// Same host: probe process liveness via signal 0.
				if pidAlive(ownerPID) {
					return fmt.Errorf(
						"session %s is locked by a live process (pid=%d host=%s acquired_at=%s); "+
							"use 'kill %d' to stop it first",
						sid, ownerPID, ownerHost,
						time.UnixMicro(acquiredAtUS).UTC().Format(time.RFC3339),
						ownerPID,
					)
				}
			}
			// Either cross-host (cannot probe) or dead PID — delete the lock.
			if _, delErr := db.ExecContext(ctx,
				`DELETE FROM session_locks WHERE session_id = ?`, string(sid),
			); delErr != nil {
				return fmt.Errorf("delete session lock: %w", delErr)
			}

			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"session_id":  string(sid),
				"detached":    true,
				"prior_owner": priorOwner,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID")
	cmd.Flags().StringVar(&keyFlag, "key", "", "external key transport:thread")
	return cmd
}

// pidAlive uses signal 0 to check whether a process with the given PID is
// alive on this host. Mirrors the logic in internal/store/external_keys.go.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		// EPERM means the process exists but we lack permission to signal it.
		return true
	}
	return false
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
			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}

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
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&key, "key", "", "external key transport:thread (e.g. jira:PLTFRM-12345)")
	_ = cmd.MarkFlagRequired("app")
	return cmd
}

// ─── session continue ─────────────────────────────────────────────────────────

func sessionContinueCmd() *cobra.Command {
	var (
		appPath       string
		dbPath        string
		key           string
		idFlag        string
		intentName    string
		slotsFlag     string
		rawText       string
		harnessType   string
		claudeModel   string
		recordingPath string
		tracePath     string // --trace override; "" = use default JSONL path when key is known
		execModeFlag  string
	)
	cmd := &cobra.Command{
		Use:   "continue",
		Short: "Continue a session with one inbound event (intent or raw reply body)",
		Long: `Run one turn against an existing session, identified by --key
or --id. Exactly one of --intent or --raw must be set.

--intent <name> [--slots JSON] takes the direct path: the call goes
straight to the machine, bypassing the LLM harness. Use this when the
orchestrator has already mapped the inbound event to a known intent.

--raw "<body>" takes the LLM-routed path: kitsoki's harness maps the
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

			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()

			sid, err := resolveSessionID(ctx, s, key, idFlag)
			if err != nil {
				// If key lookup failed and --trace is provided, try JSONL recovery.
				// This supports the Jenkins/Jira workflow where SQLite is deleted/unavailable
				// but the JSONL trace is saved (e.g., in a Jira ticket) and restored later.
				if key != "" && tracePath != "" && errors.Is(err, store.ErrSessionNotFound) {
					// Session not in SQLite, but JSONL trace exists.
					// Recover state from JSONL and create a new session.
					if newSID, recoverErr := recoverSessionFromJSONL(ctx, tracePath, def, s, key); recoverErr == nil {
						sid = newSID
						fmt.Fprintf(cmd.ErrOrStderr(),
							"note: recovered session from JSONL trace (session %s)\n",
							sid)
					} else {
						// JSONL recovery failed; return original error
						return err
					}
				} else {
					return err
				}
			}

			slotVals, err := decodeJSONFlag(slotsFlag, "slots")
			if err != nil {
				return err
			}

			m, err := machine.New(def)
			if err != nil {
				return fmt.Errorf("build machine: %w", err)
			}

			h, err := buildTurnHarness(harnessType, recordingPath, def, intentName != "")
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

			rawChatStore, chatStoreErr := chats.NewStore(s.DB())
			var chatStoreOpt orchestrator.Option
			if chatStoreErr == nil {
				chatStoreOpt = orchestrator.WithChatStore(chathost.NewAdapter(rawChatStore))
			}

			// Build the journal writer (continue-mode §4.9 Rule 1).
			// Shares the same *sql.DB so dual-write transactions are possible.
			// Built before the job store so it can be passed via
			// jobs.WithJobJournalWriter (mirrors cmd/kitsoki/main.go § run).
			var journalWriterOpt orchestrator.Option
			var journalReaderOpt orchestrator.Option
			jw, jwErr := journal.NewSQLiteWriter(s.DB())
			if jwErr == nil {
				journalWriterOpt = orchestrator.WithJournalWriter(jw)
			}
			// Build the journal reader (symmetric to the writer; §4.5 resume path).
			if jr, jrErr := journal.NewSQLiteReader(s.DB()); jrErr == nil {
				journalReaderOpt = orchestrator.WithJournalReader(jr)
			}

			// Build the job store + scheduler so on_enter effects with
			// `background: true` (host.agent.ask_with_mcp in phase_12_6,
			// phase_minus_1, …) actually dispatch asynchronously and their
			// on_complete: chains fire.  Without this wiring the
			// orchestrator's WithScheduler doc explicitly states that
			// `background: true` is silently ignored — the work runs
			// synchronously but on_complete never fires, leaving the
			// session pinned in `_executing` forever (Phase 12.6 leapfrog
			// root cause, observed against ABR-429271).  Same *sql.DB as
			// the session store so we stay at one SQLite file.
			var jobStore *jobs.JobStore
			var jobScheduler jobs.Scheduler
			var schedulerOpt, jobStoreOpt orchestrator.Option
			if jwErr == nil {
				var jsErr error
				jobStore, jsErr = jobs.NewJobStore(s.DB(), jobs.WithJobJournalWriter(jw))
				if jsErr == nil {
					jobScheduler = jobs.NewScheduler(jobStore)
					schedulerOpt = orchestrator.WithScheduler(jobScheduler)
					jobStoreOpt = orchestrator.WithJobStore(jobStore)
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: open job store failed (%v); background dispatches will be silently dropped\n",
						jsErr)
				}
			}

			// Wave 3-entry: open (or create) a JSONL trace for this session.
			// Resolution: use --trace if provided; otherwise derive the default
			// path from (app.ID, transport, thread) when --key is set.
			// When only --id is provided we have no transport:thread to key the
			// path, so we fall back to SQLite-only event writes for that session.
			var jsonlSink *store.JSONLSink
			resolvedTracePath := tracePath
			if resolvedTracePath == "" && key != "" {
				transport, thread, kErr := parseExternalKey(key)
				if kErr == nil {
					resolvedTracePath = store.DefaultTracePath(def.App.ID, transport, thread)
				}
			}
			if resolvedTracePath != "" {
				if mkErr := os.MkdirAll(filepath.Dir(resolvedTracePath), 0o755); mkErr == nil {
					if sink, sinkErr := store.OpenJSONL(resolvedTracePath); sinkErr == nil {
						jsonlSink = sink
						defer func() { _ = jsonlSink.Close() }()
					} else {
						fmt.Fprintf(cmd.ErrOrStderr(),
							"warning: open JSONL trace %q failed (%v); falling back to SQLite-only writes\n",
							resolvedTracePath, sinkErr)
					}
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: create trace dir for %q failed (%v); falling back to SQLite-only writes\n",
						resolvedTracePath, mkErr)
				}
			}

			orchOpts := []orchestrator.Option{
				orchestrator.WithHostRegistry(hostReg),
				orchestrator.WithTransportRegistry(transportReg),
			}
			if jsonlSink != nil {
				orchOpts = append(orchOpts, orchestrator.WithEventSink(jsonlSink))
			}
			if chatStoreOpt != nil {
				orchOpts = append(orchOpts, chatStoreOpt)
			}
			if chatStoreErr == nil {
				orchOpts = append(orchOpts, orchestrator.WithChatsConcrete(rawChatStore))
			}
			if journalWriterOpt != nil {
				orchOpts = append(orchOpts, journalWriterOpt)
			}
			if journalReaderOpt != nil {
				orchOpts = append(orchOpts, journalReaderOpt)
			}
			if schedulerOpt != nil {
				orchOpts = append(orchOpts, schedulerOpt)
			}
			if jobStoreOpt != nil {
				orchOpts = append(orchOpts, jobStoreOpt)
			}
			// Execution mode (execution-modes proposal). Defaults to
			// one-shot here — like `kitsoki turn` — so the scripted drive
			// path (and the debugging skill's session continue --intent)
			// keeps walking pipelines. Pass --mode staged to pause at
			// decision gates.
			switch execModeFlag {
			case "", "one-shot", "oneshot":
				// one-shot is the orchestrator zero value; no option needed.
			case "staged":
				orchOpts = append(orchOpts, orchestrator.WithExecutionMode(orchestrator.ExecStaged))
			default:
				return fmt.Errorf("--mode %q is invalid (want \"staged\" or \"one-shot\")", execModeFlag)
			}
			if d := def.Decider; d != nil {
				orchOpts = append(orchOpts, orchestrator.WithDecider(orchestrator.DeciderConfig{
					Agent: d.Agent, Schema: d.Schema, Prompt: d.Prompt, Threshold: d.Threshold,
				}))
			}
			orch := orchestrator.New(def, m, s, h, orchOpts...)

			// NewSession spawns the per-session terminal-event listener for
			// fresh sessions; for an existing session resolved by key we
			// must wire it explicitly before the synchronous turn runs.
			// Without the listener the scheduler dispatches the job but
			// `handleJobTerminal` never fires, so on_complete: chains are
			// dropped on the floor.
			orch.EnsureSessionListener(sid)

			// Record the effective story into the trace (base snapshot on a
			// fresh session; diff if the on-disk story drifted from what the
			// trace already carries) so the session trace stays self-contained.
			if recErr := orch.RecordEffectiveStory(ctx, sid); recErr != nil {
				return fmt.Errorf("record effective story: %w", recErr)
			}

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
				return errTempFail
			}
			if lockErr != nil {
				return lockErr
			}

			// Drain any background dispatches kicked off during this turn
			// before the process exits.  Without this block the bg
			// goroutine and the session listener are killed in flight when
			// the CLI command returns, so on_complete: never lands.  Loop
			// until the persisted state stops advancing — phase_12_6 →
			// phase_13 chains another bg LLM call from its own on_enter,
			// and a single drain pass would leave that one orphaned too.
			// Bounded by maxDrainPasses to avoid grinding forever on a
			// genuinely-broken story.
			if jobScheduler != nil {
				const maxDrainPasses = 8
				const drainTimeout = 10 * time.Minute
				for pass := 0; pass < maxDrainPasses; pass++ {
					drainCtx, drainCancel := context.WithTimeout(ctx, drainTimeout)
					schedErr := jobScheduler.WaitIdle(drainCtx)
					listenerErr := orch.WaitListenerIdle(drainCtx, sid)
					drainCancel()
					if schedErr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(),
							"warning: scheduler drain timed out on pass %d: %v\n",
							pass+1, schedErr)
						break
					}
					if listenerErr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(),
							"warning: listener drain timed out on pass %d: %v\n",
							pass+1, listenerErr)
						break
					}
					// Check if the persisted state advanced into another
					// `_executing` room that may have dispatched its own
					// bg work.  If yes, loop and drain again; otherwise,
					// settle.
					journey, lerr := orch.LoadJourney(sid)
					if lerr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(),
							"warning: load journey after drain pass %d failed: %v\n",
							pass+1, lerr)
						break
					}
					state := string(journey.State)
					if !strings.HasSuffix(state, "_executing") {
						break
					}
					// Still in an _executing room: a second bg job may
					// be in flight (e.g. phase_13_executing's
					// host.agent.ask_with_mcp).  Loop to drain it.
				}
				// Refresh the outcome view from persisted state so the
				// JSON return reflects the post-drain settlement (loop.py
				// reads new_state to decide its next dispatch).
				if outcome != nil {
					if journey, lerr := orch.LoadJourney(sid); lerr == nil {
						outcome.NewState = journey.State
						outcome.TurnNumber = journey.Turn
					}
				}
			}

			return writeJSON(cmd.OutOrStdout(), turnOutcomeView(sid, outcome))
		},
	}
	cmd.Flags().StringVar(&appPath, "app", "", "path to app.yaml (required)")
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&key, "key", "", "external key transport:thread (e.g. jira:PLTFRM-12345)")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID (alternative to --key)")
	cmd.Flags().StringVar(&intentName, "intent", "", "intent name to dispatch directly (no LLM)")
	cmd.Flags().StringVar(&slotsFlag, "slots", "", "intent slots as JSON or @file. With --intent: full slot set passed to SubmitDirect. With --raw: supplemental slots merged into the harness-resolved intent (existing keys are preserved).")
	cmd.Flags().StringVar(&rawText, "raw", "", "raw inbound reply body, routed through the harness")
	cmd.Flags().StringVar(&execModeFlag, "mode", "one-shot", `execution mode: "one-shot" (auto-advance, default) or "staged" (stop at decision gates)`)
	cmd.Flags().StringVar(&harnessType, "harness", "", "harness for --raw: claude|live|replay (default auto)")
	cmd.Flags().StringVar(&claudeModel, "claude-model", "", "model passed to claude -p --model")
	cmd.Flags().StringVar(&recordingPath, "recording", "", "recording YAML for --harness replay")
	cmd.Flags().StringVar(&tracePath, "trace", "",
		"JSONL trace file for event writes; default: ~/.kitsoki/sessions/<app>/<sha8>-<slug>.jsonl (derived from --key)")
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

			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
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
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
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
			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}

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
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
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

			if _, err := loadAppWithEnv(appPath); err != nil {
				return err
			}

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
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
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
		return "", store.ErrSessionNotFound // Return the original error for recovery logic to detect
	}
	return sid, err
}

// recoverSessionFromJSONL recovers a session from a JSONL trace when SQLite is unavailable.
// This supports the Jenkins/Jira workflow where the trace is saved externally but the
// session database is deleted or never exists on the recovery host.
//
// Flow:
//  1. Read and verify the JSONL trace
//  2. Create a new session in SQLite
//  3. Append the recovered events to the new session
//  4. Bind the external key to the new session
//  5. Return the new session ID
//
// Note: The recovered events are appended with their original turn/seq numbers.
// LoadHistory will replay from the latest snapshot or from turn 0 if no snapshot.
// The recovered state is implicit in the event sequence.
func recoverSessionFromJSONL(ctx context.Context, tracePath string, def *app.AppDef,
	s store.Store, key string) (app.SessionID, error) {

	// Open and read the JSONL trace.
	jsonlStore, err := store.OpenJSONL(tracePath)
	if err != nil {
		return "", fmt.Errorf("open JSONL trace %q: %w", tracePath, err)
	}
	defer func() { _ = jsonlStore.Close() }()

	history := jsonlStore.History()
	if len(history) == 0 {
		return "", fmt.Errorf("JSONL trace %q is empty; cannot recover session", tracePath)
	}

	// Verify the trace can be replayed by rebuilding the journey.
	rootState := app.StatePath(def.Root.(string))
	_, err = store.BuildJourney(def, rootState, world.New(), history)
	if err != nil {
		return "", fmt.Errorf("rebuild journey from JSONL trace: %w", err)
	}

	// Create a new session in SQLite.
	newSID, err := s.CreateSession(ctx, def)
	if err != nil {
		return "", fmt.Errorf("create session for recovery: %w", err)
	}

	// Append all recovered events to the new session.
	// The events maintain their original turn/seq structure, which is safe
	// because they're being appended to a different session (unique by session_id).
	if err := s.AppendEvents(newSID, history); err != nil {
		return "", fmt.Errorf("append recovered events to session: %w", err)
	}

	// Bind the external key to the new session.
	transport, thread, err := parseExternalKey(key)
	if err != nil {
		return "", err
	}
	if err := s.BindExternalKey(ctx, newSID, transport, thread); err != nil {
		return "", fmt.Errorf("bind external key to recovered session: %w", err)
	}

	return newSID, nil
}

// publishAppDir sets KITSOKI_APP_DIR so host handlers can resolve relative paths.
//
// IMPORTANT: call this BEFORE app.Load(appPath) when the app yaml may
// reference ${KITSOKI_APP_DIR} in any env-expanded field (today: the
// meta_modes[*].cwd and agents[*].cwd loader checks). The validator
// fires during app.Load and errors out if KITSOKI_APP_DIR is unset,
// so a post-load setenv is too late. The loadAppWithEnv helper below
// orders the calls correctly — prefer it over hand-rolling the pair.
func publishAppDir(appPath string) {
	if absPath, err := filepath.Abs(appPath); err == nil {
		_ = os.Setenv(host.AppDirEnv, filepath.Dir(absPath))
	}
}

// loadAppWithEnv is the canonical "publish KITSOKI_APP_DIR, then load
// the app yaml" sequence. Every cmd/kitsoki entry point that calls
// app.Load(appPath) should go through this helper so authors can
// safely write `cwd: "${KITSOKI_APP_DIR}/foo"` in app.yaml without
// the loader's env-var validator (loader.go::expandMetaCwd) tripping
// on a not-yet-set var.
//
// Errors are formatted identically to the previous inline "load app
// %q: %w" wrapper so call-site diagnostics stay stable.
func loadAppWithEnv(appPath string) (*app.AppDef, error) {
	// A top-level `@kitsoki/<name>` arg resolves from the embedded story library
	// (or the --kitsoki-repo / $KITSOKI_REPO override) — so a binary-only user
	// with no kitsoki checkout can launch a shipped story directly, e.g.
	// `kitsoki run @kitsoki/dev-story` to start project onboarding. The same
	// resolver the loader uses for `@kitsoki/<name>` *imports* finds the file.
	if resolved, rerr := resolveKitsokiAppArg(appPath); rerr != nil {
		return nil, rerr
	} else if resolved != "" {
		appPath = resolved
	}
	publishAppDir(appPath)
	// Inject the @kitsoki/<name> import resolver (--kitsoki-repo override ›
	// on-disk kitsoki root › embedded library). A foreign repo carrying only a
	// `source: "@kitsoki/dev-story"` instance loads against the embedded
	// library with no kitsoki checkout present.
	def, err := app.LoadWithResolver(appPath, nil, buildImportResolver())
	if err != nil {
		return nil, fmt.Errorf("load app %q: %w", appPath, err)
	}
	return def, nil
}

// resolveKitsokiAppArg maps a top-level `@kitsoki/<name>` app argument to a
// concrete app.yaml path via the same precedence the import resolver uses
// (--kitsoki-repo / $KITSOKI_REPO override, then the embedded story library).
// Returns ("", nil) for an ordinary file path so callers leave it untouched.
func resolveKitsokiAppArg(appPath string) (string, error) {
	const prefix = "@kitsoki/"
	if !strings.HasPrefix(appPath, prefix) {
		return "", nil
	}
	name := strings.TrimPrefix(appPath, prefix)
	resolver := buildImportResolver()
	// override=true first (explicit checkout wins); then the embedded fallback.
	if path, err := resolver(name, "", true); err != nil {
		return "", err
	} else if path != "" {
		return path, nil
	}
	return resolver(name, "", false)
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
// `kitsoki session continue` invocation. Always includes a TUITransport
// (in-process buffer); adds a JiraTransport when the JIRA_URL,
// JIRA_USERNAME, and JIRA_API_TOKEN env vars are all set; adds a
// BitbucketTransport when a Bitbucket Bearer token is discoverable
// (either via $BITBUCKET_TOKEN or the standard Acronis location
// “~/.config/acronis/bitbucket-token“).
//
// Setting JIRA_INSECURE_SKIP_VERIFY=1 disables TLS verification on the
// Jira HTTP client.  This is needed for internal/self-hosted instances
// behind an enterprise zero-trust proxy with a self-signed certificate
// whose CA is not on the system trust store.  Off by default — only
// opt in deliberately.
//
// The Bitbucket client always skips TLS verification because it defaults
// to the Acronis ZTA proxy mount (https://localhost:3128/bitbucket) which
// presents a self-signed cert; same convention as tools/loopy.
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

	if bbToken := loadBitbucketToken(); bbToken != "" {
		cfg := transport.BitbucketConfig{Token: bbToken}
		if base := os.Getenv("BITBUCKET_BASE_URL"); base != "" {
			cfg.BaseURL = base
		}
		bt, err := transport.NewBitbucketTransport(cfg)
		if err != nil {
			return nil, fmt.Errorf("build bitbucket transport: %w", err)
		}
		reg.Register(bt)
	}
	return reg, nil
}

// loadBitbucketToken resolves the Bitbucket bearer token.  Preference:
//  1. $BITBUCKET_TOKEN (test overrides + CI),
//  2. “~/.config/acronis/bitbucket-token“ (the standard Acronis location
//     read by tools/loopy/bugfix/lib/creds.bitbucket_token).
//
// Returns the empty string when neither source is available so
// buildTransportRegistry can simply skip registering the transport rather
// than failing the whole session.
func loadBitbucketToken() string {
	if env := strings.TrimSpace(os.Getenv("BITBUCKET_TOKEN")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "acronis", "bitbucket-token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
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
