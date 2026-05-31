// Runnable godoc examples for the judges surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/judges/...`.
package judges_test

import (
	"fmt"

	"kitsoki/internal/judges"
)

// ExampleParse is the canonical worked example: a confident "accept"
// verdict from a judge call decodes into a typed Verdict, which then
// auto-fires at the default 0.80 threshold (the same trace shown in the
// package doc).
func ExampleParse() {
	raw := []byte(`{
		"verdict": "pass",
		"intent": "accept",
		"reason": "All checks passed.",
		"confidence": 0.92
	}`)

	v, err := judges.Parse(raw)
	if err != nil {
		panic(err)
	}

	fmt.Println("verdict:   ", v.Verdict)
	fmt.Println("intent:    ", v.Intent)
	fmt.Printf("confidence: %.2f\n", v.Confidence)
	fmt.Println("auto-fire: ", v.ShouldAutoFire(0.80))
	fmt.Println("fire intent:", v.AutoFireIntent())
	// Output:
	// verdict:    pass
	// intent:     accept
	// confidence: 0.92
	// auto-fire:  true
	// fire intent: accept
}

// ExampleVerdict_ShouldAutoFire shows the gate's two refusals: an
// uncertain verdict never fires however confident it is, and a confident
// verdict at exactly the threshold does fire (>= semantics).
func ExampleVerdict_ShouldAutoFire() {
	confident := judges.Verdict{Verdict: "pass", Intent: "accept", Confidence: 0.80}
	uncertain := judges.Verdict{Verdict: "uncertain", Intent: "accept", Confidence: 0.99}

	fmt.Println("at threshold:", confident.ShouldAutoFire(0.80))
	fmt.Println("uncertain:   ", uncertain.ShouldAutoFire(0.80))
	// Output:
	// at threshold: true
	// uncertain:    false
}
