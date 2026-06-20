// web_shot.go — implements `kitsoki web-shot`, the web twin of `kitsoki shot`.
//
// Where `kitsoki shot` rasterises the TERMINAL Frame to a PNG (internal/tui/shot),
// `kitsoki web-shot` photographs the REAL kitsoki web SPA — typed-view elements,
// chrome, theme — for a state, so the studio (and a human) can review the
// browser rendering of any state as a still. It reuses web.go's no-LLM plumbing:
// it builds the SAME flow-fixture / host-cassette http.Handler `kitsoki tour`
// uses (buildTourServer), then hands it to internal/webshot.Shot, which serves
// it on an ephemeral localhost port and shells the maintained
// tools/runstatus/web-shot.ts to capture the PNG. Fully no-LLM by construction:
// the served handler is flow/cassette-driven, so no agent is ever hit.
//
//	kitsoki web-shot <story> --flow <f> [--state … --world @w.json] -o out.png
//
// The render requires a flow fixture (or host cassette) so the served session is
// deterministic and no-LLM — the same requirement `kitsoki tour` enforces.
// Reaching an arbitrary --state is driven by the flow (proposal Open question 3:
// a "start at state X" web entrypoint is deferred); --state/--world are recorded
// on the Spec and advisory for now. Requires Chromium + the tools/runstatus
// Playwright dependency on the kitsoki checkout (KITSOKI_REPO / --kitsoki-repo).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"kitsoki/internal/kitrepo"
	"kitsoki/internal/webshot"
)

func webShotCmd() *cobra.Command {
	var (
		state        string
		worldArg     string
		flowPath     string
		hostCassette string
		storyDirs    []string
		outPath      string
		viewport     string
		repoFlag     string
	)

	cmd := &cobra.Command{
		Use:   "web-shot <story>",
		Short: "Screenshot the real kitsoki web SPA for a state to a PNG (no LLM)",
		Long: `Photograph the real kitsoki web SPA — the browser rendering of a state —
to a PNG. The web twin of 'kitsoki shot' (which rasterises the terminal Frame).

It boots a deterministic, no-LLM 'kitsoki web' for the story (driven by a flow
fixture / host cassette, exactly like 'kitsoki tour'), then drives headless
Chromium through tools/runstatus/web-shot.ts to capture the PNG.

  kitsoki web-shot stories/bugfix \
    --flow stories/bugfix/flows/happy_path.yaml --state triage -o triage.png

A --flow (or --host-cassette) is REQUIRED: the served session must be no-LLM.
--state/--world are recorded on the shot Spec; reaching an arbitrary state is
driven by the flow (a "start at state X" entrypoint is deferred). Requires
Chromium and the tools/runstatus Playwright dependency on the kitsoki checkout
(KITSOKI_REPO / --kitsoki-repo).`,
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			story := args[0]
			if outPath == "" {
				return infraError("-o/--out is required")
			}
			if flowPath == "" && hostCassette == "" {
				return infraError("a --flow fixture (or --host-cassette) is required so the served session is no-LLM")
			}

			// Resolve the kitsoki checkout holding tools/runstatus/web-shot.ts.
			repoRoot := repoFlag
			if repoRoot == "" {
				repoRoot = os.Getenv(kitrepo.EnvVar)
			}
			if repoRoot == "" {
				repoRoot = kitrepo.Resolve()
			}
			if repoRoot == "" {
				return infraError("could not locate the kitsoki checkout for tools/runstatus/web-shot.ts; pass --kitsoki-repo or set %s", kitrepo.EnvVar)
			}
			helper := filepath.Join(repoRoot, "tools", "runstatus", "web-shot.ts")
			if _, err := os.Stat(helper); err != nil {
				return infraError("web-shot helper not found at %s (is this a kitsoki checkout?)", helper)
			}

			// Parse --world @file.json or inline JSON (advisory; threaded onto Spec).
			world, err := parseWorldArg(worldArg)
			if err != nil {
				return err
			}

			// The served story dir defaults to the story's own directory so a
			// bare `web-shot stories/bugfix` discovers it.
			dirs := storyDirs
			if len(dirs) == 0 {
				dirs = []string{deriveStoryDir(story)}
			}

			// Build the SAME no-LLM handler `kitsoki tour` uses.
			absFlow := flowPath
			if absFlow != "" {
				if a, aerr := filepath.Abs(absFlow); aerr == nil {
					absFlow = a
				}
			}
			absCassette := hostCassette
			if absCassette != "" {
				if a, aerr := filepath.Abs(absCassette); aerr == nil {
					absCassette = a
				}
			}
			handler, closeFn, err := buildTourServer(dirs, absFlow, absCassette, dbPathForTour())
			if err != nil {
				return infraError("build no-LLM web server: %v", err)
			}
			defer closeFn()

			vp := webshot.DefaultViewport
			if viewport != "" {
				if parsed, perr := parseViewport(viewport); perr == nil {
					vp = parsed
				} else {
					return infraError("%v", perr)
				}
			}

			png, err := webshot.Shot(cmd.Context(), webshot.Spec{
				StoryPath: story,
				State:     state,
				World:     world,
				Viewport:  vp,
			}, webshot.Options{
				Server:  &webshot.HandlerServer{Handler: handler},
				Browser: &webshot.NodeInvoker{RepoRoot: repoRoot},
			})
			if err != nil {
				return infraError("%v", err)
			}
			if err := os.WriteFile(outPath, png, 0o644); err != nil {
				return infraError("write %q: %v", outPath, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "kitsoki web-shot: wrote %s (%s)\n", outPath, vp)
			return nil
		},
	}

	cmd.Flags().StringVar(&state, "state", "", "target room/state to shoot (advisory; reached via the flow)")
	cmd.Flags().StringVar(&worldArg, "world", "", "story world vars: inline JSON or @file.json (advisory)")
	cmd.Flags().StringVar(&flowPath, "flow", "", "no-LLM flow fixture stubbing host.* calls (required unless --host-cassette)")
	cmd.Flags().StringVar(&hostCassette, "host-cassette", "", "host cassette backing host.* calls (no LLM; combinable with --flow)")
	cmd.Flags().StringArrayVar(&storyDirs, "stories-dir", nil, "story directory to serve (repeatable; default: the story's own dir)")
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "output PNG path (required)")
	cmd.Flags().StringVar(&viewport, "viewport", webshot.DefaultViewport.String(), "capture viewport WxH (== render viewport)")
	cmd.Flags().StringVar(&repoFlag, "kitsoki-repo", "", "kitsoki checkout holding tools/runstatus/web-shot.ts (overrides $KITSOKI_REPO)")

	return cmd
}

// parseWorldArg parses --world: empty -> nil, "@path" -> the JSON file, else the
// inline JSON object. Advisory world seeding (proposal Open question 3).
func parseWorldArg(arg string) (map[string]any, error) {
	if arg == "" {
		return nil, nil
	}
	var data []byte
	if len(arg) > 0 && arg[0] == '@' {
		b, err := os.ReadFile(arg[1:])
		if err != nil {
			return nil, infraError("read --world %q: %v", arg[1:], err)
		}
		data = b
	} else {
		data = []byte(arg)
	}
	var world map[string]any
	if err := json.Unmarshal(data, &world); err != nil {
		return nil, infraError("parse --world JSON: %v", err)
	}
	return world, nil
}

// parseViewport parses a "WxH" string into a webshot.Viewport.
func parseViewport(s string) (webshot.Viewport, error) {
	var w, h int
	if _, err := fmt.Sscanf(s, "%dx%d", &w, &h); err != nil || w <= 0 || h <= 0 {
		return webshot.Viewport{}, infraError("--viewport must be WxH with positive dims (got %q)", s)
	}
	return webshot.Viewport{Width: w, Height: h}, nil
}

// deriveStoryDir returns the directory to serve for a story argument: the
// directory containing an app.yaml argument, or the argument itself when it is
// already a directory path. A bare story dir (the common case,
// `web-shot stories/bugfix`) is served directly.
func deriveStoryDir(story string) string {
	if info, err := os.Stat(story); err == nil && info.IsDir() {
		return story
	}
	dir := filepath.Dir(story)
	if dir == "" || dir == "." {
		return story
	}
	return dir
}
