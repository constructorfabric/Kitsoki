package video

import (
	"sync"
	"testing"
)

var lookFFmpegTestMu sync.Mutex

// SetLookFFmpegForTest swaps the ffmpeg-on-PATH probe for the duration of a
// test, restoring it via t.Cleanup. Test-only seam so the not-found and
// found paths are exercised deterministically without touching PATH.
func SetLookFFmpegForTest(t *testing.T, fn func() error) {
	t.Helper()
	lookFFmpegTestMu.Lock()
	prev := lookFFmpeg
	lookFFmpeg = fn
	t.Cleanup(func() {
		lookFFmpeg = prev
		lookFFmpegTestMu.Unlock()
	})
}
