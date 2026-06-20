-- Chats, messages, and locks tables.
-- Applied idempotently by chats.NewStore via //go:embed.

CREATE TABLE IF NOT EXISTS chats (
    id                  TEXT    NOT NULL PRIMARY KEY,   -- ULID
    app_id              TEXT    NOT NULL,               -- the app the room belongs to
    room                TEXT    NOT NULL,               -- state path: "agent", "bugfix.phase_3"
    scope_key           TEXT    NOT NULL DEFAULT '',    -- free-form disambiguator (e.g. "PROJ-123")
    title               TEXT    NOT NULL,
    status              TEXT    NOT NULL,               -- active|paused|completed|archived
    claude_session_id   TEXT,                           -- for `claude -p --session-id`
    parent_chat_id      TEXT,                           -- non-null on forks
    session_id          TEXT,                           -- last kitsoki session that drove this chat (audit only)
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    last_active_at      INTEGER NOT NULL
) STRICT;
CREATE INDEX IF NOT EXISTS chats_room_scope ON chats(app_id, room, scope_key, last_active_at DESC);
CREATE INDEX IF NOT EXISTS chats_status     ON chats(status, last_active_at DESC);
CREATE INDEX IF NOT EXISTS chats_parent     ON chats(parent_chat_id);

CREATE TABLE IF NOT EXISTS chat_messages (
    chat_id     TEXT    NOT NULL,
    seq         INTEGER NOT NULL,
    role        TEXT    NOT NULL CHECK (role IN ('user','assistant','system','tool')),
    content     TEXT    NOT NULL,
    metadata    TEXT,                    -- JSON: tool calls, mcp validation, etc.
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (chat_id, seq)
) STRICT;

CREATE TABLE IF NOT EXISTS chat_locks (
    chat_id      TEXT    NOT NULL PRIMARY KEY,
    owner_pid    INTEGER NOT NULL,
    owner_host   TEXT    NOT NULL,
    acquired_at  INTEGER NOT NULL,
    heartbeat_at INTEGER NOT NULL
) STRICT;

-- chat_pty_sessions records each chat that is currently hosted in a
-- tmux session running `claude --resume <claude-session-id>`. One row
-- per chat that is in pty_attached or pty_background; the row is
-- deleted when the chat returns to idle. tmux_host is required
-- because tmux is per-host: a row from another host is treated as
-- "not available" from this one, mirroring chat_locks.owner_host.
CREATE TABLE IF NOT EXISTS chat_pty_sessions (
    chat_id         TEXT    NOT NULL PRIMARY KEY,
    tmux_session    TEXT    NOT NULL,
    tmux_host       TEXT    NOT NULL,
    mode            TEXT    NOT NULL CHECK (mode IN ('pty_attached','pty_background')),
    permission_mode TEXT    NOT NULL DEFAULT '',
    workspace_path  TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    last_idle_at    INTEGER
) STRICT;

-- chat_input_queue holds pending turn requests against a chat. Enqueue
-- is unsynchronized (plain INSERT, FIFO by received_at); dispatch is
-- the CAS UPDATE pending→dispatching performed by the holder of the
-- chat lock or by a chat-drain runner. Terminal rows (done, failed,
-- dismissed) stay visible so a human can re-dispatch failed drives
-- from the queue popup.
--
-- on_complete_json / origin_session_id / origin_state are the
-- async-completion plumbing for the state-machine async case (proposal
-- §9.2). They are written when an orchestrator-driven host.chat.drive
-- effect declares on_complete:; the deserialized chain fires after the
-- drive transitions terminal. The firing path itself is intentionally
-- not wired in Phase B — those columns are persisted but unread until
-- a follow-up phase adds the consumer.
CREATE TABLE IF NOT EXISTS chat_input_queue (
    drive_id          TEXT    NOT NULL PRIMARY KEY,
    chat_id           TEXT    NOT NULL,
    transport         TEXT    NOT NULL,
    thread            TEXT    NOT NULL DEFAULT '',
    actor             TEXT    NOT NULL DEFAULT '',
    correlation_id    TEXT    NOT NULL DEFAULT '',
    payload           TEXT    NOT NULL,
    status            TEXT    NOT NULL CHECK (status IN ('pending','dispatching','done','failed','dismissed')),
    received_at       INTEGER NOT NULL,
    dispatched_at     INTEGER,
    completed_at      INTEGER,
    result_seq        INTEGER,
    error_message     TEXT    NOT NULL DEFAULT '',
    on_complete_json  TEXT    NOT NULL DEFAULT '',
    origin_session_id TEXT    NOT NULL DEFAULT '',
    origin_state      TEXT    NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX IF NOT EXISTS chat_input_queue_by_chat
    ON chat_input_queue(chat_id, status, received_at);
-- Index supports the future Phase G consumer that scans for terminal
-- drives carrying an on_complete chain. Empty on_complete_json rows
-- are excluded from the index via SQLite's partial-index feature so
-- the index stays tight on a DB where most drives don't carry chains.
CREATE INDEX IF NOT EXISTS chat_input_queue_pending_oncomplete
    ON chat_input_queue(status, origin_session_id)
    WHERE on_complete_json != '' AND status IN ('done','failed');

-- Schema version. Bump in lockstep with `expectedSchemaVersion` in store.go
-- whenever the DDL above changes incompatibly. The CREATE TABLE IF NOT
-- EXISTS guards prevent silent re-runs from picking up a new column, so a
-- bump here forces the version-check in NewStore to fail loudly until a
-- migration is provided.
PRAGMA user_version = 3;
