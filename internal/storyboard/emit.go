package storyboard

import (
	"fmt"
	"strings"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/tour"
)

// TourSteps derives the capture-ready tour steps from the plan: one TourStep
// per scene, defaults filled the way the tour renderer expects (route "any",
// explain/next, placement "bottom" when anchored / "center" when not, the
// anchor doubling as waitForTarget).
func (s *Storyboard) TourSteps() []tour.TourStep {
	steps := make([]tour.TourStep, 0, len(s.Scenes))
	for _, sc := range s.Scenes {
		step := tour.TourStep{
			ID:            sc.ID,
			Route:         sc.Route,
			Target:        sc.Anchor,
			Title:         sc.Title,
			Body:          sc.Narration,
			Placement:     sc.Placement,
			Kind:          sc.Kind,
			Advance:       sc.Advance,
			WaitForTarget: sc.Anchor,
			DwellMs:       sc.DwellMs,
			Drive:         sc.Drive,
		}
		if step.Route == "" {
			step.Route = "any"
		}
		if step.Placement == "" {
			if step.Target == "" {
				step.Placement = "center"
			} else {
				step.Placement = "bottom"
			}
		}
		if step.Kind == "" {
			step.Kind = "explain"
		}
		if step.Advance == "" {
			step.Advance = "next"
		}
		steps = append(steps, step)
	}
	return steps
}

// tourManifestDoc is the standalone --manifest shape tour.LoadTourManifest
// accepts: {export, steps}.
type tourManifestDoc struct {
	Export string          `yaml:"export"`
	Steps  []tour.TourStep `yaml:"steps"`
}

// EmitTourYAML renders the plan as a standalone tour manifest consumable by
// `kitsoki tour --manifest` (and loadable via tour.LoadTourManifest — the
// round-trip is covered by tests, so an emitted plan is capture-ready by
// construction).
func (s *Storyboard) EmitTourYAML() ([]byte, error) {
	doc := tourManifestDoc{Export: s.ID, Steps: s.TourSteps()}
	out, err := goyaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal tour manifest: %w", err)
	}
	header := fmt.Sprintf("# Generated from %s.storyboard.yaml — edit the storyboard, re-emit.\n", s.ID)
	return append([]byte(header), out...), nil
}

// qaScenariosDoc mirrors the kitsoki-ui-qa scenarios file shape
// (templates/tour-scenarios.yaml; same as feature.schema.json qa.scenarios).
type qaScenariosDoc struct {
	Feature   string       `yaml:"feature"`
	Scenarios []qaScenario `yaml:"scenarios"`
}

type qaScenario struct {
	ID       string   `yaml:"id"`
	Title    string   `yaml:"title"`
	Required bool     `yaml:"required"`
	Steps    []string `yaml:"steps"`
}

// EmitQAScenariosYAML renders each scene's expect claims as a kitsoki-ui-qa
// scenarios file, so the vision gate verifies exactly what the plan promised.
func (s *Storyboard) EmitQAScenariosYAML() ([]byte, error) {
	doc := qaScenariosDoc{Feature: s.Title}
	for _, sc := range s.Scenes {
		doc.Scenarios = append(doc.Scenarios, qaScenario{
			ID:       sc.ID,
			Title:    sc.Title,
			Required: sc.requiredOrDefault(),
			Steps:    sc.Expect,
		})
	}
	out, err := goyaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal qa scenarios: %w", err)
	}
	header := fmt.Sprintf("# Generated from %s.storyboard.yaml — edit the storyboard, re-emit.\n# Each step is an OBSERVABLE claim grounded in a captured frame.\n", s.ID)
	return append([]byte(header), out...), nil
}

// RenderMarkdown renders the plan for human review: the promise and posture up
// top, a scene index table, then one section per scene with its purpose,
// narration, drive, and observable-claim checklist.
func (s *Storyboard) RenderMarkdown() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Storyboard: %s (`%s`)\n\n", s.Title, s.ID)
	fmt.Fprintf(&b, "> %s\n\n", s.Goal)
	if s.Audience != "" {
		fmt.Fprintf(&b, "- **Audience:** %s\n", s.Audience)
	}
	fmt.Fprintf(&b, "- **Surface / format:** %s / %s\n", s.Surface, s.Format)
	if bind := s.Binding; bind != nil {
		var parts []string
		for _, p := range []struct{ k, v string }{
			{"feature", bind.Feature}, {"story", bind.Story}, {"flow", bind.Flow}, {"cassette", bind.HostCassette},
		} {
			if p.v != "" {
				parts = append(parts, fmt.Sprintf("%s `%s`", p.k, p.v))
			}
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "- **Binding (no-LLM posture):** %s\n", strings.Join(parts, ", "))
		}
	}
	if s.Scenario != nil {
		ref := s.Scenario.ID
		if s.Scenario.Transport != "" {
			ref += " (" + s.Scenario.Transport + ")"
		}
		fmt.Fprintf(&b, "- **Product-journey scenario:** %s\n", ref)
	}
	fmt.Fprintf(&b, "- **Estimated screen time:** %.1fs across %d scenes (dwell floor; typing/transitions add more)\n\n",
		float64(s.EstimatedTotalMs())/1000, len(s.Scenes))

	b.WriteString("| # | Scene | Dwell | Anchor | Drive |\n|--:|---|--:|---|---|\n")
	for i, sc := range s.Scenes {
		fmt.Fprintf(&b, "| %d | %s | %.1fs | %s | %s |\n",
			i+1, sc.Title, float64(sc.DwellMs)/1000, codeOrDash(sc.Anchor), driveSummary(sc.Drive))
	}
	b.WriteString("\n")

	for i, sc := range s.Scenes {
		fmt.Fprintf(&b, "## %d. %s (`%s`)\n\n", i+1, sc.Title, sc.ID)
		fmt.Fprintf(&b, "**Why:** %s\n\n", sc.Purpose)
		if sc.Screen != "" {
			fmt.Fprintf(&b, "**On screen:** %s\n\n", sc.Screen)
		}
		fmt.Fprintf(&b, "**Narration:** %s\n\n", sc.Narration)
		if len(sc.Drive) > 0 {
			b.WriteString("**Drive:**\n")
			for _, d := range sc.Drive {
				fmt.Fprintf(&b, "- %s\n", driveLine(d))
			}
			b.WriteString("\n")
		}
		b.WriteString("**Must show:**\n")
		for _, e := range sc.Expect {
			fmt.Fprintf(&b, "- [ ] %s\n", e)
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func codeOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return "`" + s + "`"
}

func driveSummary(actions []tour.DriveAction) string {
	if len(actions) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(actions))
	for _, d := range actions {
		parts = append(parts, d.Type)
	}
	return strings.Join(parts, ", ")
}

func driveLine(d tour.DriveAction) string {
	switch d.Type {
	case tour.DriveTypeAndSend:
		return fmt.Sprintf("type & send: %q", d.Text)
	case tour.DriveClickIntent:
		return fmt.Sprintf("click intent `%s`", d.Intent)
	case tour.DriveWaitState:
		return fmt.Sprintf("wait for state `%s`", d.State)
	case tour.DriveDwellMs:
		return fmt.Sprintf("dwell %.1fs", float64(d.Ms)/1000)
	case tour.DriveRevealTurn:
		return "reveal the turn (ease input to top, scroll the reply)"
	default:
		return d.Type
	}
}
