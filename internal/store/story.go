package store

// story.go captures the *effective story* — every file the loader touches to
// build the running machine — into the trace, and reconstructs it on the way
// back out. This is what makes a trace a self-contained, deterministic replay:
// the story on disk can be edited mid-run (/reload, /meta) or after the
// session, so a replay that re-reads disk no longer reproduces what happened.
// With the source embedded, replay materialises the files to a temp dir and
// re-runs app.Load (see app.LoadFromFiles), and a future branch command can
// rewind to any turn and fork against the story effective at that turn.
//
// Wire format (StorySnapshot / StoryChanged event payloads):
//   - files are keyed by a path RELATIVE TO THE CAPTURE ROOT — the common
//     ancestor of the story's BaseDir, every imported manifest's directory,
//     and any prompt shared/overlay dirs. Keying relative to the common
//     ancestor preserves the relative layout that `import: ../sibling/app.yaml`
//     depends on, while staying portable (no absolute machine paths).
//   - `entry` is the root manifest's path relative to the same capture root,
//     so the reconstructing loader knows which file to hand app.Load.
//   - file contents are base64-encoded: the JSONL sink rejects non-NFC
//     strings, NUL bytes, and CRLF, any of which a prompt/fixture file may
//     legitimately contain. Base64 sidesteps those write-time constraints and
//     is byte-faithful. The hash is computed over the RAW bytes.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"kitsoki/internal/app"
)

// EffectiveStory is the captured source of a story at one point in time: the
// full set of files the loader touched, the entry manifest's relative path,
// and a content hash over the raw bytes. Built by CollectEffectiveStory and
// reproduced by StoryAtTurn.
type EffectiveStory struct {
	// Entry is the root manifest's path relative to the capture root (the key
	// under which it also appears in Files). Hand this to app.LoadFromFiles.
	Entry string
	// Hash is the sha256 (hex) over the canonical sorted file map. Two stories
	// with identical bytes have identical hashes; a single edited byte changes
	// it. Used to decide whether a reload actually changed anything.
	Hash string
	// Files maps each captured file's capture-root-relative path (forward
	// slashes) to its raw bytes.
	Files map[string][]byte
}

// CollectEffectiveStory walks every directory the loader touched for def — the
// BaseDir tree, each imported manifest's directory, and any prompt
// shared/overlay roots — and returns the complete file set with the entry
// manifest's relative path and a content hash. Hidden dirs (.git, .kitsoki,
// .worktrees, …), node_modules, and editor backups (~) are skipped, matching
// the metamode reload-watch's tree (internal/metamode/controller.go).
func CollectEffectiveStory(def *app.AppDef) (*EffectiveStory, error) {
	if def == nil {
		return nil, fmt.Errorf("store.CollectEffectiveStory: nil def")
	}
	if len(def.LoadedManifests) == 0 {
		return nil, fmt.Errorf("store.CollectEffectiveStory: def has no LoadedManifests (loaded without app.Load?)")
	}
	// LoadedManifests[0] is seeded with the root manifest's canonical path
	// (see app/loader.go); its directory is BaseDir. Resolve symlinks so it
	// shares the same canonical form as captureRoot and the walked file paths
	// (gatherStoryRoots resolves those) — otherwise the entry relSlash below and
	// the entry-among-files check fail on macOS, where /var/folders symlinks to
	// /private/var/folders.
	rootManifest := def.LoadedManifests[0]
	if resolved, rerr := filepath.EvalSymlinks(rootManifest); rerr == nil {
		rootManifest = resolved
	}

	roots := gatherStoryRoots(def)
	captureRoot := commonAncestorDir(roots)
	if captureRoot == "" {
		return nil, fmt.Errorf("store.CollectEffectiveStory: could not derive a capture root from %v", roots)
	}

	files := map[string][]byte{}
	for _, root := range pruneNestedRoots(roots) {
		if err := walkStoryRoot(root, captureRoot, files); err != nil {
			return nil, err
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("store.CollectEffectiveStory: captured no files under %s", captureRoot)
	}

	entry, err := relSlash(captureRoot, rootManifest)
	if err != nil {
		return nil, fmt.Errorf("store.CollectEffectiveStory: entry rel: %w", err)
	}
	if _, ok := files[entry]; !ok {
		return nil, fmt.Errorf("store.CollectEffectiveStory: entry %q not among captured files", entry)
	}

	return &EffectiveStory{Entry: entry, Hash: hashStoryFiles(files), Files: files}, nil
}

// gatherStoryRoots returns the absolute directories that constitute def's
// effective story: BaseDir, each loaded manifest's directory, and resolved
// prompt shared/overlay dirs.
func gatherStoryRoots(def *app.AppDef) []string {
	seen := map[string]struct{}{}
	var roots []string
	add := func(p string) {
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		abs = filepath.Clean(abs)
		// Canonicalise symlinks so every root shares ONE form. The loader stores
		// some paths symlink-resolved (LoadedManifests are canonical) and others
		// raw (BaseDir), and on macOS the temp root /var/folders is itself a
		// symlink to /private/var/folders. Mixed forms break relSlash —
		// filepath.Rel can't relativise a /private/var path against a /var
		// captureRoot — so file keys come out absolute instead of story-relative
		// (e.g. "private/var/.../views/base.pongo" instead of "views/base.pongo"),
		// which also changes the content hash. EvalSymlinks is best-effort: a
		// path that doesn't resolve falls back to the cleaned abs form.
		if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
			abs = resolved
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		roots = append(roots, abs)
	}

	add(def.BaseDir)
	for _, m := range def.LoadedManifests {
		add(filepath.Dir(m))
	}
	if def.Prompts != nil {
		for _, sh := range def.Prompts.Shared {
			add(resolveAgainst(def.BaseDir, sh))
		}
		if def.Prompts.Overlay != "" {
			add(resolveAgainst(def.BaseDir, def.Prompts.Overlay))
		}
	}
	sort.Strings(roots)
	return roots
}

// resolveAgainst joins rel onto base when rel is relative; absolute rel passes
// through. Mirrors how the loader resolves story-relative paths.
func resolveAgainst(base, rel string) string {
	if rel == "" {
		return ""
	}
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(base, rel)
}

// pruneNestedRoots drops any root that nests under another (its files are
// already covered by the ancestor's walk), so overlapping roots aren't walked
// twice. Order-independent: a root survives only if no OTHER root is a strict
// ancestor of it.
func pruneNestedRoots(roots []string) []string {
	var out []string
	for _, r := range roots {
		nested := false
		for _, other := range roots {
			if other == r {
				continue
			}
			if strings.HasPrefix(r+string(filepath.Separator), other+string(filepath.Separator)) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, r)
		}
	}
	return out
}

// walkStoryRoot walks root and adds every regular file to files, keyed by its
// path relative to captureRoot (forward slashes). Hidden dirs, node_modules,
// hidden files, and ~ backups are skipped. Symlinks are not followed.
func walkStoryRoot(root, captureRoot string, files map[string][]byte) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, relErr := relSlash(captureRoot, path)
		if relErr != nil {
			return nil
		}
		files[rel] = b
		return nil
	})
}

// relSlash returns target's path relative to base, using forward slashes for
// portability across machines.
func relSlash(base, target string) (string, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// commonAncestorDir returns the deepest directory that is an ancestor of every
// path in dirs. All inputs must be absolute. Returns "" if dirs is empty.
func commonAncestorDir(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	split := func(p string) []string {
		return strings.Split(filepath.Clean(p), string(filepath.Separator))
	}
	prefix := split(dirs[0])
	for _, d := range dirs[1:] {
		parts := split(d)
		n := len(prefix)
		if len(parts) < n {
			n = len(parts)
		}
		i := 0
		for i < n && prefix[i] == parts[i] {
			i++
		}
		prefix = prefix[:i]
	}
	res := strings.Join(prefix, string(filepath.Separator))
	if res == "" {
		// All paths shared only the filesystem root (leading separator).
		return string(filepath.Separator)
	}
	return res
}

// hashStoryFiles returns a sha256 hex digest over the file map. The digest is
// order-independent (paths are sorted) and unambiguous (each entry contributes
// its path length, path, byte length, and bytes), so distinct maps cannot
// collide via boundary ambiguity.
func hashStoryFiles(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	var lenbuf [8]byte
	for _, p := range paths {
		binary.BigEndian.PutUint64(lenbuf[:], uint64(len(p)))
		h.Write(lenbuf[:])
		h.Write([]byte(p))
		b := files[p]
		binary.BigEndian.PutUint64(lenbuf[:], uint64(len(b)))
		h.Write(lenbuf[:])
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// --- Event payload shapes -------------------------------------------------

type storySnapshotPayload struct {
	AppID string            `json:"app_id,omitempty"`
	Entry string            `json:"entry"`
	Hash  string            `json:"hash"`
	Files map[string]string `json:"files"` // capture-root-relative path → base64(raw bytes)
}

type storyChangedPayload struct {
	Hash     string            `json:"hash"`
	PrevHash string            `json:"prev_hash,omitempty"`
	Changed  map[string]string `json:"changed,omitempty"` // added/modified: relpath → base64
	Removed  []string          `json:"removed,omitempty"` // deleted relpaths
}

// StorySnapshotPayload builds the StorySnapshot event payload for es.
func StorySnapshotPayload(appID string, es *EffectiveStory) map[string]any {
	return map[string]any{
		"app_id": appID,
		"entry":  es.Entry,
		"hash":   es.Hash,
		"files":  encodeFiles(es.Files),
	}
}

// StoryChangedPayload builds the StoryChanged diff payload between oldFiles and
// es.Files. It returns (payload, true) when there is any difference, or
// (nil, false) when the two file sets are byte-identical.
func StoryChangedPayload(prevHash string, oldFiles map[string][]byte, es *EffectiveStory) (map[string]any, bool) {
	changed := map[string]string{}
	for p, b := range es.Files {
		ob, ok := oldFiles[p]
		if !ok || !bytesEqual(ob, b) {
			changed[p] = base64.StdEncoding.EncodeToString(b)
		}
	}
	var removed []string
	for p := range oldFiles {
		if _, ok := es.Files[p]; !ok {
			removed = append(removed, p)
		}
	}
	sort.Strings(removed)
	if len(changed) == 0 && len(removed) == 0 {
		return nil, false
	}
	return map[string]any{
		"hash":      es.Hash,
		"prev_hash": prevHash,
		"changed":   changed,
		"removed":   removed,
	}, true
}

func encodeFiles(files map[string][]byte) map[string]string {
	out := make(map[string]string, len(files))
	for p, b := range files {
		out[p] = base64.StdEncoding.EncodeToString(b)
	}
	return out
}

func decodeFiles(enc map[string]string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(enc))
	for p, s := range enc {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("decode file %q: %w", p, err)
		}
		out[p] = b
	}
	return out, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- Reconstruction from history -----------------------------------------

// LatestStoryHash returns the hash of the most recent story event (snapshot or
// diff) in history, or "" when no story has been recorded yet. A non-empty
// result implies a base StorySnapshot exists (it is always recorded first).
func LatestStoryHash(history History) string {
	hash := ""
	for _, ev := range history {
		switch ev.Kind {
		case StorySnapshot:
			var p storySnapshotPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				hash = p.Hash
			}
		case StoryChanged:
			var p storyChangedPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				hash = p.Hash
			}
		}
	}
	return hash
}

// StoryAtTurn reconstructs the effective story as of the given turn: the latest
// StorySnapshot with Turn ≤ turn, then every StoryChanged with base.Turn <
// ev.Turn ≤ turn applied in order (changed files overlaid, removed files
// dropped). Returns the file map and the entry manifest's relative path,
// ready for app.LoadFromFiles. Errors when no snapshot is present at or before
// turn.
func StoryAtTurn(history History, turn app.TurnNumber) (map[string][]byte, string, error) {
	// Locate the latest base snapshot at or before the target turn.
	baseIdx := -1
	for i, ev := range history {
		if ev.Kind == StorySnapshot && ev.Turn <= turn {
			baseIdx = i
		}
	}
	if baseIdx < 0 {
		return nil, "", fmt.Errorf("store.StoryAtTurn: no session.story snapshot at or before turn %d", turn)
	}

	var base storySnapshotPayload
	if err := json.Unmarshal(history[baseIdx].Payload, &base); err != nil {
		return nil, "", fmt.Errorf("store.StoryAtTurn: decode snapshot: %w", err)
	}
	files, err := decodeFiles(base.Files)
	if err != nil {
		return nil, "", err
	}

	// Apply diffs recorded after the snapshot, up to and including the turn.
	for _, ev := range history[baseIdx+1:] {
		if ev.Kind != StoryChanged || ev.Turn > turn {
			continue
		}
		var d storyChangedPayload
		if err := json.Unmarshal(ev.Payload, &d); err != nil {
			return nil, "", fmt.Errorf("store.StoryAtTurn: decode diff turn=%d: %w", ev.Turn, err)
		}
		changed, err := decodeFiles(d.Changed)
		if err != nil {
			return nil, "", err
		}
		for p, b := range changed {
			files[p] = b
		}
		for _, p := range d.Removed {
			delete(files, p)
		}
	}

	return files, base.Entry, nil
}
