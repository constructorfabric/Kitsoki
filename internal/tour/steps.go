package tour

import (
	"encoding/json"
	"fmt"
)

// StepShot is the deterministic record tying one captured per-step poster PNG to
// the exact place in the tour spec it depicts. It exists so a consumer (e.g. the
// kitsoki-ui-qa gate) can map a poster frame to its spec step WITHOUT an LLM
// interpreting the pixels: the frame is, by construction, the screenshot taken
// after the renderer drove spec step [StepSpecRef.Pointer], confirmed the overlay
// popover showed [Title] (the anti-drift assertTitle check), and polled the live
// session to each machine state in [StatesAsserted] (the step's wait-state
// drives). Those are machine-verified facts the renderer already enforced — not
// opinions about pixels.
//
// The records are written to "<video>.steps.json" (a sibling of the MP4 and the
// .chapters.json sidecar), one entry per captured PNG, in capture order.
type StepShot struct {
	// Capture is the 1-based capture ordinal — it matches the NN prefix in PNG
	// (e.g. Capture 5 ⇄ "05-<step-id>.png"). Route-skipped steps take no shot,
	// so Capture can be sparser than StepIndex.
	Capture int `json:"capture"`
	// StepIndex is the step's 0-based index in the spec's steps array (the N in
	// the JSON pointer).
	StepIndex int `json:"step_index"`
	// PNG is the poster-frame basename, relative to this sidecar's directory.
	PNG string `json:"png"`
	// SpecRef is the exact, referential spec location this PNG depicts.
	SpecRef StepSpecRef `json:"spec_ref"`
	// Title is the overlay popover title — asserted to equal the spec step's
	// title on screen at capture (see TitleAsserted).
	Title string `json:"title"`
	// Body is the narration body, verbatim from the spec step.
	Body string `json:"body,omitempty"`
	// Route is the step's route ("home" | "interactive" | "any").
	Route string `json:"route"`
	// WaitForTarget is the DOM-presence precondition the renderer waited on
	// before capture (empty when the step declares none).
	WaitForTarget string `json:"wait_for_target,omitempty"`
	// StatesAsserted are the machine states the step's wait-state drives polled
	// the session to, in order — the deterministic proof the session reached
	// them before the screenshot.
	StatesAsserted []string `json:"states_asserted,omitempty"`
	// TitleAsserted records that the renderer verified Title was on screen at
	// capture time (always true for an emitted shot; explicit so a consumer need
	// not infer it).
	TitleAsserted bool `json:"title_asserted"`
}

// StepSpecRef points at the exact place in the tour spec a [StepShot] depicts:
// the source file, a JSON pointer into its steps array, and the step id. It
// parallels [video.SourceRef] (the chapter sidecar's reference) but adds the
// JSON Pointer so the location is addressable, not just identifiable.
type StepSpecRef struct {
	// Kind is the producer — always "tour" here.
	Kind string `json:"kind"`
	// SpecPath is the spec source file (repo-relative when resolvable), e.g.
	// "features/dev-story-prd-design.yaml".
	SpecPath string `json:"spec_path"`
	// Pointer is an RFC 6901 JSON Pointer into SpecPath's parsed document, e.g.
	// "/tour/steps/5" for a feature catalog or "/steps/5" for a --manifest file.
	Pointer string `json:"pointer"`
	// StepID is the spec step's id (unique within the spec).
	StepID string `json:"step_id"`
}

// stepShot builds the deterministic record for a captured step. capture is the
// 1-based capture ordinal; stepIndex is the step's index in the manifest.
func (m *TourManifest) stepShot(capture, stepIndex int, pngBase string, step TourStep) StepShot {
	return StepShot{
		Capture:   capture,
		StepIndex: stepIndex,
		PNG:       pngBase,
		SpecRef: StepSpecRef{
			Kind:     "tour",
			SpecPath: m.SpecPath,
			Pointer:  m.stepPointer(stepIndex),
			StepID:   step.ID,
		},
		Title:          step.Title,
		Body:           step.Body,
		Route:          step.Route,
		WaitForTarget:  step.WaitForTarget,
		StatesAsserted: waitStates(step.Drive),
		TitleAsserted:  true,
	}
}

// waitStates returns, in order, the machine states the step's wait-state drive
// actions poll the session to. These are the deterministic state assertions the
// renderer enforced before the step's screenshot.
func waitStates(drive []DriveAction) []string {
	var states []string
	for _, a := range drive {
		if a.Type == DriveWaitState && a.State != "" {
			states = append(states, a.State)
		}
	}
	return states
}

// StepsSidecarPath returns the canonical per-step sidecar path for a video:
// "<video>.steps.json" (sibling to the MP4 and the .chapters.json sidecar).
func StepsSidecarPath(videoPath string) string {
	return videoPath + ".steps.json"
}

// writeStepShots writes the per-step records to videoPath's steps sidecar as a
// JSON array (one entry per captured PNG, in capture order), returning the
// sidecar path.
func writeStepShots(videoPath string, shots []StepShot) (string, error) {
	if shots == nil {
		shots = []StepShot{}
	}
	path := StepsSidecarPath(videoPath)
	data, err := json.MarshalIndent(shots, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal step shots: %w", err)
	}
	data = append(data, '\n')
	if err := writeFileAtomic(path, data); err != nil {
		return "", fmt.Errorf("write step shots: %w", err)
	}
	return path, nil
}
