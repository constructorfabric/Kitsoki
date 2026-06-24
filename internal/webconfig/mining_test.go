package webconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCfg writes a .kitsoki.yaml in a temp dir and returns its path.
func writeCfg(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), DefaultConfigFile)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestLoadMining_Disabled: no mining block (or enabled:false) loads clean with a
// zero MiningConfig — the default-off posture every flow fixture takes.
func TestLoadMining_Disabled(t *testing.T) {
	cfg, err := Load(writeCfg(t, "story_dirs: [./stories]\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mining.Enabled {
		t.Fatal("absent mining block must default to disabled")
	}

	cfg, err = Load(writeCfg(t, "mining:\n  enabled: false\n  cadence: not-a-duration\n"))
	if err != nil {
		t.Fatalf("a disabled block must skip validation, got: %v", err)
	}
	if cfg.Mining.Enabled {
		t.Fatal("enabled:false must stay disabled")
	}
}

// TestLoadMining_EnabledValid: a valid enabled block parses and applies defaults.
func TestLoadMining_EnabledValid(t *testing.T) {
	cfg, err := Load(writeCfg(t, `mining:
  enabled: true
  cadence: 45s
  first_pass_sample: 8
  priority_threshold: 2.5
  transcript_dirs:
    - /tmp/extra
  mined_through:
    -some-slug: 1700000000
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m := cfg.Mining
	if !m.Enabled {
		t.Fatal("enabled:true must load enabled")
	}
	d, err := m.CadenceOrDefault()
	if err != nil || d != 45*time.Second {
		t.Fatalf("cadence = %v, %v; want 45s", d, err)
	}
	if got := m.FirstPassSampleOrDefault(); got != 8 {
		t.Fatalf("first_pass_sample = %d; want 8", got)
	}
	if m.PriorityThreshold != 2.5 {
		t.Fatalf("priority_threshold = %v; want 2.5", m.PriorityThreshold)
	}
	if got := m.MinedThrough["-some-slug"]; got != 1700000000 {
		t.Fatalf("mined_through ledger = %d; want 1700000000", got)
	}
}

// TestLoadMining_Defaults: an enabled block leaving cadence/sample empty applies
// the documented defaults.
func TestLoadMining_Defaults(t *testing.T) {
	cfg, err := Load(writeCfg(t, "mining:\n  enabled: true\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d, err := cfg.Mining.CadenceOrDefault()
	if err != nil {
		t.Fatalf("CadenceOrDefault: %v", err)
	}
	if want, _ := time.ParseDuration(DefaultMiningCadence); d != want {
		t.Fatalf("default cadence = %v; want %v", d, want)
	}
	if got := cfg.Mining.FirstPassSampleOrDefault(); got != DefaultFirstPassSample {
		t.Fatalf("default first_pass_sample = %d; want %d", got, DefaultFirstPassSample)
	}
}

// TestLoadMining_FailFast: an enabled block with a bad cadence or negative
// sample is a load-time error (never a runtime surprise).
func TestLoadMining_FailFast(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"bad cadence", "mining:\n  enabled: true\n  cadence: 30furlongs\n"},
		{"negative sample", "mining:\n  enabled: true\n  first_pass_sample: -1\n"},
		{"negative threshold", "mining:\n  enabled: true\n  priority_threshold: -0.5\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeCfg(t, tc.body)); err == nil {
				t.Fatalf("expected a load error for %s", tc.name)
			}
		})
	}
}
