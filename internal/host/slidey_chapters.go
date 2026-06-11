// Package host — slidey → chapter-sidecar derivation.
//
// Part of the mockup-video-studio epic, Slice 1
// (docs/architecture/hosts.md). host.slidey.render emits a producer-agnostic
// chapter sidecar (<out>.chapters.json) from the scene list it already
// validates, so a flagged moment in the rendered video resolves back to the
// slidey scene that produced it (source_ref kind=slidey, epic decision 1).
//
// Timing note: a slidey spec carries no per-scene timestamps — slidey
// computes them at render time from narration word counts. To keep this seam
// stable and decoupled from slidey's (rounded, human-formatted) `--list`
// table, chapter windows are derived deterministically:
//   - a scene's explicit `duration_ms` (int) if present, else
//   - an even split of a top-level `total_ms` spec hint across scenes, else
//   - an even split of the RENDERED video's actual duration (ffprobed from the
//     output mp4) across the N scenes.
//
// The earlier behaviour fell back to zero-width windows (start==end==0) when
// the spec carried no timing. That produced a real bug downstream: with all
// windows collapsed to [0,0], the viewer's dominant-chapter resolver could
// never contain a mid-timeline flag, so EVERY flag resolved to scene-0 — the
// Flags-list label and the detail source_ref then disagreed with the actually
// playing slide. We now always emit REAL, non-zero, contiguous windows by
// probing the rendered video; the chapter LIST (index/id/label/source_ref) is
// of course still complete.
package host

import (
	"encoding/json"
	"fmt"
	"os"

	"kitsoki/internal/video"
)

// probeDurationMs returns the rendered video's duration in milliseconds. It is
// a package var so tests can stub it (no real ffmpeg/ffprobe in CI). The
// default implementation shells out to ffprobe.
var probeDurationMs = func(videoPath string) (int, error) {
	return video.ProbeDurationMs(videoPath)
}

// slideyScene is the subset of a slidey scene the sidecar needs. Unknown
// fields are ignored.
type slideyScene struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Type       string `json:"type"`
	Narration  string `json:"narration"`
	DurationMs int    `json:"duration_ms"`
}

// slideySpec is the subset of a slidey spec the sidecar derivation reads.
type slideySpec struct {
	TotalMs int           `json:"total_ms"`
	Scenes  []slideyScene `json:"scenes"`
}

// chaptersFromSlideySpec parses a slidey JSON spec and derives the chapter
// list (source_ref kind=slidey, spec_path=specPath). It is pure given the
// spec bytes + path. renderedMs is the actual duration of the rendered video
// used as the even-split denominator when the spec carries no timing; pass 0
// when unknown (the windows then collapse to zero width — only acceptable when
// no rendered video exists).
func chaptersFromSlideySpec(specBytes []byte, specPath string, renderedMs int) ([]video.Chapter, error) {
	var spec slideySpec
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		return nil, fmt.Errorf("parse slidey spec: %w", err)
	}
	return chaptersFromSlideyScenes(spec.Scenes, spec.TotalMs, renderedMs, specPath), nil
}

// chaptersFromSlideyScenes builds the chapter windows. See package doc for the
// timing-derivation rules.
//
// To guarantee contiguous, exactly-tiling windows even when an even split does
// not divide cleanly (e.g. 59907ms / 6), the last window is snapped to the
// total duration so the windows always cover [0, total] with no gap/overhang.
func chaptersFromSlideyScenes(scenes []slideyScene, totalMs, renderedMs int, specPath string) []video.Chapter {
	chapters := make([]video.Chapter, 0, len(scenes))
	n := len(scenes)

	// Pick the even-split denominator: an explicit spec total hint wins, else
	// the rendered video's actual duration.
	splitTotal := totalMs
	if splitTotal <= 0 {
		splitTotal = renderedMs
	}
	evenMs := 0
	if splitTotal > 0 && n > 0 {
		evenMs = splitTotal / n
	}

	cursor := 0
	for i, s := range scenes {
		id := s.ID
		if id == "" {
			id = fmt.Sprintf("scene-%d", i)
		}
		label := s.Title
		if label == "" {
			label = s.Type
		}

		width := s.DurationMs
		if width <= 0 {
			width = evenMs
		}
		start := cursor
		end := start + width
		// Snap the final scene's end to the known total so the windows tile the
		// whole timeline exactly (no rounding gap, no overhang past the video).
		if i == n-1 && splitTotal > 0 {
			end = splitTotal
		}
		cursor = end

		chapters = append(chapters, video.Chapter{
			Index:   i,
			ID:      id,
			Label:   label,
			StartMs: start,
			EndMs:   end,
			Source: video.SourceRef{
				Kind:     "slidey",
				SpecPath: specPath,
				SceneID:  id,
			},
		})
	}
	return chapters
}

// writeSlideyChapters reads the slidey spec at specPath, derives the chapter
// list, and writes the sidecar beside videoPath (<video>.chapters.json),
// returning the sidecar path. Best-effort: a read/parse failure is returned
// so the caller can log it without failing the render.
func writeSlideyChapters(specPath, videoPath string) (sidecarPath string, err error) {
	b, err := os.ReadFile(specPath)
	if err != nil {
		return "", fmt.Errorf("read slidey spec %s: %w", specPath, err)
	}
	// Probe the rendered video so the even-split fallback distributes its REAL
	// duration across the scenes (no zero-width windows). A probe failure is
	// non-fatal: renderedMs=0 falls through to the spec timing.
	renderedMs, _ := probeDurationMs(videoPath)
	chapters, err := chaptersFromSlideySpec(b, specPath, renderedMs)
	if err != nil {
		return "", err
	}
	return video.WriteChapters(videoPath, chapters)
}
