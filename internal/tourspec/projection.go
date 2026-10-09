package tourspec

import (
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/host"
	"kitsoki/internal/tour"
)

// Profile is the typed browser target a TourSpec is compiled for. It keeps
// environment-specific data out of the portable TourManifestV2 boundary.
type Profile struct {
	ID     string `yaml:"id" json:"id"`
	Origin string `yaml:"origin" json:"origin"`
}

// Projection is the deterministic, producer-neutral output of a TourSpec.
// The manifest remains the browser/player format; the remaining fields are
// companion inputs for rrweb, Slidey and semantic annotation consumers.
type Projection struct {
	Manifest        *tour.TourManifestV2    `json:"manifest"`
	Digest          string                  `json:"digest"`
	RRWebChapters   []RRWebChapter          `json:"rrweb_chapters"`
	SlideyScenes    []SlideyScene           `json:"slidey_scenes"`
	SemanticSidecar host.SemanticSidecar    `json:"semantic_sidecar"`
	Anchors         []host.AnnotationAnchor `json:"anchors"`
	GateEvidence    GateEvidence            `json:"gate_evidence"`
}

// RRWebChapter is an rrweb-friendly, source-addressable chapter. It is not a
// second browser manifest: it references the canonical v2 step id.
type RRWebChapter struct {
	ID        string   `json:"id"`
	StepID    string   `json:"step_id"`
	Title     string   `json:"title"`
	Narration string   `json:"narration"`
	Expect    []string `json:"expect"`
}

// SlideyScene is the minimal scene/narration input a Slidey producer needs.
type SlideyScene struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Narration string `json:"narration"`
	StepID    string `json:"step_id"`
}

// GateEvidence is persisted beside a deterministic replay result. Heals are
// deliberately explicit so a fallback anchor can never silently become truth.
type GateEvidence struct {
	ProfileID string           `json:"profile_id"`
	Heals     []tour.HealEvent `json:"heals"`
}

// CompileProjection compiles a validated spec for one typed profile. A profile
// origin, when set, must agree with the semantic spec's origin. Heals are
// supplied by a replay resolver and are copied into gate evidence verbatim.
func (s *Spec) CompileProjection(profile Profile, heals []tour.HealEvent) (*Projection, error) {
	if !idRE.MatchString(profile.ID) {
		return nil, fmt.Errorf("profile id must be kebab-case")
	}
	if s.Origin != "" && profile.Origin != "" && s.Origin != profile.Origin {
		return nil, fmt.Errorf("profile %q origin %q does not match TourSpec origin %q", profile.ID, profile.Origin, s.Origin)
	}
	m, digest, err := s.Compile()
	if err != nil {
		return nil, err
	}
	if m.Origin == "" {
		m.Origin = profile.Origin
	}
	p := &Projection{
		Manifest:        m,
		Digest:          digest,
		GateEvidence:    GateEvidence{ProfileID: profile.ID, Heals: append([]tour.HealEvent(nil), heals...)},
		SemanticSidecar: host.SemanticSidecar{Plugin: "kitsoki.tour-spec", SchemaVersion: 1},
	}
	for _, beat := range s.Beats {
		ref := s.ID + "." + beat.ID
		p.RRWebChapters = append(p.RRWebChapters, RRWebChapter{ID: ref, StepID: beat.ID, Title: beat.Title, Narration: beat.Narration, Expect: append([]string(nil), beat.Expect...)})
		p.SlideyScenes = append(p.SlideyScenes, SlideyScene{ID: beat.ID, StepID: beat.ID, Title: beat.Title, Narration: beat.Narration})
		p.SemanticSidecar.Elements = append(p.SemanticSidecar.Elements, host.SemanticElement{Ref: ref, Label: beat.Title, Kind: "tour_step", Description: beat.Narration, Data: map[string]any{"step_id": beat.ID, "expect": beat.Expect}})
		p.Anchors = append(p.Anchors, host.AnnotationAnchor{Kind: host.AnchorSemanticElement, SemanticElement: &host.AnchorSemanticElementTarget{Plugin: p.SemanticSidecar.Plugin, Ref: ref, SemanticKind: "tour_step", Label: beat.Title}})
	}
	sort.Slice(p.GateEvidence.Heals, func(i, j int) bool {
		return strings.Join([]string{p.GateEvidence.Heals[i].StepID, p.GateEvidence.Heals[i].FailedAnchor, p.GateEvidence.Heals[i].MatchedAnchor}, "\x00") < strings.Join([]string{p.GateEvidence.Heals[j].StepID, p.GateEvidence.Heals[j].FailedAnchor, p.GateEvidence.Heals[j].MatchedAnchor}, "\x00")
	})
	return p, nil
}
