// Package tourspec compiles the semantic, YAML-authored TourSpec v1 contract
// into the executable tour-v2 format. It deliberately keeps semantic intent
// out of internal/tour: TourManifestV2 remains the replay/player boundary.
package tourspec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	goyaml "github.com/goccy/go-yaml"
	"kitsoki/internal/tour"
)

type Spec struct {
	Version  int                `yaml:"version" json:"version"`
	ID       string             `yaml:"id" json:"id"`
	Origin   string             `yaml:"origin,omitempty" json:"origin,omitempty"`
	Bindings map[string]Binding `yaml:"bindings" json:"bindings"`
	Claims   []Claim            `yaml:"claims" json:"claims"`
	Beats    []Beat             `yaml:"beats" json:"beats"`
}
type Binding struct {
	Target tour.TargetBundle `yaml:"target" json:"target"`
}
type Claim struct {
	ID         string `yaml:"id" json:"id"`
	MustExpect string `yaml:"mustExpect" json:"mustExpect"`
}
type Beat struct {
	ID        string   `yaml:"id" json:"id"`
	Binding   string   `yaml:"binding,omitempty" json:"binding,omitempty"`
	Kind      string   `yaml:"kind" json:"kind"`
	Title     string   `yaml:"title" json:"title"`
	Narration string   `yaml:"narration" json:"narration"`
	Expect    []string `yaml:"expect" json:"expect"`
	Next      []string `yaml:"next,omitempty" json:"next,omitempty"`
	AdvanceOn string   `yaml:"advanceOn,omitempty" json:"advanceOn,omitempty"`
	Policy    string   `yaml:"policy,omitempty" json:"policy,omitempty"`
}
type Result struct {
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

var idRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func Load(path string) (*Spec, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, fmt.Errorf("read TourSpec: %w", e)
	}
	var s Spec
	if e = goyaml.UnmarshalWithOptions(b, &s, goyaml.Strict()); e != nil {
		return nil, fmt.Errorf("parse TourSpec: %w", e)
	}
	return &s, nil
}
func (s *Spec) Validate() Result {
	r := Result{}
	errf := func(f string, a ...any) { r.Errors = append(r.Errors, fmt.Sprintf(f, a...)) }
	if s.Version != 1 {
		errf("version must be 1 (got %d)", s.Version)
	}
	if !idRE.MatchString(s.ID) {
		errf("id must be kebab-case")
	}
	if len(s.Beats) == 0 {
		errf("at least one beat is required")
	}
	seen := map[string]bool{}
	incoming := map[string]int{}
	expected := map[string]bool{}
	for _, c := range s.Claims {
		if !idRE.MatchString(c.ID) || c.MustExpect == "" {
			errf("claim requires kebab-case id and mustExpect")
		}
		expected[c.MustExpect] = true
	}
	for _, b := range s.Beats {
		if !idRE.MatchString(b.ID) || seen[b.ID] {
			errf("beat %q must have a unique kebab-case id", b.ID)
		}
		seen[b.ID] = true
		if b.Binding != "" {
			if _, ok := s.Bindings[b.Binding]; !ok {
				errf("beat %q references unknown binding %q", b.ID, b.Binding)
			}
		}
		if b.Kind != "highlight" && b.Kind != "gate" && b.Kind != "navigate" && b.Kind != "act" {
			errf("beat %q has unknown kind %q", b.ID, b.Kind)
		}
		if strings.TrimSpace(b.Title) == "" || strings.TrimSpace(b.Narration) == "" {
			errf("beat %q requires title and narration", b.ID)
		}
		if len(b.Expect) == 0 {
			errf("beat %q requires expect", b.ID)
		}
		for _, e := range b.Expect {
			expected[e] = false
		}
		if b.Kind == "gate" && b.AdvanceOn == "" {
			errf("gate beat %q requires advanceOn", b.ID)
		}
		if b.Kind == "act" && b.Policy == "" {
			errf("act beat %q requires explicit policy", b.ID)
		}
		for _, n := range b.Next {
			incoming[n]++
		}
	}
	for _, b := range s.Beats {
		for _, n := range b.Next {
			if !seen[n] {
				errf("beat %q points to unknown beat %q", b.ID, n)
			}
		}
	}
	if len(s.Beats) > 1 {
		roots := 0
		for _, b := range s.Beats {
			if incoming[b.ID] == 0 {
				roots++
			}
		}
		if roots != 1 {
			errf("graph must have exactly one entry beat (got %d)", roots)
		}
	}
	for _, c := range s.Claims {
		found := false
		for _, b := range s.Beats {
			for _, e := range b.Expect {
				if e == c.MustExpect {
					found = true
				}
			}
		}
		if !found {
			errf("claim %q mustExpect %q is not covered", c.ID, c.MustExpect)
		}
	}
	sort.Strings(r.Errors)
	r.OK = len(r.Errors) == 0
	return r
}
func (s *Spec) Compile() (*tour.TourManifestV2, string, error) {
	if r := s.Validate(); !r.OK {
		return nil, "", fmt.Errorf("invalid TourSpec: %s", strings.Join(r.Errors, "; "))
	}
	m := &tour.TourManifestV2{Version: 2, ID: s.ID, Origin: s.Origin}
	for _, b := range s.Beats {
		st := tour.TourStepV2{ID: b.ID, Kind: b.Kind, Popover: &tour.PopoverV2{Title: b.Title, Body: b.Narration}, Policy: b.Policy, Data: map[string]any{"kitsoki": map[string]any{"beatId": b.ID, "expect": b.Expect, "next": b.Next}}}
		if b.Binding != "" {
			x := s.Bindings[b.Binding].Target
			st.Target = &x
		}
		if b.Kind == tour.StepKindGate {
			st.AdvanceOn = &tour.AdvanceOn{Event: b.AdvanceOn}
		}
		if b.Kind == tour.StepKindNavigate {
			st.Route = b.AdvanceOn
		}
		m.Steps = append(m.Steps, st)
	}
	if e := m.Validate(); e != nil {
		return nil, "", e
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%#v", m)))
	return m, hex.EncodeToString(sum[:]), nil
}
