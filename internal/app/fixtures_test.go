package app

import (
	"os"
	"path/filepath"
	"testing"

	goyaml "github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

// ── Minimal fixture structs — struct-level parse only (Stage 2). ─────────────
// Full semantic validation of fixture→app consistency is a Stage 7 concern.

// flowFixture is the minimal shape of a Mode 2 flow test file.
type flowFixture struct {
	TestKind     string         `yaml:"test_kind"`
	App          string         `yaml:"app"`
	Recording    string         `yaml:"recording,omitempty"`
	InitialState string         `yaml:"initial_state"`
	InitialWorld map[string]any `yaml:"initial_world,omitempty"`
	Turns        []flowTurn     `yaml:"turns"`
}

// flowTurn is one turn in a flow fixture.
type flowTurn struct {
	Intent      *intentRef     `yaml:"intent,omitempty"`
	Input       string         `yaml:"input,omitempty"`
	ExpectState string         `yaml:"expect_state,omitempty"`
	ExpectWorld map[string]any `yaml:"expect_world,omitempty"`
	ExpectError *expectError   `yaml:"expect_error,omitempty"`
}

// intentRef is the structured intent + slots form.
type intentRef struct {
	Name  string         `yaml:"name"`
	Slots map[string]any `yaml:"slots,omitempty"`
}

// expectError is the error assertion block in a flow turn.
type expectError struct {
	Code            string   `yaml:"code"`
	AllowedContains []string `yaml:"allowed_contains,omitempty"`
}

// intentFixture is the minimal shape of a Mode 1 intent test file.
type intentFixture struct {
	TestKind string         `yaml:"test_kind"`
	App      string         `yaml:"app"`
	State    string         `yaml:"state"`
	Defaults intentDefaults `yaml:"defaults,omitempty"`
	Fixtures []intentCase   `yaml:"fixtures"`
}

// intentDefaults holds per-file default configuration.
type intentDefaults struct {
	Runs        int     `yaml:"runs,omitempty"`
	MinPassRate float64 `yaml:"min_pass_rate,omitempty"`
	Temperature float64 `yaml:"temperature,omitempty"`
}

// intentCase is one fixture within a Mode 1 file.
type intentCase struct {
	ID                string         `yaml:"id"`
	Intent            *intentRef     `yaml:"intent,omitempty"`
	Inputs            []string       `yaml:"inputs,omitempty"`
	MinPassRate       float64        `yaml:"min_pass_rate,omitempty"`
	Runs              int            `yaml:"runs,omitempty"`
	ExpectFailure     *expectFailure `yaml:"expect_failure,omitempty"`
	ExpectFallthrough bool           `yaml:"expect_fallthrough,omitempty"`
}

// expectFailure is the failure-assertion block in an adversarial fixture.
type expectFailure struct {
	AnyOf []string `yaml:"any_of"`
}

// recordingFile is the minimal shape of a Mode 2 recording file.
type recordingFile struct {
	Kind        string           `yaml:"kind"`
	AppID       string           `yaml:"app_id"`
	AppVersion  string           `yaml:"app_version"`
	GeneratedAt string           `yaml:"generated_at"`
	Generator   string           `yaml:"generator,omitempty"`
	Entries     []recordingEntry `yaml:"entries"`
}

// recordingEntry is one (state, input) → (intent, slots) mapping.
type recordingEntry struct {
	State      string     `yaml:"state"`
	Input      string     `yaml:"input"`
	Intent     *intentRef `yaml:"intent"`
	Confidence float64    `yaml:"confidence,omitempty"`
	MajorityOf int        `yaml:"majority_of,omitempty"`
}

// ── Tests ────────────────────────────────────────────────────────────────────

const cloakBase = "../../testdata/apps/cloak"

// TestCloakApp_Loads verifies the Cloak app.yaml parses without errors.
func TestCloakApp_Loads(t *testing.T) {
	def, err := Load(filepath.Join(cloakBase, "app.yaml"))
	require.NoError(t, err, "cloak/app.yaml must load cleanly")
	require.NotNil(t, def)
	require.Equal(t, "cloak-of-darkness", def.App.ID)
}

// TestFlowFixtures_Parse verifies every flow fixture can be struct-parsed.
func TestFlowFixtures_Parse(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join(cloakBase, "flows", "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "must have at least one flow fixture")

	for _, path := range matches {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			b, err := os.ReadFile(path)
			require.NoError(t, err)

			var ff flowFixture
			require.NoError(t, goyaml.Unmarshal(b, &ff),
				"flow fixture %s must parse", path)
			require.Equal(t, "flow", ff.TestKind,
				"test_kind must be 'flow' in %s", path)
			require.NotEmpty(t, ff.App, "app field must be set in %s", path)
			require.NotEmpty(t, ff.InitialState, "initial_state must be set in %s", path)
			require.NotEmpty(t, ff.Turns, "turns must be non-empty in %s", path)
		})
	}
}

// TestIntentFixtures_Parse verifies every intent fixture can be struct-parsed.
func TestIntentFixtures_Parse(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join(cloakBase, "intents", "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "must have at least one intent fixture")

	for _, path := range matches {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			b, err := os.ReadFile(path)
			require.NoError(t, err)

			// Intent fixture files may use YAML multi-document (---) for
			// multiple state groups. Parse only the first document for the
			// struct check; the runtime harness handles multi-doc splitting.
			var iff intentFixture
			require.NoError(t, goyaml.Unmarshal(b, &iff),
				"intent fixture %s must parse (first doc)", path)
			require.Equal(t, "intents", iff.TestKind,
				"test_kind must be 'intents' in %s", path)
			require.NotEmpty(t, iff.App, "app field must be set in %s", path)
			require.NotEmpty(t, iff.State, "state field must be set in %s", path)
		})
	}
}

// TestRecording_Parses verifies recording.yaml can be struct-parsed.
func TestRecording_Parses(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(cloakBase, "recording.yaml"))
	require.NoError(t, err)

	var rf recordingFile
	require.NoError(t, goyaml.Unmarshal(b, &rf), "recording.yaml must parse")
	require.Equal(t, "recording", rf.Kind)
	require.Equal(t, "cloak-of-darkness", rf.AppID)
	require.NotEmpty(t, rf.GeneratedAt)
	require.NotEmpty(t, rf.Entries, "recording must have at least one entry")
}
