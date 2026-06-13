package tour

import (
	"os"
	"path/filepath"
	"regexp"
)

// observerHashRE matches the SPA's bare observer hash route (#/s/<uuid>) with no
// trailing /chat — the "any" route kind. Mirrors the spec's
// `#\/s\/[0-9a-f-]{36}$` test.
var observerHashRE = regexp.MustCompile(`#/s/[0-9a-f-]{36}$`)

// writeFileAtomic writes data to path via a temp file + rename, so a reader
// (e.g. a contact-sheet script) never sees a half-written PNG.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
