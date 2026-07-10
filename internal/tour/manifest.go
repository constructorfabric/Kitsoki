package tour

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
)

// DriveAction is one self-driving action a tour step performs against the live
// UI. It mirrors the TS DriveAction discriminated union
// (tools/runstatus/src/tour/types.ts) — a single struct with a Type
// discriminator and the union of optional fields, because YAML has no native
// sum type. Keep the two in lockstep.
//
// Valid Type values and the field each requires:
//
//	type-and-send → Text    fill the composer with Text, click send
//	click-intent  → Intent  click intent-btn-<Intent>
//	wait-state    → State   poll current-state until it equals State
//	reveal-turn   → —       ease the last turn to the top, hold, ease the reply
//	dwell-ms      → Ms      hold on the current frame for Ms (pace-scaled)
type DriveAction struct {
	Type   string `yaml:"type" json:"type"`
	Text   string `yaml:"text,omitempty" json:"text,omitempty"`
	Intent string `yaml:"intent,omitempty" json:"intent,omitempty"`
	State  string `yaml:"state,omitempty" json:"state,omitempty"`
	Ms     int    `yaml:"ms,omitempty" json:"ms,omitempty"`
}

// Drive action type constants — the closed set the executor dispatches on.
const (
	DriveTypeAndSend = "type-and-send"
	DriveClickIntent = "click-intent"
	DriveWaitState   = "wait-state"
	DriveRevealTurn  = "reveal-turn"
	DriveDwellMs     = "dwell-ms"
)

// Validate reports whether the action carries the field its Type requires.
// Exported so plan-level formats reusing the drive vocabulary (e.g.
// internal/storyboard) validate against the same rules the renderer enforces.
func (d DriveAction) Validate() error {
	switch d.Type {
	case DriveTypeAndSend:
		if d.Text == "" {
			return fmt.Errorf("drive %q requires text", d.Type)
		}
	case DriveClickIntent:
		if d.Intent == "" {
			return fmt.Errorf("drive %q requires intent", d.Type)
		}
	case DriveWaitState:
		if d.State == "" {
			return fmt.Errorf("drive %q requires state", d.Type)
		}
	case DriveDwellMs:
		if d.Ms <= 0 {
			return fmt.Errorf("drive %q requires a positive ms", d.Type)
		}
	case DriveRevealTurn:
		// no fields
	default:
		return fmt.Errorf("unknown drive type %q", d.Type)
	}
	return nil
}

// TourStep mirrors the feature-catalog tour step (and src/tour/types.ts
// TourStep) field-for-field. The narration fields (Title/Body/Target/Placement)
// drive the overlay popover; the new Drive list carries the render-time
// self-driving actions (the data form of the Playwright spec's imperative
// logic). Pointer fields are optional in the YAML.
type TourStep struct {
	ID            string        `yaml:"id" json:"id"`
	Route         string        `yaml:"route" json:"route"`
	Target        string        `yaml:"target,omitempty" json:"target,omitempty"`
	TargetText    string        `yaml:"targetText,omitempty" json:"targetText,omitempty"`
	Title         string        `yaml:"title" json:"title"`
	Body          string        `yaml:"body" json:"body"`
	Placement     string        `yaml:"placement" json:"placement"`
	Kind          string        `yaml:"kind" json:"kind"`
	Advance       string        `yaml:"advance" json:"advance"`
	AdvanceRoute  string        `yaml:"advanceRoute,omitempty" json:"advanceRoute,omitempty"`
	WaitForTarget string        `yaml:"waitForTarget,omitempty" json:"waitForTarget,omitempty"`
	DwellMs       int           `yaml:"dwellMs,omitempty" json:"dwellMs,omitempty"`
	Drive         []DriveAction `yaml:"drive,omitempty" json:"drive,omitempty"`
}

// TourManifest is the parsed tour for one feature: an export name (informational
// here) and the ordered steps. SpecPath is the source file the steps came from,
// recorded into each chapter's SourceRef so a flagged moment resolves back to
// editable YAML.
type TourManifest struct {
	Export   string     `yaml:"export" json:"export"`
	Steps    []TourStep `yaml:"steps" json:"steps"`
	SpecPath string     `yaml:"-" json:"-"`
	// SpecPointerBase is the RFC 6901 JSON Pointer prefix to the steps array in
	// SpecPath's parsed document: "/tour/steps" for a feature catalog,
	// "/steps" for a standalone --manifest file. [TourManifest.stepPointer]
	// appends the step index to it for a [StepShot]'s addressable spec location.
	SpecPointerBase string `yaml:"-" json:"-"`
}

// stepPointer returns the RFC 6901 JSON Pointer to step i in SpecPath's document
// (e.g. "/tour/steps/5"). It defaults to a top-level "/steps" base when none was
// set by the loader.
func (m *TourManifest) stepPointer(i int) string {
	base := m.SpecPointerBase
	if base == "" {
		base = "/steps"
	}
	return fmt.Sprintf("%s/%d", base, i)
}

// featureFile is the slice of a feature-catalog YAML the tour renderer reads:
// the id (for the default --out dir), the demo binding (flow / host cassette /
// story / video base), and the tour block. The rest of the catalog (promo,
// docs, qa) is ignored — the binary only needs what it takes to render.
type featureFile struct {
	ID   string `yaml:"id"`
	Demo struct {
		ArtifactDir  string `yaml:"artifactDir"`
		VideoBase    string `yaml:"videoBase"`
		Story        string `yaml:"story"`
		Flow         string `yaml:"flow"`
		HostCassette string `yaml:"hostCassette"`
	} `yaml:"demo"`
	Tour struct {
		Export string     `yaml:"export"`
		Steps  []TourStep `yaml:"steps"`
	} `yaml:"tour"`
}

// LoadFeatureManifest loads a feature catalog YAML (features/<id>.yaml) and
// returns its tour as a TourManifest plus the demo binding hints (flow / host
// cassette / story dir / video base), each resolved to an absolute path
// relative to repoRoot when set. featurePath is the absolute YAML path; its
// stem is recorded as SpecPath (repo-relative when under repoRoot).
func LoadFeatureManifest(featurePath, repoRoot string) (*TourManifest, DemoBinding, error) {
	data, err := os.ReadFile(featurePath)
	if err != nil {
		return nil, DemoBinding{}, fmt.Errorf("read feature %q: %w", featurePath, err)
	}
	var f featureFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, DemoBinding{}, fmt.Errorf("parse feature %q: %w", featurePath, err)
	}
	if len(f.Tour.Steps) == 0 {
		return nil, DemoBinding{}, fmt.Errorf("feature %q has no tour steps", featurePath)
	}
	m := &TourManifest{Export: f.Tour.Export, Steps: f.Tour.Steps, SpecPath: relTo(repoRoot, featurePath), SpecPointerBase: "/tour/steps"}
	if err := m.validate(); err != nil {
		return nil, DemoBinding{}, err
	}
	b := DemoBinding{
		VideoBase:   firstNonEmpty(f.Demo.VideoBase, f.ID),
		ArtifactDir: f.Demo.ArtifactDir,
	}
	if f.Demo.Story != "" {
		b.StoryDir = absUnder(repoRoot, f.Demo.Story)
	}
	if f.Demo.Flow != "" {
		b.Flow = absUnder(repoRoot, f.Demo.Flow)
	}
	if f.Demo.HostCassette != "" {
		b.HostCassette = absUnder(repoRoot, f.Demo.HostCassette)
	}
	return m, b, nil
}

// LoadTourManifest loads a standalone tour manifest YAML (the --manifest path),
// which is just the tour block: {export, steps}. No demo binding is implied;
// the caller supplies --flow / --stories-dir explicitly.
func LoadTourManifest(manifestPath string) (*TourManifest, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest %q: %w", manifestPath, err)
	}
	var m TourManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %q: %w", manifestPath, err)
	}
	if len(m.Steps) == 0 {
		return nil, fmt.Errorf("manifest %q has no steps", manifestPath)
	}
	m.SpecPath = manifestPath
	m.SpecPointerBase = "/steps"
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// validate checks each step has an id and each drive action is well-formed.
func (m *TourManifest) validate() error {
	seen := map[string]bool{}
	for i, s := range m.Steps {
		if s.ID == "" {
			return fmt.Errorf("step %d has no id", i)
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate step id %q", s.ID)
		}
		seen[s.ID] = true
		for j, d := range s.Drive {
			if err := d.Validate(); err != nil {
				return fmt.Errorf("step %q drive[%d]: %w", s.ID, j, err)
			}
		}
	}
	return nil
}

// DemoBinding carries the deterministic-render hints a feature catalog supplies:
// the story dir to serve, the flow fixture and host cassette (no-LLM posture),
// and the output video base name / artifact subdir. A standalone --manifest
// render leaves these empty and the CLI flags fill them.
type DemoBinding struct {
	StoryDir     string
	Flow         string
	HostCassette string
	VideoBase    string
	ArtifactDir  string
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// absUnder resolves rel against root when root is set and rel is relative;
// otherwise returns rel's absolute form. Used for demo-binding paths declared
// repo-relative in the feature catalog.
func absUnder(root, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	if root != "" {
		return filepath.Join(root, rel)
	}
	if abs, err := filepath.Abs(rel); err == nil {
		return abs
	}
	return rel
}

// relTo returns path relative to root when possible (for a tidy SpecPath in the
// chapter sidecar), else the path unchanged.
func relTo(root, path string) string {
	if root == "" {
		return path
	}
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}
