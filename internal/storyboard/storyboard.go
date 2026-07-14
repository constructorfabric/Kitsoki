package storyboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/tour"
)

// Pacing constants. The scene floor matches the rrweb pacing scan's default
// --min-dwell (a content reveal shorter than this is flagged as rushed), and
// the total floor matches the recorder helpers' MIN_DEMO_SECONDS gate — a
// plan that passes here should not be down-named `<name>.SHORT-<n>s.mp4` at
// save time.
const (
	DefaultMinTotalMs = 25_000
	MinSceneDwellMs   = 1_200
	// ReadMsPerWord is the per-word reading budget used for the dwell lint
	// (~215 wpm — comfortable on-screen reading, not skimming).
	ReadMsPerWord = 280
)

// Storyboard is the parsed *.storyboard.yaml — the whole plan for one demo.
type Storyboard struct {
	// Version pins the format; the only accepted value is 1.
	Version int `yaml:"version"`
	// ID is the demo's stable kebab-case identifier (artifact dirs, filenames).
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	// Goal is the one-sentence promise the finished video must deliver on.
	Goal string `yaml:"goal"`
	// Audience optionally names who the demo is for.
	Audience string `yaml:"audience,omitempty"`
	// Surface is where the demo plays out: web | tui | vscode | site.
	Surface string `yaml:"surface"`
	// Format is the capture mode: rrweb | mp4 | tour (the binary renderer).
	Format string `yaml:"format"`
	// MinTotalMs overrides the default total screen-time floor
	// ([DefaultMinTotalMs]) for deliberately short clips (e.g. a deck act).
	MinTotalMs int `yaml:"minTotalMs,omitempty"`
	// Binding declares the deterministic no-LLM posture the capture runs in.
	Binding *Binding `yaml:"binding,omitempty"`
	// Scenario links the demo to a product-journey catalog scenario when the
	// demo claims coverage of one (the run bundle stays the source of truth).
	Scenario *ScenarioRef `yaml:"scenario,omitempty"`
	Scenes   []Scene      `yaml:"scenes"`
}

// Binding is the deterministic capture posture — the same trio the feature
// catalog's demo block carries. Paths are repo-root-relative by convention.
type Binding struct {
	Story        string `yaml:"story,omitempty"`
	Flow         string `yaml:"flow,omitempty"`
	HostCassette string `yaml:"hostCassette,omitempty"`
	// Feature is a feature-catalog id (features/<id>.yaml) that already owns
	// the demo binding; when set, story/flow/cassette may be omitted.
	Feature string `yaml:"feature,omitempty"`
}

// ScenarioRef points at tools/product-journey/scenarios.json.
type ScenarioRef struct {
	ID        string `yaml:"id"`
	Transport string `yaml:"transport,omitempty"`
}

// Scene is one beat of the demo: why it exists, what is said, what is driven,
// how long it holds, and what a reviewer must be able to observe.
type Scene struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	// Purpose is the narrative reason the scene exists — required, so a plan
	// can be reviewed for intent, not just mechanics.
	Purpose string `yaml:"purpose"`
	// Narration is the popover body / voiceover text shown during the scene.
	Narration string `yaml:"narration"`
	// Screen optionally describes what is on screen when the scene settles.
	Screen string `yaml:"screen,omitempty"`
	// Route is the web SPA route the scene lives on: home | interactive | any.
	Route string `yaml:"route,omitempty"`
	// Anchor is the data-testid the scene spotlights.
	Anchor    string `yaml:"anchor,omitempty"`
	Placement string `yaml:"placement,omitempty"` // top|bottom|left|right|center
	Kind      string `yaml:"kind,omitempty"`      // explain|action
	Advance   string `yaml:"advance,omitempty"`   // next|route-match
	// Drive reuses the tour DriveAction vocabulary verbatim, so the plan and
	// the renderer can never disagree about what an action means.
	Drive []tour.DriveAction `yaml:"drive,omitempty"`
	// DwellMs is how long the settled scene holds on screen — required; the
	// plan commits to pacing up front.
	DwellMs int `yaml:"dwellMs"`
	// Expect lists the observable claims a reviewer must be able to verify
	// from the captured frames — at least one per scene. These become the
	// kitsoki-ui-qa scenario steps.
	Expect []string `yaml:"expect"`
	// Required marks whether the QA gate must pass this scene (default true).
	Required *bool `yaml:"required,omitempty"`
}

// Severity classifies an [Issue].
type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
)

// Issue is one validation finding, attributable to a scene when scene-scoped.
type Issue struct {
	Severity Severity
	Scene    string // empty for storyboard-level issues
	Msg      string
}

func (i Issue) String() string {
	if i.Scene != "" {
		return fmt.Sprintf("[%s] scene %q: %s", i.Severity, i.Scene, i.Msg)
	}
	return fmt.Sprintf("[%s] %s", i.Severity, i.Msg)
}

// HasErrors reports whether any issue is error-severity.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Load reads and strictly parses a storyboard YAML. Unknown keys are a parse
// error (typo safety) — the schema is small on purpose; there is no
// pass-through bag.
func Load(path string) (*Storyboard, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read storyboard %q: %w", path, err)
	}
	var sb Storyboard
	if err := goyaml.UnmarshalWithOptions(data, &sb, goyaml.Strict()); err != nil {
		return nil, fmt.Errorf("parse storyboard %q: %w", path, err)
	}
	return &sb, nil
}

// ValidateOptions controls the reference checks.
type ValidateOptions struct {
	// Root is the directory binding paths, feature ids, and the scenario
	// catalog resolve against — normally the repo root. Empty skips all
	// filesystem existence checks (pure structural lint).
	Root string
}

var (
	kebabRe   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	rawIntent = regexp.MustCompile(`[a-z0-9]__[a-z0-9]`)
)

var (
	validSurfaces   = map[string]bool{"web": true, "tui": true, "vscode": true, "site": true}
	validFormats    = map[string]bool{"rrweb": true, "mp4": true, "tour": true}
	validRoutes     = map[string]bool{"home": true, "interactive": true, "any": true}
	validPlacements = map[string]bool{"top": true, "bottom": true, "left": true, "right": true, "center": true}
	validKinds      = map[string]bool{"explain": true, "action": true}
	validAdvances   = map[string]bool{"next": true, "route-match": true}
)

// Validate runs the full semantic lint and returns every finding (errors and
// warnings) rather than stopping at the first, so an author fixes a plan in
// one pass.
func (s *Storyboard) Validate(opts ValidateOptions) []Issue {
	var issues []Issue
	errf := func(scene, format string, args ...any) {
		issues = append(issues, Issue{SeverityError, scene, fmt.Sprintf(format, args...)})
	}
	warnf := func(scene, format string, args ...any) {
		issues = append(issues, Issue{SeverityWarn, scene, fmt.Sprintf(format, args...)})
	}

	if s.Version != 1 {
		errf("", "version must be 1 (got %d)", s.Version)
	}
	if s.ID == "" {
		errf("", "id is required")
	} else if !kebabRe.MatchString(s.ID) {
		errf("", "id %q must be kebab-case", s.ID)
	}
	if s.Title == "" {
		errf("", "title is required")
	}
	if s.Goal == "" {
		errf("", "goal is required — one sentence the finished video must deliver on")
	}
	if !validSurfaces[s.Surface] {
		errf("", "surface %q must be one of web|tui|vscode|site", s.Surface)
	}
	if !validFormats[s.Format] {
		errf("", "format %q must be one of rrweb|mp4|tour", s.Format)
	}
	if len(s.Scenes) == 0 {
		errf("", "at least one scene is required")
	}

	issues = append(issues, s.validateBinding(opts)...)
	issues = append(issues, s.validateScenarioRef(opts)...)

	seen := map[string]bool{}
	for i, sc := range s.Scenes {
		label := sc.ID
		if label == "" {
			label = fmt.Sprintf("#%d", i)
		}
		switch {
		case sc.ID == "":
			errf(label, "id is required")
		case !kebabRe.MatchString(sc.ID):
			errf(label, "id must be kebab-case")
		case seen[sc.ID]:
			errf(label, "duplicate scene id")
		}
		seen[sc.ID] = true
		if sc.Title == "" {
			errf(label, "title is required")
		}
		if sc.Purpose == "" {
			errf(label, "purpose is required — say why the scene exists")
		}
		if sc.Narration == "" {
			errf(label, "narration is required")
		}
		if len(sc.Expect) == 0 {
			errf(label, "expect is required — at least one observable claim a reviewer can verify from the frames")
		}
		for _, e := range sc.Expect {
			if strings.TrimSpace(e) == "" {
				errf(label, "expect contains an empty claim")
			}
		}
		if sc.DwellMs <= 0 {
			errf(label, "dwellMs is required and must be positive — the plan commits to pacing")
		} else if budget := sc.readingBudgetMs(); sc.DwellMs < budget {
			warnf(label, "dwellMs %d is below the reading budget %d for its narration (%d words at %dms/word, floor %dms)",
				sc.DwellMs, budget, wordCount(sc.Title)+wordCount(sc.Narration), ReadMsPerWord, MinSceneDwellMs)
		}
		if sc.Route != "" && !validRoutes[sc.Route] {
			errf(label, "route %q must be one of home|interactive|any", sc.Route)
		}
		if sc.Route != "" && s.Surface != "web" && s.Surface != "site" {
			warnf(label, "route is a web-SPA concept; surface is %q", s.Surface)
		}
		if sc.Placement != "" && !validPlacements[sc.Placement] {
			errf(label, "placement %q must be one of top|bottom|left|right|center", sc.Placement)
		}
		if sc.Kind != "" && !validKinds[sc.Kind] {
			errf(label, "kind %q must be explain|action", sc.Kind)
		}
		if sc.Advance != "" && !validAdvances[sc.Advance] {
			errf(label, "advance %q must be next|route-match", sc.Advance)
		}
		for j, d := range sc.Drive {
			if err := d.Validate(); err != nil {
				errf(label, "drive[%d]: %v", j, err)
			}
		}
		if sc.Kind == "action" && len(sc.Drive) == 0 && sc.Advance != "route-match" {
			warnf(label, "kind action with no drive actions and advance != route-match — what advances the scene?")
		}
		if sc.Anchor == "" && len(sc.Drive) == 0 && sc.Screen == "" {
			warnf(label, "scene has no anchor, no drive, and no screen description — it shows nothing specific")
		}
		if sc.Anchor != "" && (strings.ContainsAny(sc.Anchor, " ") || sc.Anchor != strings.ToLower(sc.Anchor)) {
			warnf(label, "anchor %q does not look like a data-testid (lowercase, no spaces)", sc.Anchor)
		}
		for _, text := range append([]string{sc.Title, sc.Narration}, sc.Expect...) {
			if rawIntent.MatchString(text) {
				warnf(label, "viewer-facing text contains a raw internal intent name (contains %q): %q — word intents naturally", "__", text)
				break
			}
		}
	}

	if total, floor := s.EstimatedTotalMs(), s.minTotalMs(); len(s.Scenes) > 0 && total < floor {
		warnf("", "estimated screen time %.1fs is below the %.0fs floor — the recorder would down-name the output SHORT; add dwell or scenes (or set minTotalMs for a deliberate clip)",
			float64(total)/1000, float64(floor)/1000)
	}

	return issues
}

func (s *Storyboard) validateBinding(opts ValidateOptions) []Issue {
	var issues []Issue
	if s.Binding == nil {
		return []Issue{{SeverityWarn, "", "no binding declared — the deterministic no-LLM capture posture (story/flow/hostCassette or feature) is unplanned"}}
	}
	b := s.Binding
	if b.Story == "" && b.Flow == "" && b.HostCassette == "" && b.Feature == "" {
		issues = append(issues, Issue{SeverityWarn, "", "binding is empty — declare the story/flow/hostCassette (or a feature id) the capture runs against"})
	}
	if b.Flow == "" && b.Feature == "" {
		issues = append(issues, Issue{SeverityWarn, "", "binding has no flow and no feature — how does the demo drive no-LLM?"})
	}
	if opts.Root == "" {
		return issues
	}
	for name, rel := range map[string]string{"story": b.Story, "flow": b.Flow, "hostCassette": b.HostCassette} {
		if rel == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(opts.Root, rel)); err != nil {
			issues = append(issues, Issue{SeverityError, "", fmt.Sprintf("binding.%s %q not found under %s", name, rel, opts.Root)})
		}
	}
	if b.Feature != "" {
		if _, err := os.Stat(filepath.Join(opts.Root, "features", b.Feature+".yaml")); err != nil {
			issues = append(issues, Issue{SeverityError, "", fmt.Sprintf("binding.feature %q: features/%s.yaml not found under %s", b.Feature, b.Feature, opts.Root)})
		}
	}
	return issues
}

// scenarioCatalog is the slice of tools/product-journey/scenarios.json the
// reference check needs.
type scenarioCatalog struct {
	Scenarios []struct {
		ID         string `json:"id"`
		Transports struct {
			Allowed []string `json:"allowed"`
		} `json:"transports"`
	} `json:"scenarios"`
}

func (s *Storyboard) validateScenarioRef(opts ValidateOptions) []Issue {
	if s.Scenario == nil {
		return nil
	}
	if s.Scenario.ID == "" {
		return []Issue{{SeverityError, "", "scenario.id is required when scenario is set"}}
	}
	if opts.Root == "" {
		return nil
	}
	catalogPath := filepath.Join(opts.Root, "tools", "product-journey", "scenarios.json")
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		return []Issue{{SeverityWarn, "", fmt.Sprintf("scenario %q cannot be verified: %s not readable", s.Scenario.ID, catalogPath)}}
	}
	var cat scenarioCatalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return []Issue{{SeverityWarn, "", fmt.Sprintf("scenario %q cannot be verified: parse %s: %v", s.Scenario.ID, catalogPath, err)}}
	}
	for _, entry := range cat.Scenarios {
		if entry.ID != s.Scenario.ID {
			continue
		}
		if t := s.Scenario.Transport; t != "" && len(entry.Transports.Allowed) > 0 {
			for _, a := range entry.Transports.Allowed {
				if a == t {
					return nil
				}
			}
			return []Issue{{SeverityError, "", fmt.Sprintf("scenario %q does not allow transport %q (allowed: %s)",
				s.Scenario.ID, t, strings.Join(entry.Transports.Allowed, ", "))}}
		}
		return nil
	}
	return []Issue{{SeverityError, "", fmt.Sprintf("scenario %q not found in %s", s.Scenario.ID, catalogPath)}}
}

// EstimatedTotalMs is the plan's floor on screen time: every scene's dwell
// plus its explicit dwell-ms drive actions. Typing, transitions, and
// reveal-turn scrolls add real time on top, so the estimate under-counts —
// which is the safe direction for the MIN_DEMO_SECONDS comparison.
func (s *Storyboard) EstimatedTotalMs() int {
	total := 0
	for _, sc := range s.Scenes {
		total += sc.DwellMs
		for _, d := range sc.Drive {
			if d.Type == tour.DriveDwellMs {
				total += d.Ms
			}
		}
	}
	return total
}

func (s *Storyboard) minTotalMs() int {
	if s.MinTotalMs > 0 {
		return s.MinTotalMs
	}
	return DefaultMinTotalMs
}

func (sc *Scene) readingBudgetMs() int {
	budget := ReadMsPerWord * (wordCount(sc.Title) + wordCount(sc.Narration))
	if budget < MinSceneDwellMs {
		return MinSceneDwellMs
	}
	return budget
}

func wordCount(s string) int {
	return len(strings.Fields(s))
}

// requiredOrDefault returns the scene's QA-required flag (default true).
func (sc *Scene) requiredOrDefault() bool {
	if sc.Required == nil {
		return true
	}
	return *sc.Required
}
