// Package tmux is the thin wrapper over the tmux(1) CLI that kitsoki
// uses to host interactive `claude --resume` sessions. It sits below
// the chat layer: the host.chat.* handlers (see
// docs/architecture/hosts.md) start, probe, and tear down a detached
// tmux session per hosted claude, and the TUI's `kitsoki chat attach`
// path hands the user's terminal to one of those sessions.
//
// The package keeps every tmux invocation in one place so the rest of
// the codebase never grows ad-hoc exec.Command calls against tmux. Two
// kinds of caller share one [Client]: the GC / watcher paths that probe
// and mutate sessions out of band
// ([Client.HasSession], [Client.ListSessions], [Client.KillSession],
// [Client.SetStatusRight]), and the foreground attach paths that give
// the TTY to tmux ([Client.Attach], [Client.AttachStreaming]).
//
// # Design rules
//
// Three rules shape the surface:
//
//  1. Use a kitsoki-owned socket path (default
//     $XDG_STATE_HOME/kitsoki/tmux.sock, see [DefaultSocketPath]). A
//     private socket survives systemd's RemoveIPC=yes teardown of the
//     shared /tmp/tmux-<uid> directory, and it avoids stomping on the
//     user's own tmux sessions on the default socket.
//
//  2. Inject the binary path through [TmuxBinEnv] so tests substitute a
//     fake-tmux script the same way agent tests substitute a fake
//     claude. This is what keeps the test suite hermetic on machines
//     without a real tmux.
//
//  3. Expose only the verbs the chat-attach / GC flow needs, and have
//     each return a structured outcome rather than a bare error — so a
//     caller can tell "tmux says the session is gone"
//     ([ErrSessionNotFound]) from "tmux itself isn't installed"
//     ([ErrTmuxUnavailable]) without scraping stderr.
//
// # Invariants
//
//   - A [Client] is bound to exactly one socket path for its lifetime;
//     the path is validated non-empty at construction.
//   - tmux serialises commands per socket, so all [Client] methods are
//     safe for concurrent use. The struct holds no mutable state.
//   - "session absent" is never an infrastructure error.
//     [Client.HasSession] reports it as (false, nil); the mutating
//     verbs report it as [ErrSessionNotFound]; [Client.ListSessions]
//     reports a fresh (no-server) socket as an empty slice. A non-nil
//     error other than ErrSessionNotFound always means tmux itself
//     failed (missing binary, broken socket).
//
// # Worked example
//
// The canonical chat-host flow — create a detached session, confirm it,
// list it, kill it — traces as:
//
//	New(socket)                          -> *Client bound to socket
//	NewSession({Name:"kitsoki-chat-7",   -> nil  (tmux new-session -d -s …)
//	            Command:"claude --resume 7",
//	            WorkingDir:"/work/7"})
//	HasSession(ctx, "kitsoki-chat-7")    -> (true, nil)
//	HasSession(ctx, "kitsoki-chat-404")  -> (false, nil)        (absent, not an error)
//	ListSessions(ctx)                    -> (["kitsoki-chat-7"], nil)
//	KillSession(ctx, "kitsoki-chat-7")   -> nil
//	KillSession(ctx, "kitsoki-chat-7")   -> ErrSessionNotFound  (already gone)
//
// A runnable form of this trace lives in [ExampleClient].
//
// # Lifecycle
//
// Callers build one [Client] at startup via [New] with
// [DefaultSocketPath], call [Client.EnsureSocketDir] once to create the
// socket's parent directory, then reuse the Client for the process
// lifetime. Sessions are created on demand by [Client.NewSession] and
// reclaimed either by the user detaching + a GC sweep
// ([Client.ListSessions] cross-checked against the chat table, then
// [Client.KillSession]) or by claude exiting inside the pane. The
// Client itself owns no OS resources to release — there is nothing to
// Close.
//
// # Non-goals
//
//   - No session reattachment with environment restoration. Each
//     `claude --resume` forks a fresh process, so replaying a prior
//     session's env vars would defeat the per-chat workspace isolation
//     that [NewSessionOptions.WorkingDir] and .Env exist to enforce.
//   - No loading of the user's ~/.tmux.conf. kitsoki passes its own
//     keybinding file through [NewSessionOptions.ConfigFile] (-f) so a
//     hosted pane behaves identically regardless of the operator's
//     personal tmux config; the user's config is deliberately ignored.
//   - No keybinding or option customization beyond
//     [NewSessionOptions.ConfigFile] and [Client.SetStatusRight]. The
//     package is a launcher, not a tmux configuration manager; richer
//     in-pane chrome is the job of the attach wrapper, not this layer.
//   - No Windows support for the in-place [Client.Attach] path. Windows
//     has no posix exec replacement; on non-unix platforms callers must
//     use the fork-exec [Client.AttachStreaming] instead.
//
// # Reference
//
// The chat-hosting layer that drives this package — how a chat row maps
// to a hosted claude session and how attach / GC fit together — is
// documented under host.chat.* in docs/architecture/hosts.md.
package tmux
