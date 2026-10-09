package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	goyaml "github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"kitsoki/internal/capsule"
	"kitsoki/internal/capsule/control"
	capsuleproject "kitsoki/internal/capsule/project"
)

func capsuleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capsule",
		Short: "Open, verify, and close hermetic development capsules",
	}
	cmd.AddCommand(capsuleListCmd())
	cmd.AddCommand(capsuleOpenCmd())
	cmd.AddCommand(capsuleVerifyCmd())
	cmd.AddCommand(capsuleCloseCmd())
	cmd.AddCommand(capsuleWorkspaceCmd())
	cmd.AddCommand(capsuleEnvCmd())
	cmd.AddCommand(capsuleSyncCmd())
	cmd.AddCommand(capsuleCICmd())
	cmd.AddCommand(capsuleCleanupCmd())
	cmd.AddCommand(capsuleMCPCommand())
	cmd.AddCommand(capsuleWorkerCmd())
	return cmd
}

type capsuleListEntry struct {
	Ref         string `json:"ref"`
	Kind        string `json:"kind"`
	Title       string `json:"title,omitempty"`
	Path        string `json:"path"`
	Executor    string `json:"executor"`
	Environment string `json:"environment,omitempty"`
	Project     string `json:"project,omitempty"`
	Bug         string `json:"bug,omitempty"`
	Scenario    string `json:"scenario,omitempty"`
	LocalOnly   bool   `json:"local_only,omitempty"`
}

const repoHistoryCapsuleMaterializer = "internal/capsule/pinned-git"

func capsuleListCmd() *cobra.Command {
	var kind string
	var jsonOut bool
	var markdownOut bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List core and repo-history capsules",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := collectCapsuleList(kind)
			if err != nil {
				return err
			}
			if jsonOut && markdownOut {
				return fmt.Errorf("--json and --markdown are mutually exclusive")
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"ok":       true,
					"kind":     normalizeCapsuleListKind(kind),
					"count":    len(entries),
					"capsules": entries,
				})
			}
			if markdownOut {
				writeCapsuleListMarkdown(cmd.OutOrStdout(), normalizeCapsuleListKind(kind), entries)
				return nil
			}
			out := cmd.OutOrStdout()
			if len(entries) == 0 {
				fmt.Fprintln(out, "capsules: none")
				return nil
			}
			fmt.Fprintln(out, "capsules:")
			for _, entry := range entries {
				fmt.Fprintf(out, "  %s [%s]\n", entry.Ref, entry.Kind)
				if entry.Title != "" {
					fmt.Fprintf(out, "    %s\n", entry.Title)
				}
				fmt.Fprintf(out, "    path: %s\n", entry.Path)
				fmt.Fprintf(out, "    executor: %s\n", entry.Executor)
				if entry.Environment != "" {
					fmt.Fprintf(out, "    environment: %s\n", entry.Environment)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "all", "capsule kind to list: all, core, repo-history")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print catalog JSON")
	cmd.Flags().BoolVar(&markdownOut, "markdown", false, "print catalog Markdown")
	return cmd
}

func capsuleOpenCmd() *cobra.Command {
	var dest string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "open <name|path>",
		Short: "Materialize a capsule into a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := capsuleOpenManaged(cmd.Context(), args[0], dest)
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(res.Manifest)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "capsule %s\n", res.Manifest.CapsuleName)
			fmt.Fprintf(cmd.OutOrStdout(), "workspace: %s\n", res.Manifest.Workspace)
			fmt.Fprintf(cmd.OutOrStdout(), "manifest: %s/%s\n", res.Manifest.Workspace, capsule.ManifestFile)
			fmt.Fprintf(cmd.OutOrStdout(), "tree_digest: sha256:%s\n", res.Manifest.TreeDigest)
			return nil
		},
	}
	cmd.Flags().StringVar(&dest, "dest", "", "destination directory (default: temp directory)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print manifest JSON")
	return cmd
}

func capsuleOpenManaged(ctx context.Context, ref, dest string) (capsule.OpenResult, error) {
	if shouldUseLegacyCapsulePath(ref) {
		return capsule.Open(ctx, ref, capsule.OpenOptions{Dest: dest})
	}
	projectRoot := capsuleListRepoRoot()
	definitions := control.FileDefinitionStore{ProjectRoot: projectRoot}
	def, err := definitions.Get(ctx, ref)
	if err != nil {
		return capsule.Open(ctx, ref, capsule.OpenOptions{Dest: dest})
	}
	if def.Source.Kind != control.SourceSynthetic {
		return capsule.OpenResult{}, fmt.Errorf("capsule open: definition %q uses provider %q; use capsule workspace create for managed non-synthetic workspaces", def.ID, def.Source.Kind)
	}
	workspace, err := capsuleManagedOpenDest(def.ID, dest)
	if err != nil {
		return capsule.OpenResult{}, err
	}
	root := filepath.Dir(workspace)
	id := filepath.Base(workspace)
	if !validCapsuleManagedInstanceID(id) {
		return capsule.Open(ctx, ref, capsule.OpenOptions{Dest: dest})
	}
	manager, err := capsuleproject.Open(projectRoot, nil)
	if err != nil {
		return capsule.OpenResult{}, err
	}
	manager.Instances = control.NewMemoryInstanceStore()
	manager.Grant.WorkspaceRoots = []string{root}
	handle, err := manager.Create(ctx, control.CreateRequest{ID: id, DefinitionID: def.ID, Owner: "capsule-cli", Provider: string(control.SourceSynthetic)})
	if err != nil {
		return capsule.OpenResult{}, err
	}
	path, err := manager.WorkspacePath(ctx, handle)
	if err != nil {
		return capsule.OpenResult{}, err
	}
	manifest, err := capsule.ReadManifest(path)
	if err != nil {
		return capsule.OpenResult{}, err
	}
	spec, specPath, err := capsule.Load(manifest.SpecPath)
	if err != nil {
		return capsule.OpenResult{}, err
	}
	return capsule.OpenResult{Spec: spec, SpecPath: specPath, Manifest: manifest}, nil
}

func shouldUseLegacyCapsulePath(ref string) bool {
	ref = strings.TrimSpace(ref)
	return ref == "" || strings.HasSuffix(ref, ".yaml") || strings.Contains(ref, string(filepath.Separator))
}

func capsuleManagedOpenDest(definitionID, dest string) (string, error) {
	if strings.TrimSpace(dest) != "" {
		return filepath.Abs(dest)
	}
	workspace, err := os.MkdirTemp("", "kitsoki-capsule-"+safeCapsuleInstanceID(definitionID)+"-")
	if err != nil {
		return "", fmt.Errorf("capsule: create temp workspace: %w", err)
	}
	if err := os.RemoveAll(workspace); err != nil {
		return "", err
	}
	return workspace, nil
}

func safeCapsuleInstanceID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "workspace"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if out == "" {
		return "workspace"
	}
	return out
}

func validCapsuleManagedInstanceID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for i, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if !ok {
			return false
		}
		if i == 0 && !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func collectCapsuleList(kind string) ([]capsuleListEntry, error) {
	normalized := normalizeCapsuleListKind(kind)
	if normalized == "" {
		return nil, fmt.Errorf("unknown capsule kind %q; use all, core, or repo-history", kind)
	}
	root := capsuleListRepoRoot()
	var entries []capsuleListEntry
	if normalized == "all" || normalized == "core" {
		core, err := collectCoreCapsules(root)
		if err != nil {
			return nil, err
		}
		entries = append(entries, core...)
	}
	if normalized == "all" || normalized == "repo-history" {
		history, err := collectRepoHistoryCapsules(root)
		if err != nil {
			return nil, err
		}
		entries = append(entries, history...)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Ref < entries[j].Ref
	})
	return entries, nil
}

func writeCapsuleListMarkdown(w io.Writer, kind string, entries []capsuleListEntry) {
	title := "Capsules"
	if kind == "repo-history" {
		title = "Repo History Capsules"
	} else if kind == "core" {
		title = "Core Capsules"
	}
	fmt.Fprintf(w, "# %s\n\n", title)
	fmt.Fprintf(w, "Capsules: **%d**\n\n", len(entries))
	fmt.Fprintln(w, "| Capsule | Kind | Executor | Environment | Path |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, entry := range entries {
		fmt.Fprintf(w, "| `%s` | %s | %s | %s | `%s` |\n", entry.Ref, entry.Kind, entry.Executor, entry.Environment, entry.Path)
	}
}

func normalizeCapsuleListKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return "all"
	case "core", "fixture", "fixtures", "synthetic":
		return "core"
	case "repo-history", "repo_history", "history", "bugfix", "bugfix-history":
		return "repo-history"
	default:
		return ""
	}
}

func collectCoreCapsules(root string) ([]capsuleListEntry, error) {
	manager, err := capsuleproject.Open(root, nil)
	if err != nil {
		return nil, err
	}
	definitions, err := manager.Definitions.List(context.Background())
	if err != nil {
		return nil, err
	}
	var entries []capsuleListEntry
	for _, def := range definitions {
		path := def.LegacyPath
		scenario := ""
		if def.Source.Kind == control.SourceSynthetic {
			spec, _, err := capsule.Load(path)
			if err != nil {
				return nil, err
			}
			if spec.Scenario != nil {
				scenario = strings.TrimSpace(spec.Scenario.Kind)
			}
		}
		entries = append(entries, capsuleListEntry{
			Ref:      def.ID,
			Kind:     "core",
			Path:     relCapsulePath(root, path),
			Executor: capsuleListExecutor(def),
			Scenario: scenario,
		})
	}
	return entries, nil
}

func capsuleListExecutor(def control.Definition) string {
	switch def.Source.Kind {
	case control.SourceSynthetic:
		return "internal/capsule"
	case control.SourceDevWorkspaceScript:
		return string(control.SourceDevWorkspaceScript)
	case control.SourceSelf, control.SourcePinned:
		return "git"
	default:
		return string(def.Source.Kind)
	}
}

func collectRepoHistoryCapsules(root string) ([]capsuleListEntry, error) {
	paths, err := filepath.Glob(filepath.Join(root, "tools", "bugfix-bakeoff", "external", "projects", "*", "manifest.yaml"))
	if err != nil {
		return nil, err
	}
	var entries []capsuleListEntry
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read repo-history manifest %s: %w", path, err)
		}
		var manifest struct {
			Project struct {
				ID        string `yaml:"id"`
				Title     string `yaml:"title"`
				LocalOnly bool   `yaml:"local_only"`
			} `yaml:"project"`
			Bugs []struct {
				ID    string `yaml:"id"`
				Title string `yaml:"title"`
			} `yaml:"bugs"`
		}
		if err := goyaml.Unmarshal(raw, &manifest); err != nil {
			return nil, fmt.Errorf("parse repo-history manifest %s: %w", path, err)
		}
		project := strings.TrimSpace(manifest.Project.ID)
		if project == "" {
			project = filepath.Base(filepath.Dir(path))
		}
		for _, bug := range manifest.Bugs {
			bugID := strings.TrimSpace(bug.ID)
			if bugID == "" {
				continue
			}
			title := strings.TrimSpace(bug.Title)
			if title == "" {
				title = strings.TrimSpace(manifest.Project.Title)
			}
			entries = append(entries, capsuleListEntry{
				Ref:         "repo-history/" + project + "/" + bugID,
				Kind:        "repo-history",
				Title:       title,
				Path:        relCapsulePath(root, path),
				Executor:    repoHistoryCapsuleMaterializer,
				Environment: "repo-history-" + project,
				Project:     project,
				Bug:         bugID,
				LocalOnly:   manifest.Project.LocalOnly,
			})
		}
	}
	return entries, nil
}

func capsuleListRepoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for cur := wd; ; {
		if raw, err := os.ReadFile(filepath.Join(cur, "go.mod")); err == nil && goModModuleIsKitsoki(raw) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return wd
}

func goModModuleIsKitsoki(raw []byte) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest) == "kitsoki"
		}
	}
	return false
}

func relCapsulePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func capsuleVerifyCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "verify <name|workspace>",
		Short: "Verify a capsule spec or materialized workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := capsuleVerifyManaged(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if encErr := enc.Encode(res); encErr != nil {
					return encErr
				}
			} else {
				status := "ok"
				if !res.OK {
					status = "failed"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "capsule %s verification: %s\n", res.CapsuleName, status)
				fmt.Fprintf(cmd.OutOrStdout(), "workspace: %s\n", res.Workspace)
				fmt.Fprintf(cmd.OutOrStdout(), "tree_digest: sha256:%s\n", res.ActualTreeDigest)
				for _, probe := range res.Probes {
					pstatus := "ok"
					if !probe.OK {
						pstatus = "failed"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "probe %s: %s (exit %d)\n", probe.Name, pstatus, probe.ExitCode)
				}
			}
			if !res.OK {
				return fmt.Errorf("capsule verification failed: %v", res.Errors)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print verification JSON")
	return cmd
}

func capsuleVerifyManaged(ctx context.Context, ref string) (capsule.VerifyResult, error) {
	if shouldUseLegacyCapsulePath(ref) {
		return capsule.Verify(ctx, ref, nil)
	}
	projectRoot := capsuleListRepoRoot()
	definitions := control.FileDefinitionStore{ProjectRoot: projectRoot}
	def, err := definitions.Get(ctx, ref)
	if err != nil {
		return capsule.Verify(ctx, ref, nil)
	}
	if def.Source.Kind != control.SourceSynthetic {
		return capsule.VerifyResult{}, fmt.Errorf("capsule verify: definition %q uses provider %q; use capsule workspace create/status for managed non-synthetic workspaces", def.ID, def.Source.Kind)
	}
	root, err := os.MkdirTemp("", "kitsoki-capsule-verify-"+safeCapsuleInstanceID(def.ID)+"-")
	if err != nil {
		return capsule.VerifyResult{}, fmt.Errorf("capsule verify: create temp workspace: %w", err)
	}
	defer os.RemoveAll(root)
	opened, err := capsuleOpenManaged(ctx, ref, filepath.Join(root, safeCapsuleInstanceID(def.ID)))
	if err != nil {
		return capsule.VerifyResult{}, err
	}
	return capsule.Verify(ctx, opened.Manifest.Workspace, nil)
}

func capsuleCloseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "close <workspace>",
		Short: "Remove a materialized capsule workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := capsuleCloseManaged(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "closed %s\n", args[0])
			return nil
		},
	}
	return cmd
}

func capsuleCloseManaged(ctx context.Context, workspace string) error {
	if strings.TrimSpace(workspace) == "" {
		return capsule.Close(workspace)
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	manifest, err := capsule.ReadManifest(abs)
	if err != nil {
		return capsule.Close(workspace)
	}
	projectRoot := capsuleListRepoRoot()
	definitions := control.FileDefinitionStore{ProjectRoot: projectRoot}
	def, err := definitions.Get(ctx, manifest.CapsuleName)
	if err != nil || def.Source.Kind != control.SourceSynthetic {
		return capsule.Close(workspace)
	}
	manager, err := capsuleproject.Open(projectRoot, nil)
	if err != nil {
		return err
	}
	manager.Instances = control.NewMemoryInstanceStore()
	manager.Grant.WorkspaceRoots = []string{filepath.Dir(abs)}
	id := filepath.Base(abs)
	if !validCapsuleManagedInstanceID(id) {
		id = safeCapsuleInstanceID(manifest.CapsuleName) + "-close"
	}
	in, err := manager.Instances.Create(ctx, control.Instance{
		ID:               id,
		DefinitionID:     def.ID,
		DefinitionDigest: def.Digest,
		Provider:         string(control.SourceSynthetic),
		Path:             abs,
		SourceRef:        def.Source.SyntheticSpec,
		Head:             manifest.Source.Head,
		Branch:           manifest.Source.Branch,
		State:            control.StateReady,
		Lease:            control.Lease{Owner: "capsule-cli"},
	})
	if err != nil {
		return err
	}
	return manager.Close(ctx, control.Handle{ID: in.ID, Generation: in.Generation}, "capsule-cli")
}
