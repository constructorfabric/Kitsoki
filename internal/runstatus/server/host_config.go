// host_config.go — GET /api/config, the host staleness surface (U3,
// feedback report 01KXD23B3B4WZJ21XZH0VGJYC6).
//
// An embedding host page (e.g. the POG portal) needs to tell whether the
// kitsoki host it is talking to is stale relative to the repo it serves:
// an outdated installed binary, edited stories, or an edited catalog all
// silently change behavior. This endpoint exposes exactly the three
// signals the portal compares:
//
//	GET /api/config ->
//	  {"binary_build":   "<vcs revision[-dirty]|module version|devel>",
//	   "stories_digest": "sha256:<hex>",   // content digest of the story dirs
//	   "catalog_digest": "sha256:<hex>"}   // content digest of <root>/pog/catalog.yaml ("" when absent)
//
// A plain GET (not a /rpc method) so a non-SPA poller needs no JSON-RPC
// envelope. Digests are content hashes with a short TTL cache — cheap under
// polling, and honest immediately after an edit.
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

// WithStoryDirs tells the server which story directories it is serving, so
// GET /api/config can digest their content. Empty (the default) reports an
// empty-set digest.
func WithStoryDirs(dirs []string) Option {
	return func(c *serverConfig) { c.storyDirs = append([]string(nil), dirs...) }
}

// hostConfigCacheTTL bounds how often a polling host re-hashes the story
// dirs / catalog from disk.
const hostConfigCacheTTL = 2 * time.Second

type hostConfigCache struct {
	mu            sync.Mutex
	at            time.Time
	storiesDigest string
	catalogDigest string
}

func (s *Server) handleAPIConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeFeedbackJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET only"})
		return
	}
	stories, catalog := s.hostDigests()
	writeFeedbackJSON(w, http.StatusOK, map[string]any{
		"binary_build":   binaryBuild(),
		"stories_digest": stories,
		"catalog_digest": catalog,
	})
}

func (s *Server) hostDigests() (storiesDigest, catalogDigest string) {
	c := &s.hostConfig
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < hostConfigCacheTTL {
		return c.storiesDigest, c.catalogDigest
	}
	c.storiesDigest = digestDirs(s.storyDirs)
	// The home-repo catalog convention graphAllowlist already uses:
	// <materializeRoot>/pog/catalog.yaml.
	c.catalogDigest = digestFile(filepath.Join(s.feedbackRepoRoot(), "pog", "catalog.yaml"))
	c.at = time.Now()
	return c.storiesDigest, c.catalogDigest
}

// binaryBuild identifies the running binary: the embedded VCS revision
// (plus "-dirty" when built from a modified tree), else the module
// version, else "devel". Computed once — the binary cannot change under a
// running process.
var binaryBuild = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	var rev, modified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if modified == "true" {
			rev += "-dirty"
		}
		return rev
	}
	if v := strings.TrimSpace(bi.Main.Version); v != "" && v != "(devel)" {
		return v
	}
	return "devel"
})

// digestDirs content-digests a set of directories: every regular file
// (skipping .git and other dot-directories), keyed by its dir-index +
// slash-normalized relative path so the digest is stable across walk
// order and OS path separators but distinguishes which configured dir a
// file lives in. Missing/unreadable entries contribute a marker rather
// than failing — staleness polling must never error the endpoint.
func digestDirs(dirs []string) string {
	h := sha256.New()
	for i, dir := range dirs {
		type entry struct{ rel, sum string }
		var entries []entry
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable subtree: skip, keep the digest total
			}
			if d.IsDir() {
				if name := d.Name(); name != "." && strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, rerr := filepath.Rel(dir, path)
			if rerr != nil {
				return nil
			}
			entries = append(entries, entry{rel: filepath.ToSlash(rel), sum: fileSHA256(path)})
			return nil
		})
		sort.Slice(entries, func(a, b int) bool { return entries[a].rel < entries[b].rel })
		for _, e := range entries {
			fmt.Fprintf(h, "%d\x00%s\x00%s\x00", i, e.rel, e.sum)
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// digestFile content-digests a single file; "" when it does not exist or
// cannot be read (an absent catalog is a real state, not an error).
func digestFile(path string) string {
	sum := fileSHA256(path)
	if sum == "" {
		return ""
	}
	return "sha256:" + sum
}

func fileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
