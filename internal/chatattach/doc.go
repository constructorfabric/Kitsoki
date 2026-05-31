// Package chatattach owns the lifecycle for handing a kitsoki user's
// terminal to an interactive `claude --resume <id>` session hosted in
// tmux. It sits between the two attach entry points —
// cmd/kitsoki/chat_attach.go (`kitsoki chat attach <chat-id>`) and
// internal/tui/meta_attach.go (the `/attach` slash command) — and the
// lower tiers it drives: [kitsoki/internal/chats] for the chat row,
// lock, and chat_pty_sessions table, and [kitsoki/internal/tmux] for
// the kitsoki-owned tmux server. Both callers funnel through the one
// exported entry point, [Run].
//
// The two callers differ only in how they hand the terminal to tmux,
// and that difference is pushed out to a caller-supplied callback:
//
//   - The CLI surface is for power users and scripting; its callback
//     runs [tmux.Client.AttachStreaming] against the live terminal.
//
//   - The `/attach` slash command is the primary UX, where the user
//     never types a chat ID. Its callback is a bubbletea
//     tea.ExecCommand wrapper that suspends the TUI, hands the terminal
//     to tmux, and resumes the TUI on detach.
//
// runTmux is a caller callback rather than internal logic because
// bubbletea's Exec machinery requires an *exec.Cmd whose
// Stdin/Stdout/Stderr it owns; chatattach cannot construct that command
// from the inside without importing bubbletea. Keeping the tmux-attach
// exec at the call site keeps this package free of bubbletea and
// cmd/kitsoki imports.
//
// # Algorithm
//
// The load-bearing decision is which claude session-id flag the in-pane
// command line uses, and it turns on whether the chat already has a
// claude_session_id:
//
//   - First attach (chat row has no claude_session_id) — chatattach
//     mints a UUID, persists it, and the command line uses
//     `--session-id <uuid>`, which tells claude to CREATE the session.
//   - Subsequent attach (id already present) — the command line uses
//     `--resume <uuid>`, which tells claude to look the session up.
//
// Mixing the two is the bug this package exists to prevent: minting a
// fresh UUID but running `--resume` against it makes claude bail with
// "No conversation found" before the user types anything, which looks
// like a crashed attach pane.
//
// tmux liveness is made idempotent in [ensureTmuxSession]: a fresh chat
// spawns a new session; a re-attach reuses the live one; a row that
// points at a session this host's tmux server can no longer see is
// garbage-collected and respawned.
//
// # Invariants
//
//   - Single writer per chat. The entire lifecycle runs inside
//     [chats.Store.WithLock], so two drivers never attach the same chat
//     concurrently; the second sees [chats.ErrChatBusy].
//   - The minted claude_session_id is persisted BEFORE the tmux session
//     spawns, so a crash between mint and spawn still leaves a resumable
//     id rather than a dangling pane.
//   - The heartbeat goroutine runs on an independent context, not the
//     lock context, so an interrupted interactive attach (e.g. SIGINT
//     to the CLI) does not let the lock go stale before genuine release.
//   - tmux sessions kitsoki owns are always named with
//     [TmuxSessionPrefix]; nothing else writes to that namespace.
//
// # Worked example
//
// Attaching to a brand-new chat with id "bugfix":
//
//	Input:  Options{ChatID:"bugfix", Store, Tmux}, runTmux callback
//	step 1: WithLock("bugfix")            lock acquired
//	step 2: chat has no claude_session_id → mint 4f3c… , persist
//	step 3: session "kitsoki-chat-bugfix" absent → spawn, pane runs
//	          claude --session-id 4f3c… --permission-mode default --no-chrome
//	step 4: AttachPTY                     chat_pty_sessions → pty_attached
//	step 5: heartbeat goroutine starts
//	step 6: runTmux("kitsoki-chat-bugfix") blocks until user detaches
//	step 7: heartbeat stops
//	step 8: DetachPTY                     pty_attached → pty_background
//	Output: tmux session "kitsoki-chat-bugfix" left alive in the
//	        background; runTmux's error (if any) returned verbatim.
//
// A runnable form of this trace — with the tmux side stubbed by the
// fake-tmux emulator and runTmux replaced by a callback that prints the
// session name — lives in [ExampleRun].
//
// # Lifecycle
//
// Both callers share the same eight steps, all executed by [Run]:
//
//  1. Acquire the per-chat singleton lock ([chats.Store.WithLock]).
//  2. Mint claude_session_id if the chat has none.
//  3. Ensure a tmux session named [TmuxSessionPrefix]+chat-id is alive
//     running `claude` against that id — reuse a live pty_background
//     session, spawn fresh otherwise.
//  4. Record pty_attached.
//  5. Start a goroutine that heartbeats the chat lock so cross-host
//     observers can see liveness.
//  6. Call the caller-supplied runTmux callback. It blocks until the
//     user detaches (tmux prefix+d) or claude exits inside the pane.
//  7. Stop the heartbeat.
//  8. Flip pty_attached → pty_background, unless an external
//     `kitsoki chat detach --mode {headless,stop}` already removed the
//     row.
//
// # Non-goals
//
//   - Does not manage claude CLI installation or version. [Options]
//     resolves the binary (defaulting to "claude" on $PATH) but assumes
//     a working claude; a missing binary surfaces as a crashed pane, not
//     a chatattach error, because the failure happens inside tmux after
//     the terminal handoff.
//   - Does not forward tmux across hosts. A chat_pty_sessions row whose
//     session belongs to another host is treated as unreachable and
//     either probed-then-respawned locally or refused — chatattach never
//     tries to bridge a session between tmux servers.
//   - Does not isolate the inner tmux from the user's ~/.tmux.conf
//     beyond passing the kitsoki-shipped kitsoki-tmux.conf via `-f`. The
//     embedded conf sets the status bar and keys; it is best-effort, and
//     a failure to materialize it falls back to default tmux chrome
//     rather than refusing to attach.
//   - Does not render kitsoki's own chrome around the pane (no
//     vt-emulator embed). v1 carries the kitsoki identity in tmux's own
//     status bar; see the `/attach` notes in docs/stories/meta-mode.md.
//
// # Reference
//
// The user-facing `/attach`, `/sessions list`, and `/sessions attach`
// behaviour — including session-id minting and permission modes — is
// documented in docs/stories/meta-mode.md. The package's place in the
// runtime, the chat_pty_sessions schema, and the sibling
// [kitsoki/internal/tmux] wrapper are in docs/architecture/overview.md.
package chatattach
