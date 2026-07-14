package kitverify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"kitsoki/internal/app"
	"kitsoki/internal/host/opschema"
	"kitsoki/internal/kit"
	"kitsoki/internal/testrunner"
)

// fileExists reports whether path names a regular, readable file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// StoryReport is the contract-check result for one of a kit's provided
// stories.
type StoryReport struct {
	Story  string
	Issues []string
}

// FlowSuiteResult is one conformance-flow glob pattern's run.
type FlowSuiteResult struct {
	// Pattern is the manifest-declared glob (conformance.flows entry).
	Pattern string
	// AppPath is the app.yaml resolved to run the matched fixtures against.
	AppPath string
	// Report is nil when the pattern matched no files or a fatal error
	// occurred (see Err).
	Report *testrunner.FlowReport
	// Err is non-nil on a fatal error running this pattern (bad glob, app
	// failed to load, ...). A flow *failure* is not an Err — it is
	// reflected in Report.Failed.
	Err error
}

// ExtendsReport is a base kit's own conformance suite, re-run because the
// kit under verification extends it (D6/S4: "base-kit suites should be able
// to re-run against downstream extensions").
type ExtendsReport struct {
	Kit    string // the base kit's @namespace/kit identity, as declared in extends:
	Report *Report
	// Err is set when the base kit could not be resolved/loaded at all
	// (e.g. no ExtendsResolver was supplied, or it returned an error) —
	// this is reported, not silently dropped, but does not itself fail the
	// downstream kit's own verification.
	Err error
}

// Report is the full `kitsoki kit verify` result for one kit.
type Report struct {
	Kit         string // manifest.Identity()
	Dir         string
	Stories     []StoryReport
	ParamIssues []string
	Flows       []FlowSuiteResult
	Extends     []ExtendsReport
}

// OK reports whether every contract check passed and every flow suite that
// ran passed (a flow pattern that matched zero files, or an Extends kit that
// could not be resolved, does not by itself fail OK — see the doc comments
// on FlowSuiteResult/ExtendsReport).
func (r *Report) OK() bool {
	if r == nil {
		return false
	}
	if len(r.ParamIssues) > 0 {
		return false
	}
	for _, s := range r.Stories {
		if len(s.Issues) > 0 {
			return false
		}
	}
	for _, f := range r.Flows {
		if f.Err != nil {
			return false
		}
		if f.Report != nil && f.Report.Failed > 0 {
			return false
		}
	}
	for _, e := range r.Extends {
		if e.Report != nil && !e.Report.OK() {
			return false
		}
	}
	return true
}

// ExtendsResolver resolves an `extends:`/`composes:` dependency's
// fully-qualified identity (e.g. "@constructorfabric/iso-ms") to the
// absolute directory of that kit's own kit.yaml on disk.
//
// S4 deliberately does not depend on S2's kit resolution machinery
// (kits.lock / git-tier fetch — not landed on this slice's pinned base;
// see the plan doc's sequencing diagram). A caller that has a real kit
// registry/lockfile wires it in here; `kitsoki kit verify` without one
// simply skips the re-run and reports it via ExtendsReport.Err.
type ExtendsResolver func(identity string) (dir string, err error)

// Options configures VerifyKit.
type Options struct {
	// Registry is the Go-handler op-schema table CheckInterfaceOpShapes
	// compares host_interfaces defaults against. nil uses
	// opschema.Builtins().
	Registry *opschema.Registry
	// ImportResolver threads through to app.LoadWithResolver /
	// testrunner.RunFlows for a story's own `imports:` (rare — a kit story
	// is meant to be standalone, but nothing forbids it importing another
	// story from the same kit).
	ImportResolver app.ImportResolver
	// Flow passes through to testrunner.RunFlows for every matched flow
	// fixture (recording override, fail-fast, etc.).
	Flow testrunner.FlowOptions
	// Extends resolves `extends:` dependencies to a kit dir for the
	// base-kit conformance re-run. nil skips this entirely (each entry is
	// still listed in the report, with Err explaining why it was skipped).
	Extends ExtendsResolver
	// noExtendsRecursion guards against a cyclic extends: chain re-running
	// forever; VerifyKit sets this internally on recursive calls.
	noExtendsRecursion bool
}

// VerifyKit is `kitsoki kit verify`'s engine: schema-load the kit manifest
// at kitDir, run every S4 standalone contract check against each of its
// provides.stories, run its declared conformance.flows fixtures, and (when
// opts.Extends is supplied) re-run every extends: dependency's own
// conformance suite too.
func VerifyKit(kitDir string, opts Options) (*Report, error) {
	manifest, err := kit.LoadDir(kitDir)
	if err != nil {
		return nil, fmt.Errorf("kit verify: %w", err)
	}
	registry := opts.Registry
	if registry == nil {
		registry = opschema.Builtins()
	}

	report := &Report{Kit: manifest.Identity(), Dir: manifest.Dir()}
	report.ParamIssues = CheckParameters(manifest, nil)

	for _, storyName := range manifest.Provides.Stories {
		sr := StoryReport{Story: storyName}
		storyPath := filepath.Join(manifest.StoryDir(storyName), "app.yaml")
		def, loadErr := app.LoadWithResolver(storyPath, nil, opts.ImportResolver)
		if loadErr != nil {
			sr.Issues = append(sr.Issues, fmt.Sprintf("failed to load standalone: %v", loadErr))
			report.Stories = append(report.Stories, sr)
			continue
		}
		sr.Issues = append(sr.Issues, CheckExitRequires(def)...)
		sr.Issues = append(sr.Issues, CheckExportsIntents(def)...)
		sr.Issues = append(sr.Issues, CheckInterfaceOpShapes(def, registry)...)
		report.Stories = append(report.Stories, sr)
	}

	report.Flows = runConformanceFlows(manifest, opts)

	if !opts.noExtendsRecursion {
		report.Extends = runExtends(manifest, opts)
	}

	return report, nil
}

// runConformanceFlows runs every manifest.Conformance.Flows glob pattern.
// Patterns are kit-root-relative (schema.json's documented contract); the
// app a matched fixture runs against is discovered by walking up from the
// fixture's directory to the nearest ancestor (bounded at kitDir) that
// contains an app.yaml — the authoring convention every existing flow
// fixture already follows (`stories/<name>/flows/*.yaml` next to
// `stories/<name>/app.yaml`).
func runConformanceFlows(manifest *kit.Def, opts Options) []FlowSuiteResult {
	var out []FlowSuiteResult
	ctx := context.Background()
	kitDir := manifest.Dir()
	for _, pattern := range manifest.Conformance.Flows {
		absPattern := pattern
		if !filepath.IsAbs(absPattern) {
			absPattern = filepath.Join(kitDir, pattern)
		}
		matches, globErr := filepath.Glob(absPattern)
		if globErr != nil {
			out = append(out, FlowSuiteResult{Pattern: pattern, Err: fmt.Errorf("glob %q: %w", pattern, globErr)})
			continue
		}
		if len(matches) == 0 {
			out = append(out, FlowSuiteResult{Pattern: pattern})
			continue
		}
		appPath, findErr := findAppYAML(matches[0], kitDir)
		if findErr != nil {
			out = append(out, FlowSuiteResult{Pattern: pattern, Err: findErr})
			continue
		}
		flowOpts := opts.Flow
		flowOpts.ImportResolver = opts.ImportResolver
		rep, runErr := testrunner.RunFlows(ctx, appPath, absPattern, flowOpts)
		out = append(out, FlowSuiteResult{Pattern: pattern, AppPath: appPath, Report: rep, Err: runErr})
	}
	return out
}

// findAppYAML walks up from startFile's directory looking for an app.yaml,
// stopping (and failing) once it would climb above floor.
func findAppYAML(startFile, floor string) (string, error) {
	floor = filepath.Clean(floor)
	dir := filepath.Dir(startFile)
	for {
		candidate := filepath.Join(dir, "app.yaml")
		if fileExists(candidate) {
			return candidate, nil
		}
		if dir == floor || dir == "." || dir == string(filepath.Separator) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no app.yaml found above %q (within kit root %q)", startFile, floor)
}

// runExtends re-runs each extends: dependency's own conformance suite
// (contract checks + its own conformance.flows), per D6/S4's "base-kit
// suites re-run against downstream extensions."
func runExtends(manifest *kit.Def, opts Options) []ExtendsReport {
	if len(manifest.Extends) == 0 {
		return nil
	}
	deps := make([]kit.Dependency, len(manifest.Extends))
	copy(deps, manifest.Extends)
	sort.Slice(deps, func(i, j int) bool { return deps[i].Kit < deps[j].Kit })

	var out []ExtendsReport
	for _, dep := range deps {
		if opts.Extends == nil {
			out = append(out, ExtendsReport{
				Kit: dep.Kit,
				Err: fmt.Errorf("no ExtendsResolver configured — pass Options.Extends to re-run %s's conformance suite", dep.Kit),
			})
			continue
		}
		baseDir, resolveErr := opts.Extends(dep.Kit)
		if resolveErr != nil {
			out = append(out, ExtendsReport{Kit: dep.Kit, Err: fmt.Errorf("resolve %s: %w", dep.Kit, resolveErr)})
			continue
		}
		baseOpts := opts
		baseOpts.noExtendsRecursion = true // one level deep is enough; avoids cycles
		baseReport, err := VerifyKit(baseDir, baseOpts)
		if err != nil {
			out = append(out, ExtendsReport{Kit: dep.Kit, Err: fmt.Errorf("verify %s at %s: %w", dep.Kit, baseDir, err)})
			continue
		}
		out = append(out, ExtendsReport{Kit: dep.Kit, Report: baseReport})
	}
	return out
}
