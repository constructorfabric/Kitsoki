# Kitsoki for VS Code

Embeds the kitsoki web UI — **chat front and center**, with the live **trace** and
**state graph** as their own dockable surfaces — inside the editor, themed to match
VS Code. It is the same Vue SPA the browser web UI serves, relayed into a webview
and driving a local `kitsoki web` backend over the same JSON-RPC/SSE protocol.

See [`docs/tui/vscode-extension.md`](https://github.com/bsacrobatix/Kitsoki/blob/main/docs/tui/vscode-extension.md)
for the full architecture.

## Requirements

This extension does **not** ship the `kitsoki` binary — it spawns one per
workspace. You need the binary available, then point the extension at it:

- `kitsoki.binaryPath` — absolute path to a `kitsoki` binary. When empty,
  `kitsoki` on `PATH` is used.

Open a workspace that contains a `stories/` directory (or set
`kitsoki.storiesDir`), then click the **Kitsoki** icon in the activity bar — or
run **Kitsoki: Open Chat** from the Command Palette.

## Settings

| Setting | Purpose |
|---|---|
| `kitsoki.binaryPath` | Path to the `kitsoki` binary (empty ⇒ `kitsoki` on `PATH`). |
| `kitsoki.storiesDir` | `--stories-dir` passed to the spawned backend. |
| `kitsoki.flow` | `--flow` fixture (deterministic no-LLM posture; leave empty for live). |
| `kitsoki.hostCassette` | `--host-cassette` (deterministic no-LLM HTTP replay). |

Leave `kitsoki.flow` and `kitsoki.hostCassette` empty for normal (live) use.

## Packaging from source

```
make vscode-package          # builds the SPA + extension, emits the .vsix
```

The `.vsix` lands in `tools/vscode-kitsoki/`. Install it with
**Extensions: Install from VSIX…** in the Command Palette, or
`code --install-extension <file>.vsix`.
