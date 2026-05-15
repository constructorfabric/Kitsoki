// turn.go — implements the `kitsoki turn` subcommand: a stateless one-shot
// turn execution (proposal §2 of ai-collaboration-proposal.md).
//
// Given an app, a state, an optional world override, and either an --intent
// or an --input, run exactly one turn and print the diff as JSON. Nothing
// is persisted: the SQLite store is never touched.
//
// Useful for: probing "what happens if I do X in state Y with world Z?",
// CI compliance sweeps over (state × intent) pairs, and giving an AI
// collaborator a deterministic exploration tool that doesn't require a
// running TUI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/spf13/cobra"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func turnCmd() *cobra.Command {
	var (
		state         string
		worldFlag     string
		slotsFlag     string
		intentName    string
		inputText     string
		harnessType   string
		recordingPath string
	)

	cmd := &cobra.Command{
		Use:   "turn <app.yaml>",
		Short: "Run one stateless turn and print the diff as JSON",
		Long: `Run exactly one turn against an app definition without persisting
anything. Outputs a JSON document describing the state transition, world
diff, effects applied, host calls fired, and the rendered view of the
new state.

Either --intent (direct path, no LLM) or --input (LLM-routed) must be
set, not both.

Worlds and slots can be inlined as JSON or read from a file with @file:
  --world '{"score": 1}'
  --world @world.json
  --slots @slots.json

Examples:
  kitsoki turn app.yaml --state foyer --intent go_west
  kitsoki turn app.yaml --state foyer --intent open --slots '{"door":"red"}'
  kitsoki turn app.yaml --state cloakroom --world @w.json --input "hang the cloak"
  kitsoki turn app.yaml --state foyer --intent take --harness replay --recording recording.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]

			if (intentName == "") == (inputText == "") {
				return fmt.Errorf("exactly one of --intent or --input must be set")
			}
			if state == "" {
				return fmt.Errorf("--state is required")
			}

			worldVars, err := decodeJSONFlag(worldFlag, "world")
			if err != nil {
				return err
			}
			slotVals, err := decodeJSONFlag(slotsFlag, "slots")
			if err != nil {
				return err
			}

			// loadAppWithEnv publishes KITSOKI_APP_DIR before Load so
			// the loader's env-var validator can resolve references in
			// env-expanded fields (cwd, etc.) during validation.
			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}

			// Default world: schema defaults, then user overrides.
			defaultWorld := machine.WorldFromSchema(def.World)
			merged := make(map[string]any, len(defaultWorld.Vars)+len(worldVars))
			for k, v := range defaultWorld.Vars {
				merged[k] = v
			}
			for k, v := range worldVars {
				merged[k] = v
			}

			m, err := machine.New(def)
			if err != nil {
				return fmt.Errorf("build machine: %w", err)
			}

			// In-memory store: required by orchestrator.New, never written.
			s, err := store.OpenMemory()
			if err != nil {
				return fmt.Errorf("open in-memory store: %w", err)
			}
			defer func() { _ = s.Close() }()

			h, err := buildTurnHarness(harnessType, recordingPath, def, intentName != "")
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()

			hostReg := host.NewRegistry()
			host.RegisterBuiltins(hostReg)
			if err := hostReg.ValidateAllowList(def.Hosts); err != nil {
				return fmt.Errorf("validate hosts: %w", err)
			}

			// Wire the chats store on the same in-memory DB so app YAMLs
			// that invoke host.chat.* can be exercised via `kitsoki turn`.
			// The DB is discarded on return — every turn starts empty.
			chatStore, err := chats.NewStore(s.DB())
			if err != nil {
				return fmt.Errorf("init chats store: %w", err)
			}
			chatAdapter := chathost.NewAdapter(chatStore)

			orch := orchestrator.New(def, m, s, h,
				orchestrator.WithHostRegistry(hostReg),
				orchestrator.WithChatStore(chatAdapter),
			)

			result, err := orch.OneShot(cmd.Context(), orchestrator.OneShotInput{
				State:  app.StatePath(state),
				World:  merged,
				Intent: intentName,
				Slots:  slotVals,
				Input:  inputText,
			})
			if err != nil {
				return err
			}

			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(turnOutputView(result))
		},
	}

	cmd.Flags().StringVar(&state, "state", "", "starting state path (required)")
	cmd.Flags().StringVar(&worldFlag, "world", "", `world overrides as JSON or @file (defaults: app schema)`)
	cmd.Flags().StringVar(&slotsFlag, "slots", "", `intent slots as JSON or @file (only with --intent)`)
	cmd.Flags().StringVar(&intentName, "intent", "", "intent name to invoke directly (no LLM)")
	cmd.Flags().StringVar(&inputText, "input", "", "free-text input routed through the harness")
	cmd.Flags().StringVar(&harnessType, "harness", "", "harness type for --input: claude|live|replay (default auto)")
	cmd.Flags().StringVar(&recordingPath, "recording", "", "recording YAML for --harness replay")

	return cmd
}

// turnOutput is the JSON shape printed by `kitsoki turn`. It wraps OneShotResult
// so we can fold the OutcomeMode (an int) into a stable string field.
type turnOutput struct {
	Mode string `json:"mode"`
	*orchestrator.OneShotResult
}

// turnOutputView constructs the JSON-friendly shape from a OneShotResult.
// `Mode` shadows OneShotResult.Mode (which is an int) by being declared
// first in turnOutput; encoding/json picks up the outer field.
func turnOutputView(r *orchestrator.OneShotResult) turnOutput {
	return turnOutput{Mode: r.Mode.String(), OneShotResult: r}
}

// decodeJSONFlag parses a flag whose value is either inline JSON (e.g.
// `{"a":1}`) or `@path` to a JSON file. Empty input returns nil.
func decodeJSONFlag(raw, fieldName string) (map[string]any, error) {
	if raw == "" {
		return nil, nil
	}
	data := []byte(raw)
	if len(raw) > 0 && raw[0] == '@' {
		path := raw[1:]
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read --%s file %q: %w", fieldName, path, err)
		}
		data = b
	}
	out := make(map[string]any)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse --%s JSON: %w", fieldName, err)
	}
	return out, nil
}

// buildTurnHarness chooses a harness for `kitsoki turn`. When --intent is set
// the harness is never invoked, so we hand back a no-op replay-ish harness
// that errors loudly if anything tries to call it.
func buildTurnHarness(harnessType, recordingPath string, def *app.AppDef, directIntent bool) (harness.Harness, error) {
	if directIntent {
		// Cheap stub: a replay harness with no recording path will error if
		// invoked, which is exactly what we want — the direct path must
		// not call it.
		return &noRunHarness{}, nil
	}
	if harnessType == "" {
		harnessType = autoSelectHarness()
	}
	switch harnessType {
	case "replay":
		if recordingPath == "" {
			return nil, fmt.Errorf("--recording is required when --harness replay is set")
		}
		return harness.NewReplay(recordingPath)
	case "claude":
		return harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{})
	case "live":
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY required for --harness live")
		}
		client := anthropic.NewClient()
		return harness.NewLive(&client, "", def)
	default:
		return nil, fmt.Errorf("unknown --harness %q", harnessType)
	}
}

// noRunHarness is a placeholder harness used when --intent is set: it never
// gets called, but the orchestrator constructor requires a non-nil harness.
type noRunHarness struct{}

func (n *noRunHarness) RunTurn(context.Context, harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, fmt.Errorf("noRunHarness: RunTurn called unexpectedly (--intent path should not invoke the harness)")
}
func (n *noRunHarness) Close() error { return nil }
