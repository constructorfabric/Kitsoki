// Package featuresadapter is W3.0's pilot adapter: it walks a loaded
// project object graph catalog's public site-page nodes and emits the
// existing features/<id>.yaml shape (features/feature.schema.json) — the
// site codegen (tools/runstatus/scripts/features/generate.ts) keeps reading
// features/*.yaml unchanged; this package is the graph-sourced producer for
// the pilot feature (operator-ask), not a replacement for that codegen.
//
// Per the W3.0 design session: a site-page node's presents edge joins the
// feature it's presenting copy for, and its has_media edge joins the
// evidence node holding the demo recording binding — demo: is DERIVED from
// evidence, not duplicated onto the site-page node.
package featuresadapter

import (
	"fmt"
	"strings"

	"kitsoki/internal/graph"
	"kitsoki/internal/tour"
)

// FeatureDoc mirrors features/feature.schema.json's fields this adapter
// populates. Field order matches the schema/existing files for readability;
// yaml.Marshal output is not asserted byte-identical to the committed files
// (formatting is not semantically significant) — see BuildFeatureDoc's doc
// comment and the adapter tests for how equivalence is actually checked.
type FeatureDoc struct {
	ID        string   `yaml:"id"`
	Kind      string   `yaml:"kind"`
	Title     string   `yaml:"title"`
	Tagline   string   `yaml:"tagline"`
	Summary   string   `yaml:"summary"`
	Narrative string   `yaml:"narrative,omitempty"`
	Promo     *Promo   `yaml:"promo,omitempty"`
	Docs      []string `yaml:"docs,omitempty"`
	Related   []string `yaml:"related,omitempty"`
	Demo      *Demo    `yaml:"demo,omitempty"`
	// Sections and QA are schema shapes this adapter does not need to
	// interpret (stitched product-tour chapters; gated vision-QA scenarios)
	// — carried verbatim so nothing is lost on a graph round-trip.
	Sections []any `yaml:"sections,omitempty"`
	QA       any   `yaml:"qa,omitempty"`
	Tour     *Tour `yaml:"tour,omitempty"`
}

type Promo struct {
	Order     int  `yaml:"order"`
	Highlight bool `yaml:"highlight,omitempty"`
}

// Demo is derived entirely from the site-page's has_media evidence node —
// never stored on the site-page itself, so it can't drift from its source.
type Demo struct {
	Renderer     string     `yaml:"renderer,omitempty"`
	Format       string     `yaml:"format,omitempty"`
	Spec         string     `yaml:"spec,omitempty"`
	RrwebSpec    string     `yaml:"rrwebSpec,omitempty"`
	ArtifactDir  string     `yaml:"artifactDir,omitempty"`
	VideoBase    string     `yaml:"videoBase,omitempty"`
	PosterStep   string     `yaml:"posterStep,omitempty"`
	Story        string     `yaml:"story,omitempty"`
	Flow         string     `yaml:"flow,omitempty"`
	HostCassette string     `yaml:"hostCassette,omitempty"`
	External     bool       `yaml:"external,omitempty"`
	Profiles     []string   `yaml:"profiles,omitempty"`
	Embed        *DemoEmbed `yaml:"embed,omitempty"`
}

type DemoEmbed struct {
	Deck  string `yaml:"deck"`
	Rrweb string `yaml:"rrweb"`
}

type Tour struct {
	Export string     `yaml:"export"`
	Steps  []TourStep `yaml:"steps"`
}

type TourStep struct {
	ID            string             `yaml:"id"`
	Route         string             `yaml:"route,omitempty"`
	Target        string             `yaml:"target,omitempty"`
	Title         string             `yaml:"title,omitempty"`
	Body          string             `yaml:"body,omitempty"`
	Placement     string             `yaml:"placement,omitempty"`
	Kind          string             `yaml:"kind,omitempty"`
	Advance       string             `yaml:"advance,omitempty"`
	AdvanceRoute  string             `yaml:"advanceRoute,omitempty"`
	WaitForTarget string             `yaml:"waitForTarget,omitempty"`
	DwellMs       int                `yaml:"dwellMs,omitempty"`
	Drive         []tour.DriveAction `yaml:"drive,omitempty"`
}

// PublicSitePages returns every visibility:public site-page node in cat, in
// deterministic id order.
func PublicSitePages(cat *graph.Catalog) []*graph.Node {
	var pages []*graph.Node
	for _, id := range cat.SortedNodeIDs() {
		node := cat.Nodes[id]
		if node.TypeID == "site-page" && node.Visibility == graph.VisibilityPublic {
			pages = append(pages, node)
		}
	}
	return pages
}

// BuildFeatureDoc joins sitePage with the feature its `presents` edge points
// at and the evidence its `has_media` edge points at, producing the
// features/<id>.yaml shape. It returns an error rather than emitting a
// partial doc if either join target is missing — a site-page that can't
// resolve its own presents/has_media edges is a data bug, not something to
// silently paper over.
func BuildFeatureDoc(cat *graph.Catalog, sitePage *graph.Node) (*FeatureDoc, error) {
	feature, err := joinOne(cat, sitePage, "presents")
	if err != nil {
		return nil, err
	}

	content, _ := sitePage.Fields["content_fields"].(map[string]any)
	doc := &FeatureDoc{
		ID:        featureSlug(feature.ID),
		Kind:      stringField(sitePage.Fields, "kind", "feature"),
		Title:     stringField(content, "title", sitePage.Title),
		Tagline:   stringField(content, "tagline", sitePage.Title),
		Summary:   stringField(content, "summary", ""),
		Narrative: stringField(content, "narrative", ""),
	}

	if promo, ok := sitePage.Fields["promo"].(map[string]any); ok {
		p := &Promo{}
		if order, ok := promo["order"].(int); ok {
			p.Order = order
		}
		if hl, ok := promo["highlight"].(bool); ok {
			p.Highlight = hl
		}
		doc.Promo = p
	}

	doc.Docs = stringSliceField(sitePage.Fields, "docs")
	doc.Related = stringSliceField(sitePage.Fields, "related")

	if evidence, err := joinOne(cat, sitePage, "has_media"); err == nil {
		doc.Demo = demoFromEvidence(evidence)
	}

	if rawTour, ok := sitePage.Fields["tour"].(map[string]any); ok {
		doc.Tour = tourFromFields(rawTour)
	}

	if rawSections, ok := sitePage.Fields["sections"].([]any); ok {
		doc.Sections = rawSections
	}
	if rawQA, ok := sitePage.Fields["qa"]; ok {
		doc.QA = rawQA
	}

	return doc, nil
}

func stringSliceField(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// featureSlug derives the features/<id>.yaml site-facing id from a feature
// node's graph id, by convention "feature-<slug>" (every feature node in the
// seed catalog follows this: feature-operator-ask, feature-agent-actions,
// ...). Falls back to the graph id unchanged if the convention doesn't hold,
// rather than erroring — a graph id IS a valid (if unexpected) slug.
func featureSlug(featureNodeID graph.NodeID) string {
	return strings.TrimPrefix(string(featureNodeID), "feature-")
}

// joinOne resolves the single node a cardinality-one (or first-of-many)
// edge on node points at, erroring if the edge is empty or the target is
// missing from the catalog.
func joinOne(cat *graph.Catalog, node *graph.Node, edgeField graph.EdgeField) (*graph.Node, error) {
	targets := node.Edges[edgeField]
	if len(targets) == 0 {
		return nil, fmt.Errorf("featuresadapter: node %q has no %q edge", node.ID, edgeField)
	}
	target, ok := cat.Nodes[targets[0]]
	if !ok {
		return nil, fmt.Errorf("featuresadapter: node %q's %q edge points at missing node %q", node.ID, edgeField, targets[0])
	}
	return target, nil
}

// demoFromEvidence flattens an evidence node's `producers`/`artifacts`
// fields (the seed catalog's shape: one or more artifact maps, since a
// story-driven demo's fields split across a media-recording artifact and a
// story/flow/hostCassette artifact) into one Demo.
func demoFromEvidence(evidence *graph.Node) *Demo {
	d := &Demo{
		Renderer: stringField(evidence.Fields, "renderer", ""),
		Format:   stringField(evidence.Fields, "format", ""),
		External: boolField(evidence.Fields, "external"),
		Profiles: stringSliceField(evidence.Fields, "profiles"),
	}
	d.RrwebSpec = stringField(evidence.Fields, "rrwebSpec", "")
	if embed, ok := evidence.Fields["embed"].(map[string]any); ok {
		d.Embed = &DemoEmbed{
			Deck:  stringField(embed, "deck", ""),
			Rrweb: stringField(embed, "rrweb", ""),
		}
	}
	if producers, ok := evidence.Fields["producers"].([]any); ok && len(producers) > 0 {
		if s, ok := producers[0].(string); ok {
			d.Spec = s
		}
	}
	artifacts, _ := evidence.Fields["artifacts"].([]any)
	for _, a := range artifacts {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := m["artifactDir"].(string); ok {
			d.ArtifactDir = v
		}
		if v, ok := m["videoBase"].(string); ok {
			d.VideoBase = v
		}
		if v, ok := m["posterStep"].(string); ok {
			d.PosterStep = v
		}
		if v, ok := m["story"].(string); ok {
			d.Story = v
		}
		if v, ok := m["flow"].(string); ok {
			d.Flow = v
		}
		if v, ok := m["hostCassette"].(string); ok {
			d.HostCassette = v
		}
	}
	return d
}

func boolField(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

func tourFromFields(raw map[string]any) *Tour {
	t := &Tour{}
	if export, ok := raw["export"].(string); ok {
		t.Export = export
	}
	rawSteps, _ := raw["steps"].([]any)
	for _, rs := range rawSteps {
		m, ok := rs.(map[string]any)
		if !ok {
			continue
		}
		t.Steps = append(t.Steps, TourStep{
			ID:            stringField(m, "id", ""),
			Route:         stringField(m, "route", ""),
			Target:        stringField(m, "target", ""),
			Title:         stringField(m, "title", ""),
			Body:          stringField(m, "body", ""),
			Placement:     stringField(m, "placement", ""),
			Kind:          stringField(m, "kind", ""),
			Advance:       stringField(m, "advance", ""),
			AdvanceRoute:  stringField(m, "advanceRoute", ""),
			WaitForTarget: stringField(m, "waitForTarget", ""),
			DwellMs:       intField(m, "dwellMs"),
			Drive:         driveFromFields(m),
		})
	}
	return t
}

func driveFromFields(m map[string]any) []tour.DriveAction {
	rawActions, _ := m["drive"].([]any)
	var actions []tour.DriveAction
	for _, ra := range rawActions {
		am, ok := ra.(map[string]any)
		if !ok {
			continue
		}
		actions = append(actions, tour.DriveAction{
			Type:   stringField(am, "type", ""),
			Text:   stringField(am, "text", ""),
			Intent: stringField(am, "intent", ""),
			State:  stringField(am, "state", ""),
			Ms:     intField(am, "ms"),
		})
	}
	return actions
}

func stringField(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return fallback
}

func intField(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	if i, ok := m[key].(int); ok {
		return i
	}
	return 0
}
