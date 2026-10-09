package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	goyaml "github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
)

type projectProfileStoryPack struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Stories []string `json:"stories"`
}

var projectProfileStoryPacks = []projectProfileStoryPack{
	{
		ID:      "focused-engineering",
		Title:   "Focused engineering starter",
		Summary: "Focused first-run set: setup, bugfixing, repo-history capsules, PR refinement, and git operations.",
		Stories: []string{"setup", "bugfix", "repo-bakeoff", "pr-refinement", "git-ops"},
	},
	{
		ID:      "core-setup",
		Title:   "Core setup",
		Summary: "Bring a project under Kitsoki management with setup and guarded git operations only.",
		Stories: []string{"setup", "git-ops"},
	},
	{
		ID:      "planning-delivery",
		Title:   "Planning and delivery",
		Summary: "Add PRD/design and implementation loops on top of setup and git operations.",
		Stories: []string{"setup", "git-ops", "prd", "implementation", "pr-refinement"},
	},
	{
		ID:      "review-quality",
		Title:   "Review and quality",
		Summary: "Emphasize bug fixing, test repair, code review, docs review, and git operations.",
		Stories: []string{"setup", "git-ops", "bugfix", "fix-tests", "code-review", "docs-review", "pr-refinement"},
	},
	{
		ID:      "full-dev-story",
		Title:   "Full dev-story",
		Summary: "Expose the full day-level dev-story catalog after the team is ready for broader adoption.",
		Stories: []string{
			"setup",
			"git-ops",
			"bugfix",
			"fix-tests",
			"pr-refinement",
			"prd",
			"implementation",
			"deliver",
			"code-review",
			"docs-review",
			"deploy",
			"observability",
			"incident",
		},
	},
}

func projectProfileStoryPacksCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "story-packs",
		Short: "List or apply onboarding story packs in a project profile",
	}
	cmd.PersistentFlags().StringVar(&target, "target", ".", "project checkout containing .kitsoki/project-profile.yaml")
	cmd.AddCommand(projectProfileStoryPacksListCmd(&target))
	cmd.AddCommand(projectProfileStoryPacksSetCmd(&target, false))
	cmd.AddCommand(projectProfileStoryPacksSetCmd(&target, true))
	return cmd
}

func projectProfileStoryPacksListCmd(target *string) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List available onboarding story packs",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, profile, err := readOptionalProjectProfileForStoryPacks(*target)
			if err != nil {
				return err
			}
			currentPack, enabled := currentProjectProfileStoryScope(profile)
			if jsonOut {
				payload := map[string]any{
					"profile_path":     path,
					"profile_exists":   profile != nil,
					"story_pack":       currentPack,
					"enabled_stories":  enabled,
					"available_packs":  projectProfileStoryPacks,
					"normal_commands":  []string{"kitsoki project-profile story-packs set <pack>", "kitsoki project-profile story-packs add <pack>"},
					"expansion_policy": "story packs are adoption scope only; dev-story remains available",
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "story packs:")
			for _, pack := range projectProfileStoryPacks {
				fmt.Fprintf(out, "  %s - %s\n", pack.ID, pack.Title)
				fmt.Fprintf(out, "    %s\n", pack.Summary)
				fmt.Fprintf(out, "    stories: %s\n", strings.Join(pack.Stories, ", "))
			}
			fmt.Fprintf(out, "\nprofile: %s\n", path)
			if profile == nil {
				fmt.Fprintln(out, "profile_exists: false")
				return nil
			}
			fmt.Fprintf(out, "story_pack: %s\n", currentPack)
			fmt.Fprintf(out, "enabled_stories: %s\n", strings.Join(enabled, ", "))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON")
	return cmd
}

func projectProfileStoryPacksSetCmd(target *string, add bool) *cobra.Command {
	verb := "set"
	short := "Replace the profile enabled story set with one pack"
	if add {
		verb = "add"
		short = "Add one story pack to the profile enabled story set"
	}
	cmd := &cobra.Command{
		Use:          verb + " <pack>",
		Short:        short,
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pack, ok := findProjectProfileStoryPack(args[0])
			if !ok {
				return fmt.Errorf("unknown story pack %q; run `kitsoki project-profile story-packs list`", args[0])
			}
			path, profile, err := readProjectProfileForStoryPacks(*target)
			if err != nil {
				return err
			}
			if add {
				applyProjectProfileStoryPackAdd(profile, pack)
			} else {
				applyProjectProfileStoryPackSet(profile, pack)
			}
			if err := writeProjectProfileForStoryPacks(path, profile); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s story pack %s in %s\n", verb, pack.ID, path)
			return nil
		},
	}
	return cmd
}

func findProjectProfileStoryPack(raw string) (projectProfileStoryPack, bool) {
	id := normalizeProjectProfileStoryPackID(raw)
	for _, pack := range projectProfileStoryPacks {
		if pack.ID == id {
			return pack, true
		}
	}
	return projectProfileStoryPack{}, false
}

func normalizeProjectProfileStoryPackID(raw string) string {
	id := strings.ToLower(strings.TrimSpace(raw))
	id = strings.ReplaceAll(id, "_", "-")
	id = strings.Trim(id, "'\"`")
	switch id {
	case "focusedengineering", "focused", "focused-engineering", "targeted", "targeted-engineering":
		return "focused-engineering"
	case "core", "setup", "minimal", "essentials":
		return "core-setup"
	case "planning", "delivery", "prd":
		return "planning-delivery"
	case "quality", "review":
		return "review-quality"
	case "full", "all", "full-dev":
		return "full-dev-story"
	default:
		return id
	}
}

func readOptionalProjectProfileForStoryPacks(target string) (string, map[string]any, error) {
	path, err := projectProfilePathForStoryPacks(target)
	if err != nil {
		return "", nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return path, nil, nil
	}
	if err != nil {
		return path, nil, fmt.Errorf("read profile: %w", err)
	}
	profile, err := decodeProjectProfileStoryPacks(raw)
	if err != nil {
		return path, nil, err
	}
	return path, profile, nil
}

func readProjectProfileForStoryPacks(target string) (string, map[string]any, error) {
	path, profile, err := readOptionalProjectProfileForStoryPacks(target)
	if err != nil {
		return "", nil, err
	}
	if profile == nil {
		return path, nil, fmt.Errorf("project profile not found at %s", path)
	}
	return path, profile, nil
}

func decodeProjectProfileStoryPacks(raw []byte) (map[string]any, error) {
	var profile map[string]any
	if err := goyaml.Unmarshal(raw, &profile); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	return normalizeProjectProfileMap(profile), nil
}

func writeProjectProfileForStoryPacks(path string, profile map[string]any) error {
	raw, err := goyaml.Marshal(profile)
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write profile: %w", err)
	}
	return nil
}

func projectProfilePathForStoryPacks(target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		target = "."
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve target: %w", err)
	}
	return filepath.Join(abs, ".kitsoki", "project-profile.yaml"), nil
}

func currentProjectProfileStoryScope(profile map[string]any) (string, []string) {
	if profile == nil {
		return "", nil
	}
	kitsoki := projectProfileMap(profile, "kitsoki")
	packID := projectProfileString(kitsoki, "story_pack")
	enabled := projectProfileStringSlice(kitsoki["enabled_stories"])
	if packID == "" {
		onboarding := projectProfileMap(profile, "onboarding")
		packID = projectProfileString(onboarding, "story_pack")
	}
	if packID == "" {
		packID = "custom"
	}
	return packID, enabled
}

func applyProjectProfileStoryPackSet(profile map[string]any, pack projectProfileStoryPack) {
	kitsoki := ensureProjectProfileMap(profile, "kitsoki")
	kitsoki["story_pack"] = pack.ID
	kitsoki["enabled_stories"] = stringsToAnySlice(pack.Stories)

	onboarding := ensureProjectProfileMap(profile, "onboarding")
	onboarding["story_pack"] = pack.ID
	onboarding["story_pack_title"] = pack.Title
	onboarding["story_pack_summary"] = pack.Summary
	onboarding["starter_stories"] = projectProfileStarterStories(pack.Stories)
	onboarding["expansion_policy"] = projectProfileExpansionPolicy(pack.ID, pack.Stories)
}

func applyProjectProfileStoryPackAdd(profile map[string]any, pack projectProfileStoryPack) {
	kitsoki := ensureProjectProfileMap(profile, "kitsoki")
	current := projectProfileStringSlice(kitsoki["enabled_stories"])
	enabled := appendUniqueStrings(current, pack.Stories...)
	if projectProfileString(kitsoki, "story_pack") == "" {
		kitsoki["story_pack"] = pack.ID
	}
	kitsoki["enabled_stories"] = stringsToAnySlice(enabled)

	onboarding := ensureProjectProfileMap(profile, "onboarding")
	if projectProfileString(onboarding, "story_pack") == "" {
		onboarding["story_pack"] = pack.ID
		onboarding["story_pack_title"] = pack.Title
		onboarding["story_pack_summary"] = pack.Summary
	}
	onboarding["starter_stories"] = mergeProjectProfileStarterStories(onboarding["starter_stories"], pack.Stories)
	onboarding["expansion_policy"] = projectProfileExpansionPolicy(projectProfileString(kitsoki, "story_pack"), enabled)
}

func projectProfileExpansionPolicy(packID string, stories []string) string {
	return fmt.Sprintf(
		"Start new teams on the `%s` story pack: %s. Treat it as an adoption scope, not a runtime fence. Use `kitsoki project-profile story-packs list`, `set <pack>`, or `add <pack>` to manage expansion after adding readiness checks/flows.",
		packID,
		strings.Join(stories, ", "),
	)
}

func mergeProjectProfileStarterStories(current any, add []string) []any {
	seen := map[string]bool{}
	var out []any
	if items, ok := current.([]any); ok {
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id := projectProfileString(m, "id")
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, m)
		}
	}
	for _, story := range add {
		if seen[story] {
			continue
		}
		seen[story] = true
		out = append(out, projectProfileStarterStory(story))
	}
	return out
}

func projectProfileStarterStories(stories []string) []any {
	out := make([]any, 0, len(stories))
	for _, story := range stories {
		out = append(out, projectProfileStarterStory(story))
	}
	return out
}

func projectProfileStarterStory(story string) map[string]any {
	title, summary, expansion := projectProfileStoryMetadata(story)
	return map[string]any{
		"id":           story,
		"title":        title,
		"source_story": projectProfileSourceStory(story),
		"status":       "enabled",
		"summary":      summary,
		"expansion":    expansion,
	}
}

func projectProfileStoryMetadata(story string) (string, string, string) {
	switch story {
	case "setup":
		return "Project setup", "Onboard the checkout, install the Kitsoki toolkit, and run explicit readiness checks.", "Keep this enabled as the team's bootstrap and refresh path."
	case "bugfix":
		return "Bug fixing", "Drive a picked bug through reproduce, fix, test, review, and validation gates.", "Add narrower project bug policies through profile customizations or prompt overlays."
	case "pr-refinement":
		return "PR refinement", "Open or attach to a PR, monitor CI/review feedback, respond, and merge when accepted.", "Add provider-specific PR policy after the base loop is proven on real pull requests."
	case "git-ops":
		return "Git operations", "Use the guarded git command hub for status, worktrees, commits, rebases, sync, and integration.", "Extend with project branch and remote-sync policy once the team's git flow is stable."
	default:
		return titleFromProjectProfileStoryID(story), "Project-selected Kitsoki story. Confirm it loads and has project-ready fixtures before treating it as a default path.", "Promote this story after adding project readiness evidence."
	}
}

func projectProfileSourceStory(story string) string {
	if story == "setup" {
		return "dev-story:onboarding"
	}
	return story
}

func titleFromProjectProfileStoryID(story string) string {
	parts := strings.Fields(strings.ReplaceAll(story, "-", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func ensureProjectProfileMap(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok {
		return existing
	}
	next := map[string]any{}
	parent[key] = next
	return next
}

func projectProfileMap(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok {
		return existing
	}
	return map[string]any{}
}

func projectProfileString(parent map[string]any, key string) string {
	if value, ok := parent[key].(string); ok {
		return value
	}
	return ""
}

func projectProfileStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func appendUniqueStrings(current []string, values ...string) []string {
	seen := make(map[string]bool, len(current)+len(values))
	out := make([]string, 0, len(current)+len(values))
	for _, value := range append(current, values...) {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func stringsToAnySlice(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}
