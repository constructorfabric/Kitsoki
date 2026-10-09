package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	goyaml "github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/webconfig"
)

// materializeCmd graduates the implicit project root (rung 0/1) to a real
// .kitsoki/stories/<slug>/app.yaml file (rung 2). It synthesizes the root from
// .kitsoki.yaml, slugs from --name or the repo basename, refuses to overwrite an
// existing file, emits a normal dev-story instance (one import + host_bindings +
// world + a provenance header), and re-loads its own output to prove the
// graduated root is loadable — removing the partial file if it is not. See
// docs/stories/imports.md "The blank root that grows".
func materializeCmd() *cobra.Command {
	var nameFlag string
	var configFlag string

	cmd := &cobra.Command{
		Use:   "materialize",
		Short: "Graduate the implicit dev-story root to a real .kitsoki/stories/<slug>/app.yaml",
		Long: `Materialize writes the implicit project root — the dev-story instance the
loader synthesizes from .kitsoki.yaml 'root:' — to a real, hand-editable
.kitsoki/stories/<slug>/app.yaml file (rung 2 of the blank-root ladder).

The emitted file is a normal dev-story instance exactly like .kitsoki/stories/kitsoki-dev:
one import of dev-story, the host_bindings + world from your .kitsoki.yaml
overrides, and a provenance header. Materialize refuses to overwrite an existing
file (edit it directly once graduated) and re-loads its own output, aborting and
removing the partial file if it does not validate — so a graduated root is
guaranteed loadable.

Examples:
  kitsoki materialize                 # slug = repo dir basename
  kitsoki materialize --name myproj   # slug = myproj`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := configFlag
			if configPath == "" {
				configPath = webconfig.DefaultConfigFile
			}
			repoRoot, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}
			outPath, err := runMaterialize(repoRoot, configPath, nameFlag)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "materialized %s\n", outPath)
			fmt.Fprintf(cmd.OutOrStdout(), "the implicit root is now a real file — edit it directly; run with `kitsoki run %s`\n", outPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&nameFlag, "name", "", "story slug (default: repo dir basename)")
	cmd.Flags().StringVar(&configFlag, "config", "", "path to .kitsoki.yaml (default: ./.kitsoki.yaml)")
	return cmd
}

// runMaterialize is the testable core of the materialize command: load the
// config, resolve the slug, synthesize-then-emit, refuse to overwrite, write,
// and re-load-and-validate (removing the partial on failure). repoRoot is both
// the @kitsoki/dev-story resolution root and the .kitsoki/stories/<slug>/ write
// root. Returns the written path on success.
func runMaterialize(repoRoot, configPath, nameFlag string) (string, error) {
	webCfg, err := webconfig.Load(configPath)
	if err != nil {
		return "", err
	}

	slug := nameFlag
	if slug == "" {
		abs, absErr := filepath.Abs(repoRoot)
		if absErr != nil {
			abs = repoRoot
		}
		slug = filepath.Base(abs)
	}

	// Synthesize first — never emit an un-loadable tree. A bad root: block fails
	// here before any file is touched.
	rootSpec := webCfg.Root.RootSpec()
	if _, synthErr := app.SynthesizeRootWithResolver(rootSpec, repoRoot, buildImportResolver()); synthErr != nil {
		return "", fmt.Errorf("synthesize implicit root: %w", synthErr)
	}

	outPath := filepath.Join(repoRoot, ".kitsoki", "stories", slug, "app.yaml")
	if _, statErr := os.Stat(outPath); statErr == nil {
		return "", fmt.Errorf("%s already exists; this root is already materialized — edit it directly", outPath)
	}

	yamlBytes, err := emitRootYAML(rootSpec, slug, repoRoot, buildImportResolver())
	if err != nil {
		return "", fmt.Errorf("emit app.yaml: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("create stories dir: %w", err)
	}
	if err := os.WriteFile(outPath, yamlBytes, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", outPath, err)
	}

	// Re-load the emitted file: a graduated root is GUARANTEED loadable. If it
	// does not validate, remove the partial and abort.
	if _, loadErr := app.Load(outPath); loadErr != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("materialized %s did not validate (removed): %w", outPath, loadErr)
	}
	return outPath, nil
}

// rootYAMLDoc is the emit shape for a materialized dev-story instance. It is a
// strict subset of the app.yaml schema (the same keys kitsoki-dev declares) so
// app.Load round-trips it. Field order here is the on-disk order.
type rootYAMLDoc struct {
	App          rootYAMLApp               `yaml:"app"`
	AgentPlugins map[string]rootYAMLAgent  `yaml:"agent_plugins"`
	Routing      rootYAMLRouting           `yaml:"routing"`
	Hosts        []string                  `yaml:"hosts"`
	Imports      map[string]rootYAMLImport `yaml:"imports"`
	World        map[string]rootYAMLVar    `yaml:"world,omitempty"`
	Intents      map[string]rootYAMLIntent `yaml:"intents,omitempty"`
	Root         string                    `yaml:"root"`
}

type rootYAMLVar struct {
	Type    string `yaml:"type"`
	Default any    `yaml:"default"`
}

type rootYAMLApp struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	Title   string `yaml:"title,omitempty"`
}

type rootYAMLAgent struct {
	Plugin  string `yaml:"plugin"`
	Model   string `yaml:"model,omitempty"`
	Grammar bool   `yaml:"grammar,omitempty"`
}

type rootYAMLRouting struct {
	Embedding rootYAMLEmbedding `yaml:"embedding"`
}

type rootYAMLEmbedding struct {
	Model string `yaml:"model"`
}

type rootYAMLImport struct {
	Source       string            `yaml:"source"`
	Entry        string            `yaml:"entry"`
	Hosts        string            `yaml:"hosts,omitempty"`
	HostBindings map[string]string `yaml:"host_bindings,omitempty"`
	WorldIn      map[string]string `yaml:"world_in,omitempty"`
}

type rootYAMLIntent struct {
	Synonyms []string `yaml:"synonyms,omitempty"`
}

// emitRootYAML serializes the synthesized importer for the given spec to YAML in
// the kitsoki-dev instance shape, prefixed with a provenance header. It reuses
// app.BuildRootImporter so the emitted file is byte-faithful to what the loader
// synthesizes — app.Load(emit(spec)) deep-equals app.SynthesizeRoot(spec).
func emitRootYAML(spec *app.RootSpec, slug, repoRoot string, resolver app.ImportResolver) ([]byte, error) {
	imp, absRoot, err := app.BuildRootImporterWithResolver(spec, repoRoot, resolver)
	if err != nil {
		return nil, err
	}

	importDef := imp.Imports[app.RootAlias]

	var hostBindings map[string]string
	if len(importDef.HostBindings) > 0 {
		hostBindings = make(map[string]string, len(importDef.HostBindings))
		for k, v := range importDef.HostBindings {
			switch {
			case v.Script != "":
				hostBindings[k] = materializedScriptPath(v.Script, absRoot, slug)
			default:
				hostBindings[k] = v.Handler
			}
		}
	}

	doc := rootYAMLDoc{
		App: rootYAMLApp{
			ID:      slug,
			Version: imp.App.Version,
			Title:   fmt.Sprintf("%s — dev-story instance (materialized implicit root)", slug),
		},
		AgentPlugins: map[string]rootYAMLAgent{
			"agent.local_llm": {Plugin: "builtin.local_llm", Model: "qwen2.5-1.5b-instruct", Grammar: true},
		},
		Routing: rootYAMLRouting{Embedding: rootYAMLEmbedding{Model: "nomic-embed-text-v1.5"}},
		Hosts:   imp.Hosts,
		Imports: map[string]rootYAMLImport{
			app.RootAlias: {
				Source:       importDef.Source,
				Entry:        importDef.Entry,
				Hosts:        importDef.Hosts,
				HostBindings: hostBindings,
				WorldIn:      importDef.WorldIn,
			},
		},
		Root: app.RootAlias,
	}
	if len(imp.World) > 0 {
		doc.World = make(map[string]rootYAMLVar, len(imp.World))
		for k, v := range imp.World {
			doc.World[k] = rootYAMLVar{Type: v.Type, Default: v.Default}
		}
	}
	if len(imp.Intents) > 0 {
		doc.Intents = make(map[string]rootYAMLIntent, len(imp.Intents))
		for k, v := range imp.Intents {
			doc.Intents[k] = rootYAMLIntent{Synonyms: v.Synonyms}
		}
	}

	body, err := goyaml.Marshal(doc)
	if err != nil {
		return nil, err
	}

	header := fmt.Sprintf("# materialized from .kitsoki.yaml root: on %s\n"+
		"# This is a normal dev-story instance — edit it directly. See\n"+
		"# docs/stories/imports.md \"The blank root that grows\".\n",
		time.Now().UTC().Format(time.RFC3339))
	return append([]byte(header), body...), nil
}

func materializedScriptPath(script, repoRoot, slug string) string {
	if filepath.IsAbs(script) {
		return filepath.Clean(script)
	}
	absScript := filepath.Join(repoRoot, script)
	appDir := filepath.Join(repoRoot, ".kitsoki", "stories", slug)
	rel, err := filepath.Rel(appDir, absScript)
	if err != nil {
		return filepath.Clean(script)
	}
	return filepath.Clean(rel)
}
