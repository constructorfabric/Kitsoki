package extdocs

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/kit"
)

type IndexOptions struct {
	Root string
}

type Index struct {
	Schema     string      `json:"schema"`
	Root       string      `json:"root"`
	Packages   []Package   `json:"packages,omitempty"`
	Stories    []Story     `json:"stories,omitempty"`
	Components []Component `json:"components,omitempty"`
	Docs       []DocNode   `json:"docs,omitempty"`
	Warnings   []string    `json:"warnings,omitempty"`
}

type Package struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Title       string   `json:"title,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Version     string   `json:"version,omitempty"`
	Path        string   `json:"path"`
	Provides    []string `json:"provides,omitempty"`
	Requires    []string `json:"requires,omitempty"`
	Conformance []string `json:"conformance,omitempty"`
	Docs        []string `json:"docs,omitempty"`
}

type Story struct {
	ID             string   `json:"id"`
	PackageID      string   `json:"package_id,omitempty"`
	Title          string   `json:"title,omitempty"`
	Version        string   `json:"version,omitempty"`
	Path           string   `json:"path"`
	WorldKeys      []string `json:"world_keys,omitempty"`
	Intents        []string `json:"intents,omitempty"`
	States         []string `json:"states,omitempty"`
	Agents         []string `json:"agents,omitempty"`
	Toolboxes      []string `json:"toolboxes,omitempty"`
	Providers      []string `json:"providers,omitempty"`
	AgentPlugins   []string `json:"agent_plugins,omitempty"`
	HostInterfaces []string `json:"host_interfaces,omitempty"`
	Exports        []string `json:"exports,omitempty"`
	Exits          []string `json:"exits,omitempty"`
	Imports        []string `json:"imports,omitempty"`
	Prompts        []string `json:"prompts,omitempty"`
	Schemas        []string `json:"schemas,omitempty"`
	Scripts        []string `json:"scripts,omitempty"`
	Flows          []string `json:"flows,omitempty"`
	Docs           []string `json:"docs,omitempty"`
}

type DocNode struct {
	ID            string   `json:"id"`
	Owner         string   `json:"owner"`
	Title         string   `json:"title,omitempty"`
	Path          string   `json:"path,omitempty"`
	GeneratedFrom string   `json:"generated_from,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	Publish       string   `json:"publish"`
	Template      bool     `json:"template,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

func BuildIndex(opts IndexOptions) (*Index, error) {
	root := opts.Root
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	idx := &Index{Schema: "kitsoki.extensions-index/v1", Root: abs}

	kitOwners := map[string]bool{}
	if err := discoverKits(idx, abs, kitOwners); err != nil {
		return nil, err
	}
	if err := discoverStandaloneStories(idx, abs, kitOwners); err != nil {
		return nil, err
	}
	addGeneratedHookComponents(idx, abs)
	sortIndex(idx)
	return idx, nil
}

func discoverKits(idx *Index, root string, kitOwners map[string]bool) error {
	kitsRoot := filepath.Join(root, "kits")
	entries, err := os.ReadDir(kitsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("scan kits: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		kitDir := filepath.Join(kitsRoot, entry.Name())
		manifestPath := filepath.Join(kitDir, kit.ManifestFileName)
		if _, err := os.Stat(manifestPath); err != nil {
			continue
		}
		def, err := kit.LoadDir(kitDir)
		if err != nil {
			return fmt.Errorf("load kit %s: %w", kitDir, err)
		}
		pkgDocs, err := addManifestDocs(idx, kitDir, def.Identity())
		if err != nil {
			return err
		}
		pkg := Package{
			ID:          def.Identity(),
			Kind:        "kit",
			Title:       firstNonEmpty(def.Title, def.Identity()),
			Summary:     def.Summary,
			Version:     def.Version,
			Path:        rel(root, kitDir),
			Provides:    kitProvides(def),
			Requires:    kitRequires(def),
			Conformance: def.Conformance.Flows,
			Docs:        pkgDocs,
		}
		idx.Packages = append(idx.Packages, pkg)
		for _, storyName := range def.Provides.Stories {
			storyDir := def.StoryDir(storyName)
			storyID := "story:" + def.Identity() + "/" + storyName
			kitOwners[filepath.Clean(storyDir)] = true
			story, err := storyInventory(idx, root, storyDir, storyID, def.Identity())
			if err != nil {
				return err
			}
			idx.Stories = append(idx.Stories, story)
		}
	}
	return nil
}

func discoverStandaloneStories(idx *Index, root string, kitOwners map[string]bool) error {
	storiesRoot := filepath.Join(root, "stories")
	entries, err := os.ReadDir(storiesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("scan stories: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		storyDir := filepath.Join(storiesRoot, entry.Name())
		if kitOwners[filepath.Clean(storyDir)] {
			continue
		}
		if _, err := os.Stat(filepath.Join(storyDir, "app.yaml")); err != nil {
			continue
		}
		storyID := "story:@local/" + entry.Name()
		story, err := storyInventory(idx, root, storyDir, storyID, "")
		if err != nil {
			return err
		}
		idx.Stories = append(idx.Stories, story)
	}
	return nil
}

func storyInventory(idx *Index, root, storyDir, storyID, packageID string) (Story, error) {
	def, err := app.Load(filepath.Join(storyDir, "app.yaml"))
	if err != nil {
		return Story{}, fmt.Errorf("load story %s: %w", storyDir, err)
	}
	docIDs, err := addManifestDocsForStory(idx, storyDir, storyID)
	if err != nil {
		return Story{}, err
	}
	story := Story{
		ID:             storyID,
		PackageID:      packageID,
		Title:          firstNonEmpty(def.App.Title, def.App.ID),
		Version:        def.App.Version,
		Path:           rel(root, storyDir),
		WorldKeys:      mapKeys(def.World),
		Intents:        mapKeys(def.Intents),
		States:         mapKeys(def.States),
		Agents:         mapKeys(def.Agents),
		Toolboxes:      mapKeys(def.Toolboxes),
		Providers:      mapKeys(def.Providers),
		AgentPlugins:   mapKeys(def.AgentPlugins),
		HostInterfaces: mapKeys(def.HostInterfaces),
		Exports:        exports(def),
		Exits:          mapKeys(def.Exits),
		Imports:        mapKeys(def.Imports),
		Prompts:        globRel(storyDir, "prompts", "*.md"),
		Schemas:        globRel(storyDir, "schemas", "*.json"),
		Scripts:        append(globRel(storyDir, "scripts", "*.star"), globRel(storyDir, "scripts", "*.star.yaml")...),
		Flows:          globRel(storyDir, "flows", "*.yaml"),
		Docs:           docIDs,
	}
	addGeneratedStoryComponents(idx, story, def)
	return story, nil
}

func addManifestDocs(idx *Index, dir, owner string) ([]string, error) {
	return addManifestDocsForIndex(idx, dir, owner)
}

func addManifestDocsForStory(idx *Index, dir, owner string) ([]string, error) {
	return addManifestDocsForIndex(idx, dir, owner)
}

func addManifestDocsForIndex(idx *Index, dir, owner string) ([]string, error) {
	manifestPath := filepath.Join(dir, ManifestFileName)
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	m, err := LoadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range m.Docs {
		id := owner + ":" + d.ID
		ids = append(ids, id)
		if idx != nil {
			idx.Docs = append(idx.Docs, docNode(id, owner, d))
		}
	}
	for _, c := range m.Components {
		componentID := c.Kind + ":" + c.ID
		if idx != nil {
			node := c
			node.Owner = owner
			if node.Title == "" {
				node.Title = firstNonEmpty(firstDocTitle(c.Docs), c.ID)
			}
			if node.Summary == "" && len(c.Docs) > 0 {
				node.Summary = "Source-owned component docs from " + ManifestFileName + "."
			}
			if node.Path == "" {
				node.Path = firstDocPath(c.Docs)
			}
			if node.GeneratedFrom == "" {
				node.GeneratedFrom = firstDocGeneratedFrom(c.Docs)
			}
			if node.Publish == "" {
				node.Publish = firstNonEmpty(firstDocPublish(c.Docs), "true")
			}
			addComponent(idx, node)
		}
		for _, d := range c.Docs {
			id := componentID + ":" + d.ID
			ids = append(ids, id)
			if idx != nil {
				idx.Docs = append(idx.Docs, docNode(id, componentID, d))
			}
		}
	}
	return ids, nil
}

func addGeneratedStoryComponents(idx *Index, story Story, def *app.AppDef) {
	if idx == nil {
		return
	}
	appPath := filepath.ToSlash(filepath.Join(story.Path, "app.yaml"))
	addComponent(idx, Component{
		Kind:          "story",
		ID:            story.ID,
		Owner:         firstNonEmpty(story.PackageID, story.ID),
		Title:         firstNonEmpty(story.Title, story.ID) + " story contract",
		Summary:       fmt.Sprintf("%d states, %d intents, %d world keys, and %d no-LLM flow fixture(s).", len(story.States), len(story.Intents), len(story.WorldKeys), len(story.Flows)),
		Path:          story.Path,
		GeneratedFrom: appPath,
		Publish:       "full",
		Tags:          []string{"story", "contract"},
		Facts: facts(
			"states", fmt.Sprint(len(story.States)),
			"intents", fmt.Sprint(len(story.Intents)),
			"flows", fmt.Sprint(len(story.Flows)),
			"package", story.PackageID,
		),
	})

	for _, name := range story.HostInterfaces {
		iface := def.HostInterfaces[name]
		summary := "Host interface declared by this story."
		if iface != nil && strings.TrimSpace(iface.Description) != "" {
			summary = strings.TrimSpace(iface.Description)
		}
		addComponent(idx, Component{
			Kind:          "host-interface",
			ID:            storyComponentID(story.ID, "host_interfaces", name),
			Owner:         story.ID,
			Title:         name + " host interface",
			Summary:       summary,
			Path:          story.Path,
			GeneratedFrom: appPath + "#host_interfaces." + name,
			Publish:       "full",
			Tags:          []string{"host", "interface"},
			Uses:          []string{story.ID},
			Facts: facts(
				"default", hostInterfaceDefault(iface),
				"operations", strings.Join(hostInterfaceOps(iface), ", "),
			),
		})
	}

	for _, name := range story.Agents {
		decl := def.Agents[name]
		addComponent(idx, Component{
			Kind:          "agent-profile",
			ID:            storyComponentID(story.ID, "agents", name),
			Owner:         story.ID,
			Title:         "agents." + name,
			Summary:       "Agent persona and runtime posture. Prompt bodies are summarized by policy and are not emitted in this index.",
			Path:          story.Path,
			GeneratedFrom: appPath + "#agents." + name,
			Publish:       "summary",
			Tags:          []string{"agent", "profile", "redacted"},
			Uses:          []string{story.ID},
			Facts:         agentFacts(decl),
		})
	}

	for _, name := range story.Providers {
		decl := def.Providers[name]
		addComponent(idx, Component{
			Kind:          "provider-profile",
			ID:            storyComponentID(story.ID, "providers", name),
			Owner:         story.ID,
			Title:         "providers." + name,
			Summary:       "Provider backend defaults and environment key names. Environment values are intentionally omitted.",
			Path:          story.Path,
			GeneratedFrom: appPath + "#providers." + name,
			Publish:       "summary",
			Tags:          []string{"provider", "profile", "redacted"},
			Uses:          []string{story.ID},
			Facts:         providerFacts(decl),
		})
	}

	for _, name := range story.Toolboxes {
		decl := def.Toolboxes[name]
		addComponent(idx, Component{
			Kind:          "toolbox",
			ID:            storyComponentID(story.ID, "toolboxes", name),
			Owner:         story.ID,
			Title:         "toolboxes." + name,
			Summary:       "Reusable agent tool surface declared by the story.",
			Path:          story.Path,
			GeneratedFrom: appPath + "#toolboxes." + name,
			Publish:       "summary",
			Tags:          []string{"agent", "toolbox"},
			Uses:          []string{story.ID},
			Facts:         toolboxFacts(decl),
		})
	}

	for _, name := range story.AgentPlugins {
		decl := def.AgentPlugins[name]
		addComponent(idx, Component{
			Kind:          "agent-plugin",
			ID:            storyComponentID(story.ID, "agent_plugins", name),
			Owner:         story.ID,
			Title:         "agent_plugins." + name,
			Summary:       "Agent plugin transport and redacted connection surface. Env and header values are intentionally omitted.",
			Path:          story.Path,
			GeneratedFrom: appPath + "#agent_plugins." + name,
			Publish:       "summary",
			Tags:          []string{"agent", "plugin", "redacted"},
			Uses:          []string{story.ID},
			Facts:         agentPluginFacts(decl),
		})
	}

	for _, name := range story.Imports {
		addComponent(idx, Component{
			Kind:          "story-import",
			ID:            storyComponentID(story.ID, "imports", name),
			Owner:         story.ID,
			Title:         "imports." + name,
			Summary:       "Imported child story contract and host-binding surface.",
			Path:          story.Path,
			GeneratedFrom: appPath + "#imports." + name,
			Publish:       "full",
			Tags:          []string{"story", "import"},
			Uses:          []string{story.ID},
		})
	}

	for _, script := range story.Scripts {
		addComponent(idx, Component{
			Kind:          "starlark-script",
			ID:            storyAssetComponentID(story.ID, script),
			Owner:         story.ID,
			Title:         filepath.Base(script),
			Summary:       "Starlark script or sidecar file indexed from the story. The page links the file identity without executing it.",
			Path:          filepath.ToSlash(filepath.Join(story.Path, script)),
			GeneratedFrom: filepath.ToSlash(filepath.Join(story.Path, script)),
			Publish:       "true",
			Tags:          []string{"starlark", "script"},
			Uses:          []string{story.ID},
			Facts: facts(
				"file", script,
				"story", story.ID,
			),
		})
	}

	for _, schema := range story.Schemas {
		addComponent(idx, Component{
			Kind:          "schema",
			ID:            storyAssetComponentID(story.ID, schema),
			Owner:         story.ID,
			Title:         filepath.Base(schema),
			Summary:       "JSON schema fixture or acceptance contract indexed from the story.",
			Path:          filepath.ToSlash(filepath.Join(story.Path, schema)),
			GeneratedFrom: filepath.ToSlash(filepath.Join(story.Path, schema)),
			Publish:       "true",
			Tags:          []string{"schema", "contract"},
			Uses:          []string{story.ID},
			Facts: facts(
				"file", schema,
				"story", story.ID,
			),
		})
	}

	for _, prompt := range story.Prompts {
		addComponent(idx, Component{
			Kind:          "prompt",
			ID:            storyAssetComponentID(story.ID, prompt),
			Owner:         story.ID,
			Title:         filepath.Base(prompt),
			Summary:       "Prompt identity and usage surface. Raw prompt text is hidden unless a docs sidecar explicitly opts in.",
			Path:          filepath.ToSlash(filepath.Join(story.Path, prompt)),
			GeneratedFrom: filepath.ToSlash(filepath.Join(story.Path, prompt)),
			Publish:       "summary",
			Tags:          []string{"prompt", "redacted"},
			Uses:          []string{story.ID},
			Facts: facts(
				"file", prompt,
				"policy", "summary only",
			),
		})
	}
}

func addGeneratedHookComponents(idx *Index, root string) {
	if idx == nil {
		return
	}
	source := filepath.Join(root, "cmd", "kitsoki", "hook.go")
	if _, err := os.Stat(source); err != nil {
		return
	}
	addComponent(idx, Component{
		Kind:          "hook",
		ID:            "hook.claude.UserPromptSubmit",
		Owner:         "kitsoki",
		Title:         "Claude UserPromptSubmit hook",
		Summary:       "Prompt-submit hook installed for Claude Code. The hook fails open, blocks only clean no-LLM intercepts, and documents no-hook behavior for Codex and Copilot.",
		Path:          "cmd/kitsoki/hook.go",
		GeneratedFrom: "cmd/kitsoki/hook.go#mergeClaudeHook",
		Publish:       "summary",
		Tags:          []string{"hook", "agent", "claude", "redacted"},
		Uses:          []string{"kitsoki hook install --agent claude", "kitsoki hook run --agent claude"},
		Facts: facts(
			"event", "UserPromptSubmit",
			"command", "kitsoki hook run --agent claude",
			"timeout", "1200s",
			"Claude Code", "supported",
			"Codex", "no pre-model hook",
			"Copilot", "no blocking prompt hook",
			"failure policy", "fail open",
		),
	})
}

func addComponent(idx *Index, c Component) {
	if c.Publish == "" {
		c.Publish = "true"
	}
	if c.Title == "" {
		c.Title = c.ID
	}
	idx.Components = append(idx.Components, c)
}

func storyComponentID(storyID, section, name string) string {
	return storyID + "#" + section + "." + name
}

func storyAssetComponentID(storyID, path string) string {
	return storyID + "#" + filepath.ToSlash(path)
}

func facts(pairs ...string) []ComponentFact {
	var out []ComponentFact
	for i := 0; i+1 < len(pairs); i += 2 {
		label := strings.TrimSpace(pairs[i])
		value := strings.TrimSpace(pairs[i+1])
		if label == "" || value == "" {
			continue
		}
		out = append(out, ComponentFact{Label: label, Value: value})
	}
	return out
}

func agentFacts(decl *app.AgentDecl) []ComponentFact {
	if decl == nil {
		return nil
	}
	prompt := "inline or resolved prompt summarized"
	if decl.SystemPrompt == "" {
		prompt = "not declared"
	}
	return facts(
		"effect", string(decl.Effect),
		"provider", decl.Provider,
		"harness", decl.Harness,
		"model", decl.Model,
		"effort", decl.Effort,
		"toolbox", decl.Toolbox,
		"tools", strings.Join(decl.Tools, ", "),
		"tools added", strings.Join(decl.ToolsAdd, ", "),
		"tools removed", strings.Join(decl.ToolsRemove, ", "),
		"mcp tools", strings.Join(agentMCPTools(decl), ", "),
		"permissions", agentPermissionMode(decl),
		"prompt", prompt,
	)
}

func providerFacts(decl *app.ProviderDecl) []ComponentFact {
	if decl == nil {
		return nil
	}
	envKeys := mapKeys(decl.Env)
	return facts(
		"backend", decl.Backend,
		"model", decl.Model,
		"effort", decl.Effort,
		"env keys", strings.Join(envKeys, ", "),
		"env values", "hidden",
	)
}

func toolboxFacts(decl *app.ToolboxDecl) []ComponentFact {
	if decl == nil {
		return nil
	}
	return facts(
		"effect", string(decl.Effect),
		"tools", strings.Join(decl.Tools, ", "),
	)
}

func agentPluginFacts(decl *app.AgentPluginDecl) []ComponentFact {
	if decl == nil {
		return nil
	}
	endpoint := ""
	if decl.Endpoint != "" {
		endpoint = "configured"
	}
	command := ""
	if decl.Command != "" {
		command = "configured"
	}
	return facts(
		"plugin", decl.Plugin,
		"command", command,
		"endpoint", endpoint,
		"tool", decl.Tool,
		"model", decl.Model,
		"env keys", strings.Join(mapKeys(decl.Env), ", "),
		"header keys", strings.Join(mapKeys(decl.Headers), ", "),
		"api key env", decl.APIKeyEnv,
	)
}

func hostInterfaceDefault(iface *app.HostInterfaceDef) string {
	if iface == nil {
		return ""
	}
	return iface.Default
}

func hostInterfaceOps(iface *app.HostInterfaceDef) []string {
	if iface == nil {
		return nil
	}
	return mapKeys(iface.Operations)
}

func agentMCPTools(decl *app.AgentDecl) []string {
	if decl == nil || decl.MCP == nil {
		return nil
	}
	return decl.MCP.Tools
}

func agentPermissionMode(decl *app.AgentDecl) string {
	if decl == nil || decl.Permissions == nil {
		return ""
	}
	return decl.Permissions.Mode
}

func firstDocTitle(docs []DocEntry) string {
	for _, d := range docs {
		if strings.TrimSpace(d.Title) != "" {
			return d.Title
		}
	}
	return ""
}

func firstDocPath(docs []DocEntry) string {
	for _, d := range docs {
		if strings.TrimSpace(d.Path) != "" {
			return d.Path
		}
	}
	return ""
}

func firstDocGeneratedFrom(docs []DocEntry) string {
	for _, d := range docs {
		if strings.TrimSpace(d.GeneratedFrom) != "" {
			return d.GeneratedFrom
		}
	}
	return ""
}

func firstDocPublish(docs []DocEntry) string {
	for _, d := range docs {
		if strings.TrimSpace(d.Publish) != "" {
			return d.Publish
		}
	}
	return ""
}

func docNode(id, owner string, d DocEntry) DocNode {
	return DocNode{ID: id, Owner: owner, Title: d.Title, Path: d.Path, GeneratedFrom: d.GeneratedFrom, Kind: d.Kind, Publish: d.Publish, Template: d.Template, Tags: d.Tags}
}

func kitProvides(def *kit.Def) []string {
	var out []string
	for _, s := range def.Provides.Stories {
		out = append(out, "story:"+s)
	}
	for _, s := range def.Provides.Schemas {
		out = append(out, "schema:"+s)
	}
	for _, s := range def.Provides.Interfaces {
		out = append(out, "host-interface:"+s)
	}
	for _, ui := range def.Provides.UI {
		out = append(out, "ui:"+ui.ID)
	}
	if def.Provides.Onboarding != "" {
		out = append(out, "onboarding:"+def.Provides.Onboarding)
	}
	sort.Strings(out)
	return out
}

func kitRequires(def *kit.Def) []string {
	var out []string
	if def.Requires.Kitsoki != "" {
		out = append(out, "kitsoki "+def.Requires.Kitsoki)
	}
	for _, dep := range def.Extends {
		out = append(out, "extends "+dep.Kit+constraintSuffix(dep.Constraint))
	}
	for _, dep := range def.Composes {
		out = append(out, "composes "+dep.Kit+constraintSuffix(dep.Constraint))
	}
	sort.Strings(out)
	return out
}

func constraintSuffix(c string) string {
	if c == "" {
		return ""
	}
	return " " + c
}

func exports(def *app.AppDef) []string {
	if def.Exports == nil {
		return nil
	}
	out := append([]string(nil), def.Exports.Intents...)
	sort.Strings(out)
	return out
}

func globRel(root, dir, pattern string) []string {
	base := filepath.Join(root, dir)
	matches, _ := filepath.Glob(filepath.Join(base, pattern))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || info.IsDir() {
			continue
		}
		out = append(out, filepath.ToSlash(filepath.Join(dir, filepath.Base(m))))
	}
	sort.Strings(out)
	return out
}

func mapKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(r)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func sortIndex(idx *Index) {
	sort.Slice(idx.Packages, func(i, j int) bool { return idx.Packages[i].ID < idx.Packages[j].ID })
	sort.Slice(idx.Stories, func(i, j int) bool { return idx.Stories[i].ID < idx.Stories[j].ID })
	sort.Slice(idx.Components, func(i, j int) bool {
		return idx.Components[i].Kind+idx.Components[i].ID < idx.Components[j].Kind+idx.Components[j].ID
	})
	sort.Slice(idx.Docs, func(i, j int) bool { return idx.Docs[i].ID < idx.Docs[j].ID })
}

func WriteJSON(idx *Index, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func WalkDocsManifests(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".artifacts" || name == ".context" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == ManifestFileName {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}
