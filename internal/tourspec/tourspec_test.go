package tourspec

import (
	"testing"

	"github.com/stretchr/testify/require"
	"kitsoki/internal/tour"
)

func TestValidateAndCompile(t *testing.T) {
	s := &Spec{Version: 1, ID: "save-tour", Bindings: map[string]Binding{"save": {Target: tourTarget("save")}}, Claims: []Claim{{ID: "save", MustExpect: "saved"}}, Beats: []Beat{{ID: "intro", Kind: "highlight", Title: "Intro", Narration: "See save", Expect: []string{"saved"}, Next: []string{"save"}}, {ID: "save", Binding: "save", Kind: "gate", Title: "Save", Narration: "Click save", Expect: []string{"saved"}, AdvanceOn: "click"}}}
	if r := s.Validate(); !r.OK {
		t.Fatal(r.Errors)
	}
	m, d, e := s.Compile()
	if e != nil || d == "" || len(m.Steps) != 2 {
		t.Fatalf("compile %v %q %#v", e, d, m)
	}
}
func TestRejectsUnknownBindingAndUncoveredClaim(t *testing.T) {
	s := &Spec{Version: 1, ID: "x", Claims: []Claim{{ID: "c", MustExpect: "missing"}}, Beats: []Beat{{ID: "b", Binding: "no", Kind: "highlight", Title: "x", Narration: "x", Expect: []string{"seen"}}}}
	if s.Validate().OK {
		t.Fatal("accepted invalid spec")
	}
}

func TestCompileProjectionKeepsV2CanonicalAndRecordsHeals(t *testing.T) {
	s := &Spec{Version: 1, ID: "save-tour", Origin: "https://example.test", Bindings: map[string]Binding{"save": {Target: tourTarget("save")}}, Beats: []Beat{{ID: "save", Binding: "save", Kind: "gate", Title: "Save", Narration: "Persist", Expect: []string{"saved"}, AdvanceOn: "click"}}}
	p, err := s.CompileProjection(Profile{ID: "web-local", Origin: "https://example.test"}, []tour.HealEvent{{StepID: "save", FailedAnchor: "role", MatchedAnchor: "testid", Confidence: .9}})
	require.NoError(t, err)
	require.Equal(t, 2, p.Manifest.Version)
	require.Equal(t, "save-tour.save", p.RRWebChapters[0].ID)
	require.Equal(t, "save", p.SlideyScenes[0].StepID)
	require.Equal(t, "kitsoki.tour-spec", p.SemanticSidecar.Plugin)
	require.Equal(t, "save-tour.save", p.Anchors[0].SemanticElement.Ref)
	require.Equal(t, "testid", p.GateEvidence.Heals[0].MatchedAnchor)
}

func TestCompileProjectionRejectsMismatchedProfile(t *testing.T) {
	s := &Spec{Version: 1, ID: "save-tour", Origin: "https://one.test", Beats: []Beat{{ID: "save", Kind: "highlight", Title: "Save", Narration: "Persist", Expect: []string{"saved"}}}}
	_, err := s.CompileProjection(Profile{ID: "web-local", Origin: "https://two.test"}, nil)
	require.ErrorContains(t, err, "does not match")
}

func TestTrustedEntrypointResolvesOnlyMetadata(t *testing.T) {
	r := Registry{"demo-artifact-review": {BindingID: "demo-artifact-review", CanonicalRoom: "demoart.review", SessionKey: "demo:{session_id}", RevisionPolicy: "separate-records", AssignmentPolicy: "declared", Prerequisites: []string{"artifact-ready"}, RequestSchema: map[string]any{"version": 1}, FormSchema: map[string]any{"version": 1}, Defaults: map[string]any{"revision": "latest"}}}
	e, err := r.Resolve("demo-artifact-review", SubjectIDs{ProjectID: "p", SessionID: "s", ArtifactID: "a"})
	require.NoError(t, err)
	require.Equal(t, "demoart.review", e.CanonicalRoom)
	_, err = r.Resolve("unknown", SubjectIDs{ProjectID: "p", SessionID: "s", ArtifactID: "a"})
	require.ErrorContains(t, err, "unknown trusted binding")
}
func tourTarget(id string) tour.TargetBundle { return tour.TargetBundle{TestID: id} }
