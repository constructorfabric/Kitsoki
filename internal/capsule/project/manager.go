// Package project assembles the generic project-scoped Capsule manager shared
// by CLI, MCP, and story host adapters.
package project

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"kitsoki/internal/capsule/control"
)

// ScopeOptions is the immutable authority requested by a project front door.
// An empty Definitions list selects all definitions in this one project; all
// mutating effects and reconciliation branches remain explicit deny-by-default
// lists.
type ScopeOptions struct {
	Owner       string
	Definitions []string
	Effects     []string
	Branches    []string
}

var fullEffects = []string{
	"workspace_manage",
	"fs_write",
	"exec",
	"vcs_commit",
	"local_reconcile",
	"ci_run",
	"env_write",
	"cleanup",
}

// Open preserves the trusted in-process manager capability set used by the
// CLI and story host. Agent-facing adapters should use OpenScoped.
func Open(root string, branches []string) (*control.Manager, error) {
	return OpenScoped(root, ScopeOptions{Effects: fullEffects, Branches: branches})
}

// OpenScoped discovers project definitions while granting only the requested
// definitions, their required source providers, effects, and branches.
func OpenScoped(root string, scope ScopeOptions) (*control.Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	definitions := control.FileDefinitionStore{ProjectRoot: abs}
	list, err := definitions.List(nil)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("capsule project: no definitions found")
	}
	selected, err := selectDefinitions(list, scope.Definitions)
	if err != nil {
		return nil, err
	}
	effects, err := validateEffects(scope.Effects)
	if err != nil {
		return nil, err
	}
	roots := []string{filepath.Join(abs, ".capsules", "workspaces")}
	providers := map[string]control.WorkspaceProvider{}
	executorSet := map[string]bool{}
	git := control.GitSourceProvider{ProjectRoot: abs}
	development := control.DevWorkspaceScriptProvider{ProjectRoot: abs}
	ids := make([]string, 0, len(selected))
	for _, definition := range selected {
		ids = append(ids, definition.ID)
		switch definition.Source.Kind {
		case control.SourceSynthetic:
			providers["synthetic"] = control.SyntheticProvider{ProjectRoot: abs}
			executorSet["synthetic"] = true
		case control.SourceSelf:
			providers["self"], providers["git"] = git, git
			executorSet["self"] = true
		case control.SourcePinned:
			providers["pinned"], providers["git"] = git, git
			executorSet["pinned"] = true
		case control.SourceDevWorkspaceScript:
			providers[string(control.SourceDevWorkspaceScript)] = development
			executorSet[string(control.SourceDevWorkspaceScript)] = true
			if relative := definition.Source.Development.Root; relative != "" {
				candidate := filepath.Join(abs, relative)
				if !contains(roots, candidate) {
					roots = append(roots, candidate)
				}
			}
		}
	}
	executors := make([]string, 0, len(executorSet))
	for name := range executorSet {
		executors = append(executors, name)
	}
	sort.Strings(ids)
	sort.Strings(executors)
	branches := uniqueSorted(scope.Branches)
	return &control.Manager{
		Definitions: definitions,
		Instances:   control.FileInstanceStore{Root: roots[0]},
		Providers:   providers,
		Grant: control.ScopeGrant{
			Owner:          strings.TrimSpace(scope.Owner),
			ProjectRoot:    abs,
			WorkspaceRoots: roots,
			Definitions:    ids,
			Executors:      executors,
			Effects:        effects,
			Branches:       branches,
		},
	}, nil
}

func selectDefinitions(all []control.Definition, requested []string) ([]control.Definition, error) {
	if len(requested) == 0 || contains(requested, "*") {
		return append([]control.Definition(nil), all...), nil
	}
	wanted := map[string]bool{}
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("capsule project: definition id is required")
		}
		wanted[id] = true
	}
	selected := make([]control.Definition, 0, len(wanted))
	for _, definition := range all {
		if wanted[definition.ID] {
			selected = append(selected, definition)
			delete(wanted, definition.ID)
		}
	}
	if len(wanted) > 0 {
		missing := make([]string, 0, len(wanted))
		for id := range wanted {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("capsule project: definitions not found: %s", strings.Join(missing, ", "))
	}
	return selected, nil
}

func validateEffects(requested []string) ([]string, error) {
	for _, effect := range requested {
		if !control.ValidEffect(effect) {
			return nil, fmt.Errorf("capsule project: unknown effect %q", effect)
		}
	}
	return uniqueSorted(requested), nil
}

func uniqueSorted(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func contains(paths []string, candidate string) bool {
	for _, path := range paths {
		if filepath.Clean(path) == filepath.Clean(candidate) {
			return true
		}
	}
	return false
}
