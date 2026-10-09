package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	goyaml "github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/baseskills"
	"kitsoki/internal/webconfig"
)

type projectUpgradeOptions struct {
	Target string
	Apply  bool
}

type projectUpgradeReport struct {
	Target       string                `json:"target"`
	Onboarded    bool                  `json:"onboarded"`
	NeedsUpgrade bool                  `json:"needs_upgrade"`
	ConfigPath   string                `json:"config_path,omitempty"`
	ProfilePath  string                `json:"profile_path,omitempty"`
	InstancePath string                `json:"instance_path,omitempty"`
	MCPPath      string                `json:"mcp_path,omitempty"`
	Checks       []projectUpgradeCheck `json:"checks"`
	Hints        []string              `json:"hints,omitempty"`
	Actions      []string              `json:"actions,omitempty"`
}

type projectUpgradeCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func projectToolsUpgradeCmd() *cobra.Command {
	var (
		target string
		apply  bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Check whether a project needs Kitsoki onboarding/toolkit refresh",
		Long: `Check a target project for stale or missing Kitsoki project files.

The check is read-only by default. It validates the project .kitsoki.yaml
implicit root, the project-profile pointer, any materialized dev-story
instance under .kitsoki/stories, and the .mcp.json toolkit registration.

Passing --apply refreshes only the embedded project toolkit (.agents/.claude
and .mcp.json). It does not overwrite a materialized project story instance;
rerun onboarding or use a future explicit regeneration flow for that.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep, err := checkProjectUpgrade(cmd.Context(), projectUpgradeOptions{
				Target: target,
				Apply:  apply,
			})
			if err != nil {
				return err
			}
			if asJSON {
				b, _ := json.MarshalIndent(rep, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			printProjectUpgradeReport(cmd, rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", ".", "target project root to check")
	cmd.Flags().BoolVar(&apply, "apply", false, "refresh the embedded project toolkit and .mcp.json registration")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit a JSON upgrade report")
	return cmd
}

func checkProjectUpgrade(ctx context.Context, opts projectUpgradeOptions) (projectUpgradeReport, error) {
	target := strings.TrimSpace(opts.Target)
	if target == "" {
		target = "."
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return projectUpgradeReport{}, fmt.Errorf("resolve --target: %w", err)
	}
	rep := projectUpgradeReport{Target: abs}

	configPath := filepath.Join(abs, webconfig.DefaultConfigFile)
	rep.ConfigPath = configPath
	if _, err := os.Stat(configPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			rep.addCheck("config", "error", fmt.Sprintf("stat %s: %v", configPath, err))
			return rep, nil
		}
		rep.addCheck("config", "missing", "no .kitsoki.yaml found")
		rep.Hints = append(rep.Hints,
			"Open `kitsoki run` in this checkout and type `onboard .` to connect it to the current dev-story.",
		)
		if opts.Apply {
			if err := applyProjectToolkit(ctx, &rep); err != nil {
				return rep, err
			}
		}
		rep.checkMCP()
		rep.finalizeNeedsUpgrade()
		return rep, nil
	}

	rep.Onboarded = true
	rep.addCheck("config", "ok", ".kitsoki.yaml found")

	cfg, err := webconfig.Load(configPath)
	if err != nil {
		rep.addCheck("config-load", "error", err.Error())
		rep.Hints = append(rep.Hints, "Fix `.kitsoki.yaml` / `.kitsoki/project-profile.yaml`, then rerun `kitsoki project-tools upgrade`.")
		if opts.Apply {
			if err := applyProjectToolkit(ctx, &rep); err != nil {
				return rep, err
			}
		}
		rep.checkMCP()
		rep.finalizeNeedsUpgrade()
		return rep, nil
	}
	if _, err := app.SynthesizeRootWithResolver(cfg.Root.RootSpec(), abs, buildImportResolver()); err != nil {
		rep.addCheck("implicit-root", "error", fmt.Sprintf("synthesize current dev-story root: %v", err))
		rep.Hints = append(rep.Hints, "The project profile or root overrides no longer match the current dev-story contract.")
	} else {
		rep.addCheck("implicit-root", "ok", "current dev-story root synthesizes")
	}

	profilePath := resolveProjectProfilePath(configPath, cfg.ProjectProfile)
	if _, err := os.Stat(profilePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			rep.addCheck("project-profile", "missing", "no project profile found")
			rep.Hints = append(rep.Hints, "Rerun dev-story onboarding if this checkout should be project-profile managed.")
		} else {
			rep.addCheck("project-profile", "error", fmt.Sprintf("stat %s: %v", profilePath, err))
		}
	} else {
		rep.ProfilePath = profilePath
		rep.addCheck("project-profile", "ok", projectRelative(abs, profilePath))
		if instancePath, detail := projectProfileInstancePath(abs, profilePath); instancePath != "" {
			rep.InstancePath = instancePath
			rep.addCheck("project-instance-pointer", "ok", detail)
		} else if detail != "" {
			rep.addCheck("project-instance-pointer", "missing", detail)
		}
	}

	if rep.InstancePath == "" {
		matches, err := filepath.Glob(filepath.Join(abs, ".kitsoki", "stories", "*", "app.yaml"))
		if err != nil {
			rep.addCheck("project-instance", "error", err.Error())
		} else {
			sort.Strings(matches)
			switch len(matches) {
			case 0:
				rep.addCheck("project-instance", "missing", "no .kitsoki/stories/*/app.yaml found")
				rep.Hints = append(rep.Hints, "A materialized project story is missing; rerun onboarding after reviewing any local profile edits.")
			case 1:
				rep.InstancePath = matches[0]
			default:
				rep.addCheck("project-instance", "skipped", fmt.Sprintf("multiple project stories found (%d); pass one explicitly through normal story validation", len(matches)))
			}
		}
	}
	if rep.InstancePath != "" {
		if _, err := app.LoadWithResolver(rep.InstancePath, nil, buildImportResolver()); err != nil {
			rep.addCheck("project-instance", "error", fmt.Sprintf("%s does not load against current dev-story: %v", projectRelative(abs, rep.InstancePath), err))
			rep.Hints = append(rep.Hints, "Review the materialized instance before regenerating; `project-tools upgrade --apply` will not overwrite it.")
		} else {
			rep.addCheck("project-instance", "ok", projectRelative(abs, rep.InstancePath))
		}
	}

	if opts.Apply {
		if err := applyProjectToolkit(ctx, &rep); err != nil {
			return rep, err
		}
	}
	rep.checkMCP()
	rep.finalizeNeedsUpgrade()
	return rep, nil
}

func applyProjectToolkit(ctx context.Context, rep *projectUpgradeReport) error {
	installRep, err := baseskills.Install(ctx, rep.Target)
	if err != nil {
		return err
	}
	rep.MCPPath = installRep.MCPPath
	rep.Actions = append(rep.Actions, fmt.Sprintf("refreshed project toolkit: %d skills, %d agents", len(installRep.Skills), len(installRep.Agents)))
	if installRep.MCPWritten {
		rep.Actions = append(rep.Actions, "updated .mcp.json kitsoki server registration")
	}
	return nil
}

func (rep *projectUpgradeReport) checkMCP() {
	mcpPath := filepath.Join(rep.Target, ".mcp.json")
	rep.MCPPath = mcpPath
	b, err := os.ReadFile(mcpPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			rep.addCheck("mcp", "missing", ".mcp.json does not register the kitsoki studio MCP server")
			rep.Hints = append(rep.Hints, "Run `kitsoki project-tools upgrade --apply` to refresh .agents/.claude and .mcp.json.")
			return
		}
		rep.addCheck("mcp", "error", fmt.Sprintf("read %s: %v", mcpPath, err))
		return
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		rep.addCheck("mcp", "error", fmt.Sprintf("parse %s: %v", mcpPath, err))
		return
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	want := map[string]any{"command": "kitsoki", "args": baseskills.MCPArgs}
	if projectJSONEqual(servers["kitsoki"], want) {
		rep.addCheck("mcp", "ok", "kitsoki studio MCP server is registered")
		return
	}
	rep.addCheck("mcp", "stale", ".mcp.json kitsoki server is missing or differs from this binary")
	rep.Hints = append(rep.Hints, "Run `kitsoki project-tools upgrade --apply` to refresh .agents/.claude and .mcp.json.")
}

func resolveProjectProfilePath(configPath, profile string) string {
	if strings.TrimSpace(profile) == "" {
		profile = filepath.Join(".kitsoki", "project-profile.yaml")
	}
	if filepath.IsAbs(profile) {
		return filepath.Clean(profile)
	}
	return filepath.Join(filepath.Dir(configPath), profile)
}

func projectProfileInstancePath(root, profilePath string) (string, string) {
	b, err := os.ReadFile(profilePath)
	if err != nil {
		return "", fmt.Sprintf("read %s: %v", profilePath, err)
	}
	var doc struct {
		Kitsoki struct {
			Instance struct {
				ID   string `yaml:"id"`
				Path string `yaml:"path"`
			} `yaml:"instance"`
		} `yaml:"kitsoki"`
	}
	if err := goyaml.Unmarshal(b, &doc); err != nil {
		return "", fmt.Sprintf("parse %s: %v", profilePath, err)
	}
	p := strings.TrimSpace(doc.Kitsoki.Instance.Path)
	if p == "" {
		return "", "kitsoki.instance.path is empty"
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p), projectRelative(root, p)
}

func (rep *projectUpgradeReport) addCheck(id, status, detail string) {
	rep.Checks = append(rep.Checks, projectUpgradeCheck{ID: id, Status: status, Detail: detail})
}

func (rep *projectUpgradeReport) finalizeNeedsUpgrade() {
	for _, check := range rep.Checks {
		switch check.Status {
		case "ok", "skipped":
			continue
		default:
			rep.NeedsUpgrade = true
			return
		}
	}
	rep.NeedsUpgrade = false
}

func projectUpgradeNoticeForRoot(root string) string {
	rep, err := checkProjectUpgrade(context.Background(), projectUpgradeOptions{Target: root})
	if err != nil || !rep.NeedsUpgrade {
		return ""
	}
	if !rep.Onboarded {
		return "(kitsoki: this checkout is not onboarded yet - type `onboard .` to connect it to the current dev-story)"
	}
	var problems []string
	for _, check := range rep.Checks {
		if check.Status != "ok" && check.Status != "skipped" {
			problems = append(problems, check.ID)
		}
	}
	if len(problems) == 0 {
		return ""
	}
	return fmt.Sprintf("(kitsoki: project files may need refresh: %s - run `kitsoki project-tools upgrade --target .`, then add `--apply` to refresh the toolkit)", strings.Join(problems, ", "))
}

func projectOnboardedForRoot(root string) bool {
	configPath := filepath.Join(root, webconfig.DefaultConfigFile)
	cfg, err := webconfig.Load(configPath)
	if err != nil {
		return false
	}
	profilePath := resolveProjectProfilePath(configPath, cfg.ProjectProfile)
	if _, err := os.Stat(profilePath); err != nil {
		return false
	}
	return true
}

func printProjectUpgradeReport(cmd *cobra.Command, rep projectUpgradeReport) {
	status := "current"
	if rep.NeedsUpgrade {
		status = "needs attention"
	}
	if !rep.Onboarded {
		status = "not onboarded"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "kitsoki project check for %s: %s\n", rep.Target, status)
	for _, check := range rep.Checks {
		if check.Detail == "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  %-24s %s\n", check.ID, check.Status)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "  %-24s %-8s %s\n", check.ID, check.Status, check.Detail)
		}
	}
	for _, action := range rep.Actions {
		fmt.Fprintf(cmd.OutOrStdout(), "  action: %s\n", action)
	}
	for _, hint := range uniqueStrings(rep.Hints) {
		fmt.Fprintf(cmd.OutOrStdout(), "  hint: %s\n", hint)
	}
}

func projectRelative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}
	return rel
}

func projectJSONEqual(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ab) == string(bb)
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
