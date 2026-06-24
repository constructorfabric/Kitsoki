package judges_test

import (
	"errors"
	"testing"

	"kitsoki/internal/judges"
)

func TestParseHappyPathAccept(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"intent": "accept",
		"reason": "All checks passed.",
		"confidence": 0.92
	}`)
	v, err := judges.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if v.Verdict != "pass" {
		t.Errorf("Verdict: got %q want %q", v.Verdict, "pass")
	}
	if v.Intent != "accept" {
		t.Errorf("Intent: got %q want %q", v.Intent, "accept")
	}
	if v.Reason != "All checks passed." {
		t.Errorf("Reason: got %q", v.Reason)
	}
	if v.Confidence != 0.92 {
		t.Errorf("Confidence: got %v want 0.92", v.Confidence)
	}
	if !v.ShouldAutoFire(0.8) {
		t.Errorf("ShouldAutoFire(0.8): got false, want true")
	}
	if v.AutoFireIntent() != "accept" {
		t.Errorf("AutoFireIntent: got %q want %q", v.AutoFireIntent(), "accept")
	}
}

func TestShouldAutoFireBelowThreshold(t *testing.T) {
	v := judges.Verdict{
		Verdict:    "pass",
		Intent:     "accept",
		Reason:     "Looks fine.",
		Confidence: 0.5,
	}
	if v.ShouldAutoFire(0.8) {
		t.Errorf("ShouldAutoFire(0.8) with confidence 0.5: got true, want false")
	}
}

func TestShouldAutoFireUncertainVerdict(t *testing.T) {
	v := judges.Verdict{
		Verdict:    "uncertain",
		Intent:     "accept",
		Reason:     "Cannot tell.",
		Confidence: 0.99,
	}
	if v.ShouldAutoFire(0.8) {
		t.Errorf("ShouldAutoFire with verdict=uncertain at conf 0.99: got true, want false")
	}
}

func TestShouldAutoFireUncertainIntent(t *testing.T) {
	v := judges.Verdict{
		Verdict:    "pass",
		Intent:     "uncertain",
		Reason:     "Cannot decide what to do.",
		Confidence: 0.99,
	}
	if v.ShouldAutoFire(0.8) {
		t.Errorf("ShouldAutoFire with intent=uncertain at conf 0.99: got true, want false")
	}
}

func TestShouldAutoFireAtThreshold(t *testing.T) {
	// The gate uses >= semantics. A verdict whose confidence equals
	// the threshold exactly must auto-fire.
	v := judges.Verdict{
		Verdict:    "pass",
		Intent:     "accept",
		Reason:     "Borderline but ok.",
		Confidence: 0.8,
	}
	if !v.ShouldAutoFire(0.8) {
		t.Errorf("ShouldAutoFire at exactly-threshold: got false, want true (>= semantics)")
	}
}

func TestParseMalformedJSON(t *testing.T) {
	raw := []byte(`{not valid json`)
	_, err := judges.Parse(raw)
	if err == nil {
		t.Fatalf("Parse: expected error on malformed JSON, got nil")
	}
	if !errors.Is(err, judges.ErrMalformedJSON) {
		t.Errorf("Parse error: not ErrMalformedJSON: %v", err)
	}
}

func TestParseMissingRequiredField(t *testing.T) {
	// Missing "confidence" — required by schema.
	raw := []byte(`{
		"verdict": "pass",
		"intent": "accept",
		"reason": "Looks fine."
	}`)
	_, err := judges.Parse(raw)
	if err == nil {
		t.Fatalf("Parse: expected error on missing required field, got nil")
	}
	if !errors.Is(err, judges.ErrSchemaViolation) {
		t.Errorf("Parse error: not ErrSchemaViolation: %v", err)
	}
}

func TestParseInvalidEnumValue(t *testing.T) {
	// "maybe" is not in the verdict enum.
	raw := []byte(`{
		"verdict": "maybe",
		"intent": "accept",
		"reason": "Hedging.",
		"confidence": 0.7
	}`)
	_, err := judges.Parse(raw)
	if err == nil {
		t.Fatalf("Parse: expected error on invalid enum, got nil")
	}
	if !errors.Is(err, judges.ErrSchemaViolation) {
		t.Errorf("Parse error: not ErrSchemaViolation: %v", err)
	}
}

func TestParseConfidenceOutOfRange(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"intent": "accept",
		"reason": "Looks fine.",
		"confidence": 1.5
	}`)
	_, err := judges.Parse(raw)
	if err == nil {
		t.Fatalf("Parse: expected error on confidence > 1.0, got nil")
	}
	if !errors.Is(err, judges.ErrSchemaViolation) {
		t.Errorf("Parse error: not ErrSchemaViolation: %v", err)
	}
}

func TestParseAdditionalPropertyRejected(t *testing.T) {
	// Schema has additionalProperties:false — extra keys must reject.
	raw := []byte(`{
		"verdict": "pass",
		"intent": "accept",
		"reason": "Looks fine.",
		"confidence": 0.9,
		"extra": "nope"
	}`)
	_, err := judges.Parse(raw)
	if err == nil {
		t.Fatalf("Parse: expected error on extra property, got nil")
	}
	if !errors.Is(err, judges.ErrSchemaViolation) {
		t.Errorf("Parse error: not ErrSchemaViolation: %v", err)
	}
}

func TestParseRestartFromIntentRoundTrip(t *testing.T) {
	// The schema permits restart_from as an intent; the wrapper must
	// not strip or coerce it. Future fanned-out logic for stage slots
	// lives in AutoFireIntent (currently a passthrough).
	raw := []byte(`{
		"verdict": "fail",
		"intent": "restart_from",
		"reason": "Reproduction was wrong; rewind to that stage.",
		"confidence": 0.95
	}`)
	v, err := judges.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if v.AutoFireIntent() != "restart_from" {
		t.Errorf("AutoFireIntent: got %q want %q", v.AutoFireIntent(), "restart_from")
	}
	if !v.ShouldAutoFire(0.8) {
		t.Errorf("ShouldAutoFire: a confident, non-uncertain restart_from must auto-fire")
	}
}
