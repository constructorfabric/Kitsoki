package webconfig

import "testing"

// TestLoad_HarnessLadder_Absent verifies the load-bearing default: no
// harness_ladder: block ⇒ a nil HarnessLadder, so ToHostLadderConfig converts
// it to a disabled host.LadderConfig and every dispatch stays on today's
// single-attempt behavior.
func TestLoad_HarnessLadder_Absent(t *testing.T) {
	p := writeConfig(t, `
story_dirs: [./stories]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HarnessLadder != nil {
		t.Fatalf("expected nil HarnessLadder, got %+v", cfg.HarnessLadder)
	}
	if cfg.HarnessLadder.ToHostLadderConfig().Enabled() {
		t.Fatal("expected a disabled host.LadderConfig when no harness_ladder: is declared")
	}
}

// TestLoad_HarnessLadder_Valid verifies a well-formed ladder loads and
// converts to the runtime shape field-for-field, in declaration order.
func TestLoad_HarnessLadder_Valid(t *testing.T) {
	p := writeConfig(t, `
harness_ladder:
  models:
    - { backend: codex, provider: synthetic-codex, model: "hf:zai-org/GLM-5.2" }
    - { backend: codex, provider: codex-native, model: gpt-5.5 }
    - { backend: claude, provider: claude-native, model: opus }
  efforts: [low, medium, high, xhigh, max]
  max_attempts: 12
  backoff: 90s
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HarnessLadder == nil {
		t.Fatal("expected a non-nil HarnessLadder")
	}
	if len(cfg.HarnessLadder.Models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(cfg.HarnessLadder.Models))
	}
	hc := cfg.HarnessLadder.ToHostLadderConfig()
	if !hc.Enabled() {
		t.Fatal("expected an enabled host.LadderConfig")
	}
	if len(hc.Models) != 3 || hc.Models[0].Model != "hf:zai-org/GLM-5.2" || hc.Models[2].Model != "opus" {
		t.Fatalf("model conversion mismatch: %+v", hc.Models)
	}
	if len(hc.Efforts) != 5 || hc.Efforts[0] != "low" || hc.Efforts[4] != "max" {
		t.Fatalf("effort conversion mismatch: %+v", hc.Efforts)
	}
	if hc.MaxAttempts != 12 {
		t.Fatalf("max_attempts = %d, want 12", hc.MaxAttempts)
	}
	if hc.Backoff != "90s" {
		t.Fatalf("backoff = %q, want 90s", hc.Backoff)
	}
}

// TestLoad_HarnessLadder_Errors verifies fail-fast validation for the
// structural mistakes an operator is most likely to make.
func TestLoad_HarnessLadder_Errors(t *testing.T) {
	cases := map[string]string{
		"declared with no models is rejected": `
harness_ladder:
  models: []
`,
		"missing model name is rejected": `
harness_ladder:
  models:
    - { backend: claude }
`,
		"invalid backend is rejected": `
harness_ladder:
  models:
    - { backend: gpt, model: x }
`,
		"invalid effort is rejected": `
harness_ladder:
  models:
    - { backend: claude, model: opus }
  efforts: [low, turbo]
`,
		"negative max_attempts is rejected": `
harness_ladder:
  models:
    - { backend: claude, model: opus }
  max_attempts: -1
`,
		"unparseable backoff is rejected": `
harness_ladder:
  models:
    - { backend: claude, model: opus }
  backoff: "not-a-duration"
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeConfig(t, body)
			if _, err := Load(p); err == nil {
				t.Fatalf("expected Load to reject: %s", body)
			}
		})
	}
}

// TestMergeConfig_HarnessLadder_LocalReplacesWhole verifies the local file
// replaces the base harness_ladder: block WHOLE (mirroring Intercept), not
// field-merged.
func TestMergeConfig_HarnessLadder_LocalReplacesWhole(t *testing.T) {
	base := HarnessLadder{
		Models:  []HarnessLadderModel{{Backend: "claude", Model: "opus"}},
		Efforts: []string{"low", "high"},
	}
	local := HarnessLadder{
		Models: []HarnessLadderModel{{Backend: "codex", Model: "gpt-5.5"}},
	}
	out := mergeConfig(WebConfig{HarnessLadder: &base}, WebConfig{HarnessLadder: &local})
	if out.HarnessLadder == nil || len(out.HarnessLadder.Models) != 1 || out.HarnessLadder.Models[0].Model != "gpt-5.5" {
		t.Fatalf("expected local's ladder to replace base's whole, got %+v", out.HarnessLadder)
	}
	if len(out.HarnessLadder.Efforts) != 0 {
		t.Fatalf("expected NO field-merge of base.Efforts into the local block, got %+v", out.HarnessLadder.Efforts)
	}
}

// TestMergeConfig_HarnessLadder_AbsentLocalKeepsBase verifies a local file
// that declares no harness_ladder: block at all leaves the base one intact.
func TestMergeConfig_HarnessLadder_AbsentLocalKeepsBase(t *testing.T) {
	base := HarnessLadder{Models: []HarnessLadderModel{{Backend: "claude", Model: "opus"}}}
	out := mergeConfig(WebConfig{HarnessLadder: &base}, WebConfig{})
	if out.HarnessLadder == nil || len(out.HarnessLadder.Models) != 1 {
		t.Fatalf("expected base ladder preserved, got %+v", out.HarnessLadder)
	}
}
