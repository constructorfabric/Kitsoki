package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/webconfig"
)

// testIntentsCmd implements `kitsoki test intents`.
//
// Default harness:
//   - "static" when ANTHROPIC_API_KEY is not set (uses a recording as the
//     canned-response table; no LLM calls).
//   - "live" when ANTHROPIC_API_KEY is set (calls the real LLM; costs money).
//
// Override with --harness <live|static>.
//
// Default intents glob: <app-dir>/intents/*.yaml
func testIntentsCmd() *cobra.Command {
	var (
		intentsGlob         string
		runsOverride        int
		dryRun              bool
		maxCost             float64
		onlyState           string
		emitRecording       string
		baselinePath        string
		updateBaseline      bool
		regressionThreshold float64
		jsonOut             string
		harnessType         string
		claudeModel         string
		agentBackend        string
		profileName         string
		configPath          string
		recordingPath       string // for static harness seeding
	)

	cmd := &cobra.Command{
		Use:   "intents <app.yaml>",
		Short: "Run Mode 1 intent pass-rate tests",
		Long: `Run pass-rate tests that measure how reliably the harness maps user
phrasings to the correct intents in each state.

By default this uses a StaticHarness seeded from the recording file so no
LLM calls are made and the run is deterministic. To use a real routing model,
pass --harness claude or --harness live explicitly.

Default harness:
  static  unless --harness is provided
Override with --harness <static|claude|live>.

Harness modes:
  static  deterministic recording lookup; no LLM calls
  claude  claude/codex/copilot CLI routing harness; use --claude-model and --agent
  live    direct Anthropic SDK routing harness

Use --profile <name> to benchmark an operator-declared harness profile from
.kitsoki.yaml/.kitsoki.local.yaml. The profile supplies backend, model, and
provider environment such as synthetic.new credentials.

Default intents glob: <app-dir>/intents/*.yaml
Default recording (for --harness static): <app-dir>/recording.yaml

Exit codes:
  0  all fixtures at or above their pass-rate threshold, no regressions
  1  at least one fixture below threshold or regression detected
  2  fatal startup error`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]

			// Resolve default glob.
			if intentsGlob == "" {
				intentsGlob = defaultIntentsGlob(appPath)
			}

			// Resolve harness type.
			if harnessType == "" {
				harnessType = "static"
			}

			// Resolve recording path for static harness.
			if harnessType == "static" && recordingPath == "" {
				recordingPath = filepath.Join(filepath.Dir(appPath), "recording.yaml")
			}

			var activeProfile host.ActiveProfile
			if profileName != "" {
				if configPath == "" {
					configPath = webconfig.DefaultConfigFile
				}
				cfg, err := webconfig.Load(configPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "kitsoki test intents: load --config %s: %v\n", configPath, err)
					os.Exit(2)
				}
				profiles, _ := harnessProfilesFromConfig(cfg)
				profile, ok := profiles[profileName]
				if !ok {
					fmt.Fprintf(os.Stderr, "kitsoki test intents: unknown --profile %q\n", profileName)
					os.Exit(2)
				}
				if profile.Backend != "" {
					agentBackend = profile.Backend
				}
				if profile.Model != "" && claudeModel == "" {
					claudeModel = profile.Model
				}
				activeProfile = host.ActiveProfile{
					Name: profileName,
					Provider: host.Provider{
						Model:  profile.Model,
						Effort: profile.Effort,
						Env:    profile.Env,
					},
					Quota: profile.Quota,
				}
			}

			var liveHarnessFactory func(*app.AppDef) (harness.Harness, error)
			var h harness.Harness
			switch harnessType {
			case "static":
				var err error
				h, err = testrunner.NewStaticHarnessFromRecording(recordingPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "kitsoki test intents: %v\n", err)
					os.Exit(2)
				}
			case "claude", "live":
				liveHarnessFactory = func(def *app.AppDef) (harness.Harness, error) {
					return buildHarnessWithActiveProfile(harnessType, claudeModel, agentBackend, recordingPath, "", def, activeProfile)
				}
			default:
				fmt.Fprintf(os.Stderr, "kitsoki test intents: unknown harness %q (use static|claude|live)\n", harnessType)
				os.Exit(2)
			}

			opts := testrunner.IntentOptions{
				Glob:                intentsGlob,
				Runs:                runsOverride,
				DryRun:              dryRun,
				MaxCostUSD:          maxCost,
				OnlyState:           onlyState,
				EmitRecording:       emitRecording,
				BaselinePath:        baselinePath,
				UpdateBaseline:      updateBaseline,
				RegressionThreshold: regressionThreshold,
				JSONOut:             jsonOut,
				HarnessType:         harnessType,
				HarnessModel:        claudeModel,
				AgentBackend:        agentBackend,
				ProfileName:         profileName,
				StaticHarnessImpl:   h,
				LiveHarnessFactory:  liveHarnessFactory,
				// Recording-miss inputs are skipped (not failed) when using the static harness.
				// This is correct behaviour: the recording only covers canonical phrasings, not
				// every colloquial phrasing in the Mode 1 fixtures (which require a live LLM).
				SkipOnRecordingMiss: harnessType == "static",
				ImportResolver:      buildImportResolver(),
			}

			ctx := context.Background()
			report, err := testrunner.RunIntents(ctx, appPath, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "kitsoki test intents: %v\n", err)
				os.Exit(2)
			}

			testrunner.PrintIntentReport(report)

			if report.TotalFailed > 0 || len(report.Regressions) > 0 {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&intentsGlob, "intents", "",
		"glob for intent fixture files (default: <app-dir>/intents/*.yaml)")
	cmd.Flags().IntVar(&runsOverride, "runs", 0,
		"override runs per input globally (0 = use fixture/file defaults)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"print plan (files, fixture count, call count) and exit 0")
	cmd.Flags().Float64Var(&maxCost, "max-cost", 2.0,
		"refuse to run if estimated cost exceeds this (USD; 0 = no limit)")
	cmd.Flags().StringVar(&onlyState, "only", "",
		"filter to only this state (exact match)")
	cmd.Flags().StringVar(&emitRecording, "emit-recording", "",
		"write majority-vote recording to this YAML file after a passing run")
	cmd.Flags().StringVar(&baselinePath, "baseline", "",
		"path to baseline JSON for regression tracking")
	cmd.Flags().BoolVar(&updateBaseline, "update-baseline", false,
		"write a new baseline JSON after running")
	cmd.Flags().Float64Var(&regressionThreshold, "fail-regression-at", 0.05,
		"fail if any fixture's pass rate dropped by more than this from baseline")
	cmd.Flags().StringVar(&jsonOut, "json", "",
		"write JSON report to this file")
	cmd.Flags().StringVar(&harnessType, "harness", "",
		"harness type: static|claude|live (default: static)")
	cmd.Flags().StringVar(&claudeModel, "claude-model", "",
		fmt.Sprintf("model passed to the claude/codex/copilot CLI harness (default: %s)", harness.DefaultClaudeModel))
	cmd.Flags().StringVar(&agentBackend, "agent", "",
		"coding-agent CLI backend for --harness claude: claude|copilot|codex (default: claude, or $KITSOKI_AGENT)")
	cmd.Flags().StringVar(&profileName, "profile", "",
		"harness profile from .kitsoki.yaml/.kitsoki.local.yaml; supplies backend, model, and provider env")
	cmd.Flags().StringVar(&configPath, "config", webconfig.DefaultConfigFile,
		"config file used with --profile")
	cmd.Flags().StringVar(&recordingPath, "recording", "",
		"recording YAML to seed the static harness (default: <app-dir>/recording.yaml)")

	return cmd
}

// defaultIntentsGlob returns the glob to use when --intents is not set.
func defaultIntentsGlob(appPath string) string {
	appDir := filepath.Dir(appPath)
	candidate := filepath.Join(appDir, "intents", "*.yaml")
	dir := filepath.Join(appDir, "intents")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return candidate
	}
	return filepath.Join("intents", "*.yaml")
}
