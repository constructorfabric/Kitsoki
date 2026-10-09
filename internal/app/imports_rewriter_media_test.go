package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRewriteViewElement_MediaFieldsPrefixed is a regression
// guard for the bug: rewriteViewElement copied a media element's MediaHandle
// verbatim instead of running it through the import rewriter. When a child room
// is imported under an alias (e.g. "core"), its `media` element handle
// `{{ world.mockup_handle }}` must become `{{ world.core__mockup_handle }}` —
// otherwise the handle resolves against the non-existent bare key and the
// embedded substrate (slideshow iframe) renders empty, even though the bind
// stored core__mockup_handle correctly. This is
// exactly what broke dev-story's prd_published mockup embed when run via the
// slidey-dev instance (imports @kitsoki/dev-story as core).
//
// MediaCaption and MediaPath were already rewritten beside MediaHandle; this
// test pins MediaHandle, AnnotateIntent, and AnnotateURL to the same treatment.
func TestRewriteViewElement_MediaFieldsPrefixed(t *testing.T) {
	rw := &childRewriter{
		alias: "core",
		childWorldKey: map[string]struct{}{
			"mockup_handle":     {},
			"mockup_review_url": {},
		},
		childIntent: map[string]struct{}{
			"refine": {},
		},
	}

	in := ViewElement{
		Kind:                 "media",
		MediaHandle:          "{{ world.mockup_handle }}",
		MediaCaption:         "the mockup the PRD describes",
		MediaKind:            "slideshow",
		AnnotateURL:          "{{ world.mockup_review_url }}",
		AnnotateIntent:       "refine",
		AnnotateFeedbackSlot: "feedback",
	}

	out := rw.rewriteViewElement(in)

	require.Equal(t, "{{ world.core__mockup_handle }}", out.MediaHandle,
		"media handle world-ref must be prefixed with the import alias")
	require.Equal(t, "core__refine", out.AnnotateIntent,
		"media annotate_intent must be rewritten to the prefixed intent name")
	require.Equal(t, "{{ world.core__mockup_review_url }}", out.AnnotateURL,
		"media annotate_url world-ref must be prefixed with the import alias")
	// The feedback slot is a slot NAME, not a ref — preserved verbatim.
	require.Equal(t, "feedback", out.AnnotateFeedbackSlot)
	require.Equal(t, "slideshow", out.MediaKind)
}
