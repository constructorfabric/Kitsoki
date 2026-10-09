package storyboard

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"kitsoki/internal/video"
)

// dwellTolerance is how much of the planned dwell a captured chapter window
// may undershoot before the check flags it — capture overhead trims a little
// off every window, so an exact comparison would be all noise.
const dwellTolerance = 0.8

// CheckChapters diffs a captured video against the plan via its chapter
// sidecar: every scene must appear as a chapter (matched by id), scenes must
// appear in plan order, and each matched chapter's window must hold at least
// [dwellTolerance] of the planned dwell. Extra chapters are a warning
// (unplanned scenes), not an error. path may be the video or the
// `<video>.chapters.json` sidecar itself.
func CheckChapters(s *Storyboard, path string) ([]Issue, error) {
	chapters, err := readChapters(path)
	if err != nil {
		return nil, err
	}
	var issues []Issue

	byID := map[string]video.Chapter{}
	order := map[string]int{}
	for i, ch := range chapters {
		byID[ch.ID] = ch
		order[ch.ID] = i
	}

	lastIdx := -1
	planned := map[string]bool{}
	for _, sc := range s.Scenes {
		planned[sc.ID] = true
		ch, ok := byID[sc.ID]
		if !ok {
			issues = append(issues, Issue{SeverityError, sc.ID, "planned scene has no chapter in the capture"})
			continue
		}
		if idx := order[sc.ID]; idx < lastIdx {
			issues = append(issues, Issue{SeverityError, sc.ID, "captured out of plan order"})
		} else {
			lastIdx = idx
		}
		window := ch.EndMs - ch.StartMs
		if minWindow := int(float64(sc.DwellMs) * dwellTolerance); window < minWindow {
			issues = append(issues, Issue{SeverityWarn, sc.ID,
				fmt.Sprintf("captured window %.1fs is under the planned dwell %.1fs (tolerance %.0f%%)",
					float64(window)/1000, float64(sc.DwellMs)/1000, dwellTolerance*100)})
		}
	}
	for _, ch := range chapters {
		if !planned[ch.ID] {
			issues = append(issues, Issue{SeverityWarn, ch.ID, "captured chapter is not in the storyboard (unplanned scene)"})
		}
	}
	return issues, nil
}

func readChapters(path string) ([]video.Chapter, error) {
	if !strings.HasSuffix(path, ".chapters.json") {
		return video.ReadChapters(path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var chapters []video.Chapter
	if err := json.Unmarshal(b, &chapters); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return chapters, nil
}
