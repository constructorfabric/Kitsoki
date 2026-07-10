package main

import (
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
	return cmd
}

type capsuleListEntry struct {
	Ref       string `json:"ref"`
	Kind      string `json:"kind"`
	Title     string `json:"title,omitempty"`
	Path      string `json:"path"`
	Executor  string `json:"executor"`
	Project   string `json:"project,omitempty"`
	Bug       string `json:"bug,omitempty"`
	Scenario  string `json:"scenario,omitempty"`
	LocalOnly bool   `json:"local_only,omitempty"`
}

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
			res, err := capsule.Open(cmd.Context(), args[0], capsule.OpenOptions{Dest: dest})
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
	fmt.Fprintln(w, "| Capsule | Kind | Executor | Path |")
	fmt.Fprintln(w, "|---|---|---|---|")
	for _, entry := range entries {
		fmt.Fprintf(w, "| `%s` | %s | %s | `%s` |\n", entry.Ref, entry.Kind, entry.Executor, entry.Path)
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
	paths, err := filepath.Glob(filepath.Join(root, "capsules", "*", "capsule.yaml"))
	if err != nil {
		return nil, err
	}
	var entries []capsuleListEntry
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read capsule spec %s: %w", path, err)
		}
		var spec struct {
			Name     string `yaml:"name"`
			Scenario struct {
				Kind string `yaml:"kind"`
			} `yaml:"scenario"`
		}
		if err := goyaml.Unmarshal(raw, &spec); err != nil {
			return nil, fmt.Errorf("parse capsule spec %s: %w", path, err)
		}
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			name = filepath.Base(filepath.Dir(path))
		}
		entries = append(entries, capsuleListEntry{
			Ref:      name,
			Kind:     "core",
			Path:     relCapsulePath(root, path),
			Executor: "internal/capsule",
			Scenario: strings.TrimSpace(spec.Scenario.Kind),
		})
	}
	return entries, nil
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
				Ref:       "repo-history/" + project + "/" + bugID,
				Kind:      "repo-history",
				Title:     title,
				Path:      relCapsulePath(root, path),
				Executor:  "bugfix-bakeoff",
				Project:   project,
				Bug:       bugID,
				LocalOnly: manifest.Project.LocalOnly,
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
			res, err := capsule.Verify(cmd.Context(), args[0], nil)
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

func capsuleCloseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "close <workspace>",
		Short: "Remove a materialized capsule workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := capsule.Close(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "closed %s\n", args[0])
			return nil
		},
	}
	return cmd
}
