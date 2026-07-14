package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	legacy "kitsoki/internal/capsule"
)

// SyntheticProvider adapts the shipped deterministic capsule materializer to
// the control plane. It is deliberately a provider rather than a CLI special
// case, so the same Definition/lease/handle rules cover old fixtures and new
// project definitions.
type SyntheticProvider struct {
	ProjectRoot string
	Runner      legacy.Runner
}

func (SyntheticProvider) Name() string { return string(SourceSynthetic) }

func (p SyntheticProvider) Create(ctx context.Context, def Definition, in Instance) (MaterializedWorkspace, error) {
	if def.Source.Kind != SourceSynthetic {
		return MaterializedWorkspace{}, fmt.Errorf("synthetic provider: definition %q has source %q", def.ID, def.Source.Kind)
	}
	root, err := projectRoot(p.ProjectRoot)
	if err != nil {
		return MaterializedWorkspace{}, err
	}
	spec, err := ResolveWorkspacePath(root, def.Source.SyntheticSpec, true)
	if err != nil {
		return MaterializedWorkspace{}, fmt.Errorf("synthetic provider: spec: %w", err)
	}
	if filepath.Base(spec) != "capsule.yaml" && !strings.HasSuffix(spec, ".yaml") {
		return MaterializedWorkspace{}, fmt.Errorf("synthetic provider: spec must be yaml")
	}
	if err := os.MkdirAll(filepath.Dir(in.Path), 0o755); err != nil {
		return MaterializedWorkspace{}, err
	}
	opened, err := legacy.Open(ctx, spec, legacy.OpenOptions{Dest: in.Path, Runner: p.Runner})
	if err != nil {
		return MaterializedWorkspace{}, err
	}
	return MaterializedWorkspace{Path: opened.Manifest.Workspace, SourceRef: def.Source.SyntheticSpec, Head: opened.Manifest.Source.Head, Branch: opened.Manifest.Source.Branch}, nil
}

func (SyntheticProvider) Close(_ context.Context, in Instance) error {
	if strings.TrimSpace(in.Path) == "" {
		return fmt.Errorf("synthetic provider: instance path is required")
	}
	return legacy.Close(in.Path)
}
