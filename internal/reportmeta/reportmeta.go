// Package reportmeta captures runtime version metadata for filed bug reports.
package reportmeta

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"kitsoki/internal/app"
	"kitsoki/internal/buildinfo"
)

// Field is one flat key/value entry in the structured kitsoki metadata block.
type Field struct {
	Key   string
	Value string
}

// Engine describes the exact kitsoki engine executable/source identity that
// filed a report.
type Engine struct {
	Version        string `json:"version,omitempty"`
	Revision       string `json:"revision,omitempty"`
	RevisionShort  string `json:"revision_short,omitempty"`
	Dirty          string `json:"dirty,omitempty"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
}

// Story describes the loaded app definition that was active for a report.
type Story struct {
	AppID          string `json:"app_id,omitempty"`
	Version        string `json:"version,omitempty"`
	Entry          string `json:"entry,omitempty"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
}

// PublicStory describes one loaded reusable/public story package.
type PublicStory struct {
	Name           string `json:"name,omitempty"`
	AppID          string `json:"app_id,omitempty"`
	Version        string `json:"version,omitempty"`
	Source         string `json:"source,omitempty"`
	Path           string `json:"path,omitempty"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
}

// Snapshot is the structured version metadata stamped onto a filed report.
type Snapshot struct {
	Engine        Engine        `json:"engine,omitempty"`
	Story         Story         `json:"story,omitempty"`
	PublicStories []PublicStory `json:"public_stories,omitempty"`
}

// RuntimeFieldKeys is the stable order of flat metadata keys written into
// GitHub's fenced ```kitsoki block and local bug frontmatter.
var RuntimeFieldKeys = []string{
	"engine_version",
	"engine_revision",
	"engine_revision_short",
	"engine_dirty",
	"engine_checksum_sha256",
	"story_app_id",
	"story_app_version",
	"story_entry",
	"story_checksum_sha256",
	"public_stories_json",
}

// Capture snapshots the running engine and, when def is supplied, the loaded
// story/public-story checksums. root is the repo/source root used for git and
// relative path resolution; an empty root falls back to the process cwd.
func Capture(root string, def *app.AppDef) Snapshot {
	root = cleanRoot(root)
	snap := Snapshot{Engine: captureEngine(root)}
	if def != nil {
		snap.Story = captureStory(root, def)
		snap.PublicStories = capturePublicStories(root, def)
	}
	return snap
}

// Empty reports whether the snapshot contains no useful report metadata.
func (s Snapshot) Empty() bool {
	return s.Engine == (Engine{}) && s.Story == (Story{}) && len(s.PublicStories) == 0
}

// Fields renders the snapshot into a stable flat key/value list. The public
// stories remain structured as one compact JSON value so GitHub's line-oriented
// kitsoki metadata block stays backward-compatible.
func (s Snapshot) Fields() []Field {
	raw := map[string]string{}
	if s.Engine.Version != "" {
		raw["engine_version"] = s.Engine.Version
	}
	if s.Engine.Revision != "" {
		raw["engine_revision"] = s.Engine.Revision
	}
	if s.Engine.RevisionShort != "" {
		raw["engine_revision_short"] = s.Engine.RevisionShort
	}
	if s.Engine.Dirty != "" {
		raw["engine_dirty"] = s.Engine.Dirty
	}
	if s.Engine.ChecksumSHA256 != "" {
		raw["engine_checksum_sha256"] = s.Engine.ChecksumSHA256
	}
	if s.Story.AppID != "" {
		raw["story_app_id"] = s.Story.AppID
	}
	if s.Story.Version != "" {
		raw["story_app_version"] = s.Story.Version
	}
	if s.Story.Entry != "" {
		raw["story_entry"] = s.Story.Entry
	}
	if s.Story.ChecksumSHA256 != "" {
		raw["story_checksum_sha256"] = s.Story.ChecksumSHA256
	}
	if len(s.PublicStories) > 0 {
		if b, err := json.Marshal(s.PublicStories); err == nil {
			raw["public_stories_json"] = string(b)
		}
	}
	out := make([]Field, 0, len(raw))
	for _, key := range RuntimeFieldKeys {
		if value := strings.TrimSpace(raw[key]); value != "" {
			out = append(out, Field{Key: key, Value: value})
		}
	}
	return out
}

// Fence renders the snapshot as the same fenced metadata block used by GitHub
// issue creation. It returns "" when there are no fields to write.
func Fence(s Snapshot) string {
	fields := s.Fields()
	if len(fields) == 0 {
		return ""
	}
	lines := make([]string, 0, len(fields))
	for _, f := range fields {
		lines = append(lines, f.Key+": "+f.Value)
	}
	return "```kitsoki\n" + strings.Join(lines, "\n") + "\n```"
}

// AppendFence appends Fence(s) to body, preserving the existing issue body shape.
func AppendFence(body string, s Snapshot) string {
	block := Fence(s)
	if block == "" {
		return body
	}
	if strings.TrimSpace(body) == "" {
		return block
	}
	return strings.TrimRight(body, "\n") + "\n\n" + block + "\n"
}

func captureEngine(root string) Engine {
	out := Engine{
		Version:       strings.TrimSpace(buildinfo.Version),
		Revision:      strings.TrimSpace(buildinfo.Revision),
		RevisionShort: strings.TrimSpace(buildinfo.RevisionShort),
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if out.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			out.Version = bi.Main.Version
		}
		if out.Revision == "" {
			for _, setting := range bi.Settings {
				switch setting.Key {
				case "vcs.revision":
					out.Revision = strings.TrimSpace(setting.Value)
				case "vcs.modified":
					out.Dirty = strings.TrimSpace(setting.Value)
				}
			}
		}
	}
	if out.Revision == "" {
		out.Revision = gitOutput(root, "rev-parse", "HEAD")
	}
	if out.RevisionShort == "" {
		if out.Revision != "" {
			out.RevisionShort = shortRevision(out.Revision)
		} else {
			out.RevisionShort = gitOutput(root, "rev-parse", "--short", "HEAD")
		}
	}
	if out.Dirty == "" {
		if status := gitOutput(root, "status", "--short"); status != "" {
			out.Dirty = "true"
		} else if out.Revision != "" {
			out.Dirty = "false"
		}
	}
	if sum := executableSHA256(); sum != "" {
		out.ChecksumSHA256 = sum
	}
	return out
}

func captureStory(root string, def *app.AppDef) Story {
	story := Story{
		AppID:   strings.TrimSpace(def.App.ID),
		Version: strings.TrimSpace(def.App.Version),
	}
	if len(def.LoadedManifests) > 0 {
		story.Entry = displayPath(root, def.LoadedManifests[0])
	}
	if hash := hashRoots(storyRoots(def)); hash != "" {
		story.ChecksumSHA256 = hash
	}
	return story
}

func capturePublicStories(root string, def *app.AppDef) []PublicStory {
	seen := map[string]bool{}
	var out []PublicStory
	for _, manifest := range def.LoadedManifests {
		name, source, ok := publicStoryName(manifest)
		if !ok {
			continue
		}
		dir := filepath.Dir(manifest)
		key := source + ":" + name + ":" + filepath.Clean(dir)
		if seen[key] {
			continue
		}
		seen[key] = true
		id, version := readAppMeta(manifest)
		out = append(out, PublicStory{
			Name:           name,
			AppID:          id,
			Version:        version,
			Source:         source,
			Path:           displayPath(root, manifest),
			ChecksumSHA256: hashRoots([]string{dir}),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func cleanRoot(root string) string {
	if strings.TrimSpace(root) == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return filepath.Clean(root)
}

func storyRoots(def *app.AppDef) []string {
	seen := map[string]bool{}
	var roots []string
	add := func(dir string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		if _, err := os.Stat(dir); err != nil {
			return
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			return
		}
		seen[dir] = true
		roots = append(roots, dir)
	}
	for _, manifest := range def.LoadedManifests {
		if strings.TrimSpace(manifest) == "" {
			continue
		}
		add(filepath.Dir(manifest))
	}
	if def.BaseDir != "" {
		if _, err := os.Stat(filepath.Join(def.BaseDir, "app.yaml")); err == nil {
			add(def.BaseDir)
		}
	}
	if def.Prompts != nil {
		for _, dir := range def.Prompts.Shared {
			add(resolveAgainst(def.BaseDir, dir))
		}
		if def.Prompts.Overlay != "" {
			add(resolveAgainst(def.BaseDir, def.Prompts.Overlay))
		}
	}
	sort.Strings(roots)
	return pruneNestedRoots(roots)
}

func resolveAgainst(base, rel string) string {
	if rel == "" || filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(base, rel)
}

func publicStoryName(manifest string) (name, source string, ok bool) {
	clean := filepath.ToSlash(filepath.Clean(manifest))
	parts := strings.Split(clean, "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] != "stories" || parts[i+2] != "app.yaml" {
			continue
		}
		source = "public"
		if i >= 2 && parts[i-2] == "internal" && parts[i-1] == "basestories" {
			source = "embedded"
		}
		return parts[i+1], source, true
	}
	return "", "", false
}

func readAppMeta(manifest string) (id, version string) {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return "", ""
	}
	var raw struct {
		App struct {
			ID      string `yaml:"id"`
			Version string `yaml:"version"`
		} `yaml:"app"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return "", ""
	}
	return raw.App.ID, raw.App.Version
}

func displayPath(root, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if root != "" {
		if rel, err := filepath.Rel(root, abs); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func executableSHA256() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

func gitOutput(root string, args ...string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func shortRevision(rev string) string {
	rev = strings.TrimSpace(rev)
	if len(rev) <= 12 {
		return rev
	}
	return rev[:12]
}

func hashRoots(roots []string) string {
	roots = pruneNestedRoots(roots)
	if len(roots) == 0 {
		return ""
	}
	captureRoot := commonAncestorDir(roots)
	files := map[string][]byte{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
			rel, relErr := filepath.Rel(captureRoot, path)
			if relErr != nil {
				return nil
			}
			files[filepath.ToSlash(rel)] = b
			return nil
		})
	}
	if len(files) == 0 {
		return ""
	}
	return "sha256:" + hashFiles(files)
}

func hashFiles(files map[string][]byte) string {
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

func pruneNestedRoots(roots []string) []string {
	cleaned := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" {
			continue
		}
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		root = filepath.Clean(root)
		if seen[root] {
			continue
		}
		seen[root] = true
		cleaned = append(cleaned, root)
	}
	sort.Strings(cleaned)
	var out []string
	for _, r := range cleaned {
		nested := false
		for _, other := range cleaned {
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
		return string(filepath.Separator)
	}
	return res
}
