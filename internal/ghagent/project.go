package ghagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"kitsoki/internal/app"
	"kitsoki/internal/basestories"
	"kitsoki/internal/kitrepo"
	"kitsoki/internal/webconfig"
)

const (
	projectBugBeat     = "project-bug.beat.yaml"
	projectFeatureBeat = "project-feature.beat.yaml"
)

// ProjectRouteResolver maps GitHub issue routes onto an onboarded local project
// checkout. A hosted service still needs an explicit local checkout path; absent
// that, the default Kitsoki story routes are left unchanged.
type ProjectRouteResolver struct {
	Root string
}

// Apply returns a project-local route for issue mentions when Root points at an
// onboarded checkout. PR sentinel routes and unconfigured projects fall back to
// the caller's default route.
func (r ProjectRouteResolver) Apply(route Route, mention Mention) (Route, bool, error) {
	root := strings.TrimSpace(r.Root)
	if root == "" || mention.Item.Kind != "issue" {
		return route, false, nil
	}
	if route.Story != "stories/bugfix" && route.Story != "stories/dev-story" {
		return route, false, nil
	}
	projectApp, err := discoverProjectApp(root)
	if err != nil {
		return route, false, err
	}
	if projectApp == "" {
		return route, false, nil
	}
	next := route
	next.AppPath = projectApp
	next.ProjectRoot = absPath(root)
	next.Story = "project:" + relOrBase(next.ProjectRoot, projectApp)
	if route.Story == "stories/bugfix" {
		next.BeatFixture = projectBeatPath(projectBugBeat)
		next.World = mergeRouteWorld(route.World, map[string]any{"ticket_type": "bug"})
	} else {
		next.BeatFixture = projectBeatPath(projectFeatureBeat)
		next.World = mergeRouteWorld(route.World, map[string]any{"ticket_type": "feature"})
	}
	return next, true, nil
}

func discoverProjectApp(root string) (string, error) {
	root = absPath(root)
	cfgPath := filepath.Join(root, webconfig.DefaultConfigFile)
	if _, err := os.Stat(cfgPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	cfg, err := webconfig.Load(cfgPath)
	if err != nil {
		return "", err
	}
	dirs := webconfig.Resolve(nil, cfg)
	for i, dir := range dirs {
		if !filepath.IsAbs(dir) {
			dirs[i] = filepath.Join(root, dir)
		}
	}
	metas, err := webconfig.DiscoverStories(dirs, embeddedImportResolver())
	if err != nil {
		return "", err
	}
	if path := chooseProjectStory(root, metas); path != "" {
		return path, nil
	}
	return materializeImplicitProjectApp(root, cfg)
}

func chooseProjectStory(root string, metas []webconfig.StoryMeta) string {
	if len(metas) == 0 {
		return ""
	}
	root = absPath(root)
	sort.Slice(metas, func(i, j int) bool { return metas[i].Path < metas[j].Path })
	projectStories := filepath.Join(root, ".kitsoki", "stories")
	for _, meta := range metas {
		if strings.HasPrefix(absPath(meta.Path), projectStories+string(os.PathSeparator)) {
			return meta.Path
		}
	}
	if len(metas) == 1 {
		return metas[0].Path
	}
	return ""
}

func materializeImplicitProjectApp(root string, cfg webconfig.WebConfig) (string, error) {
	spec := (*app.RootSpec)(nil)
	if cfg.Root != nil {
		spec = cfg.Root.RootSpec()
	}
	def, _, err := app.BuildRootImporter(spec, root)
	if err != nil {
		return "", err
	}
	body, err := yaml.Marshal(def)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "kitsoki-ghagent-project-app-*")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}

func projectBeatPath(name string) string {
	root, err := repoRoot()
	if err != nil {
		return filepath.Join("internal", "ghagent", "testdata", name)
	}
	return filepath.Join(root, "internal", "ghagent", "testdata", name)
}

func mergeRouteWorld(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func embeddedImportResolver() app.ImportResolver {
	return func(name, _ string, override bool) (string, error) {
		if override {
			if repo := strings.TrimSpace(os.Getenv(kitrepo.EnvVar)); repo != "" {
				candidate := filepath.Join(repo, "stories", name, "app.yaml")
				if _, err := os.Stat(candidate); err != nil {
					return "", fmt.Errorf("%s=%s: story %q not found at %s: %w", kitrepo.EnvVar, repo, name, candidate, err)
				}
				return candidate, nil
			}
			return "", nil
		}
		root, err := basestories.Materialize(context.Background())
		if err != nil {
			return "", err
		}
		return filepath.Join(root, name, "app.yaml"), nil
	}
}

func absPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func relOrBase(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return filepath.Base(path)
}
