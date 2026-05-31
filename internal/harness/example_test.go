// Runnable godoc examples for the harness surface. Each Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/harness/...`. They are LLM-free: the
// live and CLI harnesses need a model, so the runnable examples exercise the
// schema builder and the deterministic Replay harness that mirror the package
// doc's worked example.
package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
)

// ExampleBuildTransitionSchema shows the first half of the package worked
// example: an allowed-intents list plus per-intent slots become one flat tool
// schema with the intent enum and a unioned slots object.
func ExampleBuildTransitionSchema() {
	def := &app.AppDef{
		App: app.AppMeta{ID: "store", Version: "v0"},
		Intents: map[string]app.Intent{
			"propose_purchase": {
				Slots: map[string]app.Slot{
					"items":      {Type: "string"},
					"total_cost": {Type: "integer"},
				},
			},
			"leave": {},
		},
	}

	raw, err := harness.BuildTransitionSchema(def, []string{"propose_purchase", "leave"})
	if err != nil {
		panic(err)
	}

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		panic(err)
	}
	props := schema["properties"].(map[string]any)
	intentEnum := props["intent"].(map[string]any)["enum"].([]any)
	slotProps := props["slots"].(map[string]any)["properties"].(map[string]any)

	fmt.Println("intent enum:", intentEnum)
	fmt.Println("items type:", slotProps["items"].(map[string]any)["type"])
	fmt.Println("total_cost type:", slotProps["total_cost"].(map[string]any)["type"])
	// Output:
	// intent enum: [propose_purchase leave]
	// items type: string
	// total_cost type: integer
}

// ExampleReplayHarness shows the second half: a recorded (state, input) pair
// is replayed into the same mcp.CallToolParams a live harness would have
// produced — with no LLM call — which is how the cassette tests stay
// deterministic.
func ExampleReplayHarness() {
	recording := `kind: recording
app_id: store
app_version: v0
entries:
  - state: general_store
    input: buy 6 oxen for 240
    intent:
      name: propose_purchase
      slots:
        items: "6 oxen"
        total_cost: 240
    confidence: 0.9
`
	dir, err := os.MkdirTemp("", "harness-example-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "recording.yaml")
	if err := os.WriteFile(path, []byte(recording), 0o600); err != nil {
		panic(err)
	}

	h, err := harness.NewReplay(path)
	if err != nil {
		panic(err)
	}
	defer h.Close()

	params, err := h.RunTurn(context.Background(), harness.TurnInput{
		StatePath:      "general_store",
		AllowedIntents: []string{"propose_purchase", "leave"},
		UserText:       "buy 6 oxen for 240",
	})
	if err != nil {
		panic(err)
	}

	args := params.Arguments.(map[string]any)
	slots := args["slots"].(map[string]any)
	fmt.Println("tool:  ", params.Name)
	fmt.Println("intent:", args["intent"])
	fmt.Println("items: ", slots["items"])
	// Output:
	// tool:   transition
	// intent: propose_purchase
	// items:  6 oxen
}

// ExampleClarifyResponse shows how a caller distinguishes "the model needs
// more from the user" (a soft clarification) from a hard technical failure:
// errors.As reaches the *ClarifyResponse and exposes the model's free-form
// Message for display.
func ExampleClarifyResponse() {
	// A harness returns this when the model answered but never called the
	// expected tool; the orchestrator surfaces Message to the user.
	err := error(&harness.ClarifyResponse{
		Message:    "Which item did you want to buy?",
		Underlying: errors.New("harness/claude-cli: LLM did not call submit"),
	})

	var clarify *harness.ClarifyResponse
	if errors.As(err, &clarify) {
		fmt.Println("soft clarification:", clarify.Message)
	} else {
		fmt.Println("hard error:", err)
	}
	// Output:
	// soft clarification: Which item did you want to buy?
}
