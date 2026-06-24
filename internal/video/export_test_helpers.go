package video

import "testing"

// SetLookFFmpegForTest swaps the ffmpeg-on-PATH probe for the duration of a
// test, restoring it via t.Cleanup. Test-only seam so the not-found and
// found paths are exercised deterministically without touching PATH.
func SetLookFFmpegForTest(t *testing.T, fn func() error) {
	t.Helper()
	prev := lookFFmpeg
	lookFFmpeg = fn
	t.Cleanup(func() { lookFFmpeg = prev })
}
