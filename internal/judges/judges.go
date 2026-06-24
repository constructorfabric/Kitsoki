package judges

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// IntentScore is one runner-up entry in the Alternatives list. It records
// the agent's relative scoring for a non-chosen intent so that the
// gate_decided trace event carries a full ranked picture of the decision.
type IntentScore struct {
	// Intent is the candidate intent name (must match one of the gate
	// candidates, but not validated here — the schema enforces it).
	Intent string `json:"intent"`
	// Score is the agent's relative score for this intent in [0.0, 1.0].
	Score float64 `json:"score"`
	// Reason is the agent's optional explanation for this score.
	Reason string `json:"reason,omitempty"`
}

// Verdict is the typed shape of a judge's structured response. The zero
// Verdict is a valid value but never auto-fires (empty Verdict and Intent
// strings fail neither the "uncertain" check nor — at a zero threshold —
// the confidence check, but in practice a real verdict always comes from
// [Parse]). Matches stories/bugfix/schemas/judge_verdict.json verbatim;
// the on-disk schema is the canonical copy, and the const in this package
// mirrors it so tests do not reach across the filesystem.
type Verdict struct {
	// Verdict is the judge's overall call: "pass", "fail", or
	// "uncertain". An "uncertain" verdict blocks auto-fire (see
	// [Verdict.ShouldAutoFire]) so it can be routed to a human.
	Verdict string `json:"verdict"`
	// Intent is the named intent the runtime should dispatch on auto-fire:
	// "accept", "refine", "restart_from", "quit", or "uncertain". It is the
	// judge's decision rendered as a state-machine intent, not free text.
	Intent string `json:"intent"`
	// Reason is the judge's human-readable justification. The schema
	// requires at least 4 characters so a verdict is never silently empty.
	Reason string `json:"reason"`
	// Confidence is the judge's self-reported confidence in [0.0, 1.0],
	// compared against the caller's threshold by [Verdict.ShouldAutoFire].
	Confidence float64 `json:"confidence"`
	// Alternatives is the optional ranked list of runner-up intents with
	// their relative scores. When the agent provides this, the engine
	// records it in the gate_decided trace event for diagnostics and
	// self-improvement pipelines. Not required; older verdicts without it
	// are fully valid.
	Alternatives []IntentScore `json:"alternatives,omitempty"`
}

// schemaJSON mirrors the judge_verdict schema. Keep this in lockstep
// with stories/bugfix/schemas/judge_verdict.json — that on-disk schema
// is the source of truth; if it ever drifts, update this constant.
//
// The "alternatives" property is intentionally NOT in "required": it is
// optional — older verdicts without it remain fully valid. Each entry
// carries {intent, score, reason?} and records the runner-up scores for
// the gate_decided trace event.
const schemaJSON = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title":   "judge_verdict",
  "type":    "object",
  "required": ["verdict", "intent", "reason", "confidence"],
  "properties": {
    "verdict":    { "type": "string", "enum": ["pass", "fail", "uncertain"] },
    "intent":     { "type": "string", "enum": ["accept", "refine", "restart_from", "quit", "uncertain"] },
    "reason":     { "type": "string", "minLength": 4 },
    "confidence": { "type": "number", "minimum": 0.0, "maximum": 1.0 },
    "alternatives": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["intent", "score"],
        "properties": {
          "intent": { "type": "string" },
          "score":  { "type": "number", "minimum": 0.0, "maximum": 1.0 },
          "reason": { "type": "string" }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}`

// compiledSchema is the parsed, compiled judge_verdict schema. Compiled
// once at package init so Parse() is cheap. A compile-time failure is a
// programmer error in this package (drifted constant) — panic loudly
// rather than carry a "validator unavailable" error path.
var compiledSchema = mustCompileSchema()

func mustCompileSchema() *jsonschema.Schema {
	var probe any
	if err := json.Unmarshal([]byte(schemaJSON), &probe); err != nil {
		panic(fmt.Sprintf("judges: embedded schema is malformed JSON: %v", err))
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("judge_verdict.json", probe); err != nil {
		panic(fmt.Sprintf("judges: register embedded schema: %v", err))
	}
	s, err := c.Compile("judge_verdict.json")
	if err != nil {
		panic(fmt.Sprintf("judges: compile embedded schema: %v", err))
	}
	return s
}

// ErrMalformedJSON is returned (wrapped) when the raw payload is not
// valid JSON. Callers can errors.Is against this to route malformed
// verdicts to a human bail-out under the llm_then_human judge mode (see
// docs/case-studies/bug-fix.md).
var ErrMalformedJSON = errors.New("judges: malformed JSON")

// ErrSchemaViolation is returned when the payload is valid JSON but
// fails judge_verdict schema validation (missing required field, wrong
// enum value, out-of-range confidence, etc.). Callers can errors.Is
// against this to distinguish schema failures from transport failures.
var ErrSchemaViolation = errors.New("judges: schema violation")

// Parse validates raw JSON output from an LLM judge call against the
// canonical judge_verdict schema and returns a typed Verdict. It never
// panics on caller input and never returns a partially-populated Verdict:
// on failure it returns a zero Verdict and an error wrapping
// [ErrMalformedJSON] (not valid JSON) or [ErrSchemaViolation] (valid JSON
// that fails the schema), so callers can errors.Is the two apart and
// route malformed verdicts to a human bail-out under the llm_then_human
// judge mode (see docs/case-studies/bug-fix.md). The decoder uses
// json.Number for confidence so the value is range-checked by the schema
// before any float widening.
func Parse(raw []byte) (Verdict, error) {
	var probe any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&probe); err != nil {
		return Verdict{}, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	if err := compiledSchema.Validate(probe); err != nil {
		return Verdict{}, fmt.Errorf("%w: %v", ErrSchemaViolation, err)
	}
	var v Verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		// Schema passed but Go unmarshal failed — shouldn't happen, but
		// surface as malformed rather than panicking.
		return Verdict{}, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	return v, nil
}

// ShouldAutoFire returns true when the verdict meets the confidence
// threshold and is not uncertain. It encodes the confidence-and-
// uncertainty half of the auto-fire gate in one place so the room YAML,
// the flow harness, and any future tooling agree on the rule:
//
//	world.llm_verdict.confidence >= world.judge_confidence_threshold &&
//	world.llm_verdict.verdict != 'uncertain' &&
//	world.llm_verdict.intent != 'uncertain'
//
// The comparison is >= so a verdict whose confidence equals the threshold
// exactly still fires. The judge_mode check (the other half of the gate)
// is the caller's responsibility — this package does not know about modes
// (see docs/case-studies/bug-fix.md for the full gate and the judge
// polymorphism). Safe to call on the zero Verdict.
func (v Verdict) ShouldAutoFire(threshold float64) bool {
	if v.Confidence < threshold {
		return false
	}
	if v.Verdict == "uncertain" || v.Intent == "uncertain" {
		return false
	}
	return true
}

// AutoFireIntent returns the intent the auto-fire effect should emit.
// Defined as a method (rather than a bare field read) so future fanned-
// out logic (e.g. mapping `restart_from` with a stage slot) stays
// inside this package.
func (v Verdict) AutoFireIntent() string {
	return v.Intent
}
