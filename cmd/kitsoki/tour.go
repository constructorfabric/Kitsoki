// tour.go — implements `kitsoki tour`, the binary-native demo-video renderer.
//
// Where `kitsoki web` serves the multi-story UI for an operator, `kitsoki tour`
// drives that same UI headlessly to RECORD a deterministic, no-LLM demo MP4 from
// a declarative tour manifest — no Node, pnpm, or Playwright (kitsoki-as-
// dependency epic, slice 2). It reuses web.go's no-LLM plumbing: it builds the
// SAME flow-fixture / host-cassette runtimeBase and SessionRegistry, then hands
// the server's http.Handler to internal/tour.Run, which launches chromedp,
// injects window.__startTourWithSteps, executes each step's drive[] actions, and
// captures the screencast → ffmpeg MP4 + chapter sidecar + per-step PNGs.
//
// Input is EITHER a feature catalog entry (--feature <id>, loaded from
// features/<id>.yaml, which also supplies the flow/host-cassette/story-dir/
// video-base defaults) OR a standalone tour manifest (--manifest <yaml>, with
// the no-LLM posture supplied via the explicit flags).
package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/tour"
	"kitsoki/internal/webconfig"
)

func tourCmd() *cobra.Command {
	var (
		featureID    string
		manifestPath string
		flowPath     string
		hostCassette string
		storyDirs    []string
		outDir       string
		pace         float64
		headless     bool
		fps          int
		viewportW    int
		viewportH    int
	)

	cmd := &cobra.Command{
		Use:   "tour",
		Short: "Render a deterministic no-LLM demo MP4 from a tour manifest (headless Chrome + ffmpeg)",
		Long: `Drive the embedded web UI headlessly to record a demo video from a tour
manifest, with no Node/pnpm/Playwright. The render is fully deterministic and
no-LLM: a flow fixture (+ optional host cassette) stubs every host.* call, the
SAME no-LLM posture 'kitsoki web --flow' uses.

Load the tour EITHER from the feature catalog:

  kitsoki tour --feature dev-story-prd-design

(features/<id>.yaml supplies the tour steps and the demo binding — flow, host
cassette, story dir, video base — so the flags above are optional), OR from a
standalone manifest:

  kitsoki tour --manifest my-tour.yaml --flow stories/x/flows/happy.yaml \
    --stories-dir stories/x --out .artifacts/my-tour

Output lands in --out (default .artifacts/<feature-id>): <videoBase>.mp4,
<videoBase>.mp4.chapters.json (one chapter per step, source_ref kind=tour), and
per-step NN-<id>.png poster frames. Requires ffmpeg and Chrome/Chromium on PATH;
without ffmpeg the PNGs + chapter sidecar are still emitted (the command reports
the missing MP4).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (featureID == "") == (manifestPath == "") {
				return fmt.Errorf("exactly one of --feature or --manifest is required")
			}

			repoRoot := discoverRepoRoot()

			// ── Resolve the manifest + demo binding ──────────────────────────
			var (
				manifest *tour.TourManifest
				binding  tour.DemoBinding
				err      error
			)
			if featureID != "" {
				featurePath := filepath.Join(repoRoot, "features", featureID+".yaml")
				if _, statErr := os.Stat(featurePath); statErr != nil {
					return fmt.Errorf("feature %q not found at %s", featureID, featurePath)
				}
				manifest, binding, err = tour.LoadFeatureManifest(featurePath, repoRoot)
				if err != nil {
					return err
				}
			} else {
				abs, aerr := filepath.Abs(manifestPath)
				if aerr != nil {
					return fmt.Errorf("resolve --manifest: %w", aerr)
				}
				manifest, err = tour.LoadTourManifest(abs)
				if err != nil {
					return err
				}
				binding.VideoBase = "tour"
			}

			// Flags override the feature binding's defaults.
			if flowPath != "" {
				if abs, aerr := filepath.Abs(flowPath); aerr == nil {
					binding.Flow = abs
				}
			}
			if hostCassette != "" {
				if abs, aerr := filepath.Abs(hostCassette); aerr == nil {
					binding.HostCassette = abs
				}
			}
			resolvedStoryDirs := storyDirs
			if len(resolvedStoryDirs) == 0 && binding.StoryDir != "" {
				resolvedStoryDirs = []string{binding.StoryDir}
			}
			if binding.Flow == "" {
				return fmt.Errorf("no flow fixture: pass --flow (or use a --feature with a demo.flow binding) — the render must be no-LLM")
			}

			// ── Resolve output dir ───────────────────────────────────────────
			if outDir == "" {
				name := featureID
				if name == "" {
					name = "tour"
				}
				outDir = filepath.Join(repoRoot, ".artifacts", name)
			}

			// ── Build the no-LLM server handler (reuse web.go plumbing) ──────
			handler, closeFn, err := buildTourServer(resolvedStoryDirs, binding.Flow, binding.HostCassette, dbPathForTour())
			if err != nil {
				return err
			}
			defer closeFn()

			// ── Render ───────────────────────────────────────────────────────
			fmt.Fprintf(cmd.ErrOrStderr(), "kitsoki: rendering tour (%d steps) → %s\n", len(manifest.Steps), outDir)
			res, runErr := tour.Run(cmd.Context(), tour.Config{
				Manifest:  manifest,
				Handler:   handler,
				OutDir:    outDir,
				VideoBase: binding.VideoBase,
				Pace:      pace,
				Headless:  headless,
				ViewportW: viewportW,
				ViewportH: viewportH,
				FPS:       fps,
			})
			if res != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "kitsoki: %d frames, %d chapters, %d screenshots\n",
					res.FrameCount, len(res.Chapters), len(res.PNGPaths))
				if res.VideoPath != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "%s\n", res.VideoPath)
					fmt.Fprintf(cmd.ErrOrStderr(), "kitsoki: chapters → %s\n", res.ChaptersPath)
				}
			}
			if runErr != nil {
				return fmt.Errorf("render tour: %w", runErr)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&featureID, "feature", "", "feature catalog id (features/<id>.yaml) to render")
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "standalone tour manifest YAML to render (alternative to --feature)")
	cmd.Flags().StringVar(&flowPath, "flow", "", "no-LLM flow fixture stubbing host.* calls (overrides the feature's demo.flow)")
	cmd.Flags().StringVar(&hostCassette, "host-cassette", "", "host cassette backing host.* calls (overrides the feature's demo.hostCassette)")
	cmd.Flags().StringArrayVar(&storyDirs, "stories-dir", nil, "story directory to serve (repeatable; overrides the feature's demo.story)")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory for the MP4 + .chapters.json + PNGs (default .artifacts/<feature-id>)")
	cmd.Flags().Float64Var(&pace, "pace", 1, "pacing multiplier: 0 = instant (fast), 1 = watch speed")
	cmd.Flags().BoolVar(&headless, "headless", true, "launch Chrome headless")
	cmd.Flags().IntVar(&fps, "fps", 30, "output MP4 frame rate")
	cmd.Flags().IntVar(&viewportW, "width", 1600, "viewport / video width")
	cmd.Flags().IntVar(&viewportH, "height", 900, "viewport / video height")

	return cmd
}

// buildTourServer constructs the no-LLM server handler the tour renderer drives,
// reusing web.go's runtimeBase + SessionRegistry + server.NewMulti plumbing in
// the deterministic flow/cassette posture. It returns the handler plus a close
// function that releases the registry's live sessions on shutdown.
//
// This is the same construction web.go performs, minus the network listener
// (internal/tour owns the localhost listener) and minus the bug-root / actor
// niceties an operator surface needs — a render has no operator.
func buildTourServer(storyDirs []string, flowPath, hostCassette, dbPath string) (handler http.Handler, closeFn func(), err error) {
	// ── Load the flow fixture (deterministic no-LLM posture), as web.go does ──
	data, rerr := os.ReadFile(flowPath)
	if rerr != nil {
		return nil, nil, fmt.Errorf("read --flow %q: %w", flowPath, rerr)
	}
	var fixture testrunner.FlowFixture
	if uerr := yaml.Unmarshal(data, &fixture); uerr != nil {
		return nil, nil, fmt.Errorf("parse --flow %q: %w", flowPath, uerr)
	}
	if hostCassette != "" {
		fixture.HostCassette = hostCassette
	}

	if mkErr := os.MkdirAll(filepath.Dir(dbPath), 0o755); mkErr != nil {
		return nil, nil, fmt.Errorf("create db directory: %w", mkErr)
	}

	// ── Story discovery (flags > .kitsoki.yaml > ./stories) ──────────────────
	cfg, lerr := webconfig.Load("")
	if lerr != nil {
		return nil, nil, lerr
	}
	dirs := webconfig.Resolve(storyDirs, cfg)

	base := runtimeBase{
		DBPath:       dbPath,
		ExecMode:     orchestrator.ExecStaged,
		Flow:         &fixture,
		FlowFilePath: flowPath,
	}

	registry := NewRegistry(cfg, dirs, base)
	if _, rsErr := registry.Rescan(); rsErr != nil {
		registry.Close()
		return nil, nil, fmt.Errorf("discover stories: %w", rsErr)
	}

	srv := server.NewMulti(registry)
	registry.SetNotifier(srv)
	return srv.Handler(), registry.Close, nil
}

// dbPathForTour returns a temp SQLite session-store path for a render — a tour
// is ephemeral (its sessions die with the process), so it must never touch the
// operator's real .kitsoki/sessions.db.
func dbPathForTour() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("kitsoki-tour-%d.db", os.Getpid()))
}

// discoverRepoRoot locates the kitsoki repo root the feature catalog
// (features/*.yaml) and demo-binding paths resolve against. Order: $KITSOKI_REPO
// (exported by the root command's PersistentPreRunE from --kitsoki-repo / the
// persisted ~/.kitsoki/repo), then a walk up from the cwd for a directory that
// holds a features/ dir, then the cwd. The --feature path's existence is checked
// by the caller, so a wrong root surfaces as a clear "feature not found".
func discoverRepoRoot() string {
	if env := os.Getenv("KITSOKI_REPO"); env != "" {
		return env
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	dir := cwd
	for {
		if st, err := os.Stat(filepath.Join(dir, "features")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return cwd
}
