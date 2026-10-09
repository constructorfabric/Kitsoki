// Package host — host.cypilot_artifacts — cypilot SDLC artifact provider.
//
// Implements the `artifact` host_interface against the cypilot `cpt` CLI.
// PRD / ADR / DESIGN / DECOMPOSITION /
// FEATURE / CODE artifacts are managed by cypilot's existing template +
// checklist + validator machinery; this provider is a thin Go shell-out
// layer that lets a kitsoki story drive cypilot's three workflows
// (`cypilot-generate`, `cypilot-plan`, `cypilot-analyze`) from a state
// machine.
//
// # Where the story lives vs. where the provider lives
//
// The cypilot STORY (rooms / prompts / schemas / flows)
// is hosted interim in kitsoki and migrates to the cypilot upstream repo
// later.  This PROVIDER (the Go handler) is the permanent kitsoki side —
// it stays in this tree even after the story migrates, because it has no
// cypilot-internal knowledge: it just shells out to the `cpt` binary.
//
// # Op → cpt CLI mapping
//
// Idealized command shapes:
//
//	list      →  cpt artifact list --kind <k>           (today's cpt may need
//	                                                     a --json flag added;
//	                                                     we pass --json
//	                                                     defensively and
//	                                                     accept whatever
//	                                                     envelope comes back)
//	get       →  read the artifact file directly        (cpt's artifacts.toml
//	                                                     owns path conventions
//	                                                     — but the file is
//	                                                     the source of truth)
//	create    →  cpt generate --kind <k> --title <t>
//	                          --slug <s> --parent <p>
//	validate  →  cpt analyze --target <id> --mode <m>   (mode in
//	                                                     {deterministic,
//	                                                     semantic, consistency})
//	decompose →  cpt plan --task <id>                    (writes
//	                                                     .plans/<slug>/phase-NN-*.md)
//
// Today's real cpt CLI (per focused-engineering/cypilot/.core/workflows/) uses
// `--json` as a top-level flag (e.g. `cpt --json validate --artifact <path>`)
// and slightly different subcommand verbs (`validate`, `list-ids`,
// `chunk-input`, `info`, `update`).  This drift is known
// (today's cypilot CLI may need a --json flag added) and we accept that
// the v1 provider may need a thin adapter in cpt or a Go-side shim.  For
// now we issue commands in the idealized shape; when cpt is absent or the
// op returns a non-zero exit, we surface the stderr verbatim so the story
// can route on_error and the operator can adapt.
//
// # Availability vs. absence
//
// When `cpt` is not on PATH the handler returns a clean Result.Error with
// an installation hint rather than crashing.  The error message is
// deliberately verbose so the operator running this from a non-cypilot
// repo gets actionable guidance:
//
//	"host.cypilot_artifacts: cpt CLI not available — install cypilot from
//	 https://github.com/Acronis/cypilot or run from a checkout that has it on PATH"
//
// All exec calls go through the same `cliExec` seam declared in
// `cli_exec.go`, so tests substitute deterministic runners without
// shelling to a real `cpt` binary.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CypilotArtifactsHandler implements host.cypilot_artifacts (prefix-fallback
// for all 5 artifact ops).  The runtime registry's prefix-fallback means a
// single registration of `host.cypilot_artifacts` satisfies every
// `host.cypilot_artifacts.<op>` dispatch site.
//
// Required args:
//   - op (string): one of list, get, create, validate, decompose.
//
// Optional args (all ops):
//   - workdir (string): working directory for the cpt command; defaults to
//     the process cwd.  Stories thread `world.workdir` here when the
//     artifacts live in a per-task workspace.
//
// Per-op input/output follows the artifact iface schema.
func CypilotArtifactsHandler(ctx context.Context, args map[string]any) (Result, error) {
	op, _ := args["op"].(string)
	op = strings.TrimSpace(op)
	if op == "" {
		return Result{Error: "host.cypilot_artifacts: op argument is required"}, nil
	}
	workdir, _ := args["workdir"].(string)
	if !cptCLIAvailable(ctx, workdir) {
		return Result{Error: "host.cypilot_artifacts: cpt CLI not available — install cypilot from https://github.com/Acronis/cypilot or run from a checkout that has it on PATH"}, nil
	}
	switch op {
	case "list":
		return cptArtifactList(ctx, workdir, args)
	case "get":
		return cptArtifactGet(ctx, workdir, args)
	case "create":
		return cptArtifactCreate(ctx, workdir, args)
	case "validate":
		return cptArtifactValidate(ctx, workdir, args)
	case "decompose":
		return cptArtifactDecompose(ctx, workdir, args)
	default:
		return Result{Error: fmt.Sprintf("host.cypilot_artifacts: unknown op %q", op)}, nil
	}
}

// cptCLIAvailable probes `cpt --version` through the package cliExec seam.
// Returns true iff the binary exists, runs, and exits 0.
func cptCLIAvailable(ctx context.Context, workdir string) bool {
	_, _, code, err := cliExec(ctx, workdir, "cpt", "--version")
	return err == nil && code == 0
}

// ─── Op dispatchers ─────────────────────────────────────────────────────────

// cptArtifactList implements artifact.list via
// `cpt artifact list --kind <k> --json`.
//
// Input  args: kind (string, optional — when omitted every kind is returned).
// Output Data: artifacts ([]{id,kind,title,path,status}).
//
// The handler accepts either a JSON envelope on stdout (preferred — cpt
// produces structured output today via the top-level `--json` flag) or a
// plain-text line-per-artifact fallback (a degenerate envelope cpt might
// emit when `--json` is not supported by the installed version).
func cptArtifactList(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	kind, _ := args["kind"].(string)
	cptArgs := []string{"artifact", "list", "--json"}
	if k := strings.TrimSpace(kind); k != "" {
		cptArgs = append(cptArgs, "--kind", k)
	}
	stdout, stderr, code, err := cliExec(ctx, workdir, "cpt", cptArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("artifact.list: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("artifact.list: %s", strings.TrimSpace(stderr))}, nil
	}
	artifacts, err := parseArtifactList(stdout)
	if err != nil {
		return Result{Error: fmt.Sprintf("artifact.list: parse: %v", err)}, nil
	}
	return Result{Data: map[string]any{"artifacts": artifacts}}, nil
}

// parseArtifactList accepts either:
//   - a JSON array of {id,kind,title,path,status,…} objects, or
//   - a JSON object envelope { "artifacts": [...] } (the shape cpt's
//     `--json` family emits for other subcommands), or
//   - one path per line (the plain-text degenerate fallback) — each line
//     becomes a stub artifact { id, path } so downstream `get` can read it.
func parseArtifactList(stdout string) ([]map[string]any, error) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return []map[string]any{}, nil
	}
	// JSON array — preferred shape.
	if strings.HasPrefix(trimmed, "[") {
		var list []map[string]any
		if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
			return nil, err
		}
		return list, nil
	}
	// JSON object envelope.
	if strings.HasPrefix(trimmed, "{") {
		var env map[string]any
		if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
			return nil, err
		}
		if list, ok := env["artifacts"].([]any); ok {
			out := make([]map[string]any, 0, len(list))
			for _, item := range list {
				if m, ok := item.(map[string]any); ok {
					out = append(out, m)
				}
			}
			return out, nil
		}
		// Single artifact envelope.
		return []map[string]any{env}, nil
	}
	// Plain-text fallback — one path per line.
	out := make([]map[string]any, 0)
	for _, ln := range strings.Split(trimmed, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		out = append(out, map[string]any{
			"path": ln,
			"id":   strings.TrimSuffix(filepath.Base(ln), filepath.Ext(ln)),
		})
	}
	return out, nil
}

// cptArtifactGet implements artifact.get by reading the artifact file
// directly.  The path conventions are owned by
// `cypilot/config/artifacts.toml`; we don't reimplement that parser in
// Go — we just accept the resolved path on the args.
//
// Input  args: id (string), path (string, optional — when set, read
//
//	directly).  When only id is supplied we shell to
//	`cpt artifact path --id <id> --json` to resolve.
//
// Output Data: id, kind, title, body, frontmatter, path, depends_on.
func cptArtifactGet(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	id, _ := args["id"].(string)
	path, _ := args["path"].(string)
	if strings.TrimSpace(id) == "" && strings.TrimSpace(path) == "" {
		return Result{Error: "artifact.get: id or path is required"}, nil
	}
	// Resolve path via cpt if only id was supplied.
	if strings.TrimSpace(path) == "" {
		stdout, stderr, code, err := cliExec(ctx, workdir, "cpt", "artifact", "path", "--id", id, "--json")
		if err != nil {
			return Result{Error: fmt.Sprintf("artifact.get: resolve path: %v", err)}, nil
		}
		if code != 0 {
			return Result{Error: fmt.Sprintf("artifact.get: resolve path: %s", strings.TrimSpace(stderr))}, nil
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &env); err != nil {
			// Fallback: assume stdout is the path.
			path = strings.TrimSpace(stdout)
		} else if p, ok := env["path"].(string); ok {
			path = p
		}
	}
	if strings.TrimSpace(path) == "" {
		return Result{Error: fmt.Sprintf("artifact.get: could not resolve path for id %q", id)}, nil
	}
	// Read the file — under workdir if path is relative.
	abs := path
	if !filepath.IsAbs(path) && workdir != "" {
		abs = filepath.Join(workdir, path)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Error: fmt.Sprintf("artifact.get: %s not found", path)}, nil
		}
		return Result{Error: fmt.Sprintf("artifact.get: read: %v", err)}, nil
	}
	front, body, _ := splitFrontmatter(raw)
	data := map[string]any{
		"id":          id,
		"path":        path,
		"body":        body,
		"frontmatter": front,
		"depends_on":  frontList(front, "depends_on"),
	}
	if title, ok := front["title"].(string); ok {
		data["title"] = title
	}
	if kind, ok := front["kind"].(string); ok {
		data["kind"] = kind
	}
	return Result{Data: data}, nil
}

// cptArtifactCreate implements artifact.create via
// `cpt generate --kind <k> --title <t> --slug <s> --parent <p>`.
//
// Input  args: kind (string, required), title (string, required),
//
//	slug (string, optional), parent_id (string, optional).
//
// Output Data: ok (bool), id (string), path (string).
//
// The handler does not attempt to interpret cpt's interactive prompts —
// interactive drafting is OUT OF SCOPE
// for v1.  cpt is expected to run in a non-interactive mode when given
// `--non-interactive` or similar; until that lands, the operator must
// supply enough flags up-front that cpt does not prompt.  This v1 wraps
// the existing cpt surface as-is — if cpt blocks on a TTY prompt the
// invocation will hang.  Document this in the story flows so authors set
// `interactive: false` (or whatever the eventual cpt flag is) in their
// scenarios; future versions of this provider can pass that flag
// transparently.
func cptArtifactCreate(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	kind, _ := args["kind"].(string)
	title, _ := args["title"].(string)
	slug, _ := args["slug"].(string)
	parentID, _ := args["parent_id"].(string)
	if strings.TrimSpace(kind) == "" {
		return Result{Error: "artifact.create: kind is required"}, nil
	}
	if strings.TrimSpace(title) == "" {
		return Result{Error: "artifact.create: title is required"}, nil
	}
	cptArgs := []string{"generate", "--json", "--kind", kind, "--title", title}
	if slug != "" {
		cptArgs = append(cptArgs, "--slug", slug)
	}
	if parentID != "" {
		cptArgs = append(cptArgs, "--parent", parentID)
	}
	stdout, stderr, code, err := cliExec(ctx, workdir, "cpt", cptArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("artifact.create: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("artifact.create: %s", strings.TrimSpace(stderr))}, nil
	}
	// cpt is expected to emit a JSON envelope on stdout (last line) with
	// `id` + `path`.  When it does not we fall back to deriving an id from
	// the slug or returning whatever the trailing line contains.
	last := lastNonEmptyLine(stdout)
	id, path := "", ""
	if last != "" && (strings.HasPrefix(last, "{") || strings.HasPrefix(last, "[")) {
		var env map[string]any
		if err := json.Unmarshal([]byte(last), &env); err == nil {
			if v, ok := env["id"].(string); ok {
				id = v
			}
			if v, ok := env["path"].(string); ok {
				path = v
			}
		}
	}
	if id == "" {
		// Best-effort derivation from inputs.
		id = fmt.Sprintf("cpt-%s-%s", kind, slug)
	}
	// Nest the result under `artifact` so a story room can bind the
	// whole envelope into one world slot via:
	//   bind: { prd_artifact: artifact }
	// while still allowing specific-field binds (id, path) for callers
	// that only need scalars.
	artifact := map[string]any{
		"id":   id,
		"path": path,
		"kind": kind,
	}
	return Result{Data: map[string]any{
		"ok":       true,
		"id":       id,
		"path":     path,
		"artifact": artifact,
	}}, nil
}

// cptArtifactValidate implements artifact.validate via
// `cpt analyze --target <id> --mode <m>` (the proposal's idealized form).
//
// Input  args: id (string, required), mode (string, optional — one of
//
//	"deterministic" | "semantic" | "consistency").
//
// Output Data: ok (bool), findings (list), report (string).
//
// The "report" key is the raw cpt stdout — useful for the LLM-judge prompt
// to read.  The "findings" list is parsed from a JSON envelope when cpt
// emits one; otherwise it's empty and the caller relies on `ok` + report.
func cptArtifactValidate(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	id, _ := args["id"].(string)
	mode, _ := args["mode"].(string)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "artifact.validate: id is required"}, nil
	}
	cptArgs := []string{"analyze", "--json", "--target", id}
	if m := strings.TrimSpace(mode); m != "" {
		cptArgs = append(cptArgs, "--mode", m)
	}
	stdout, stderr, code, err := cliExec(ctx, workdir, "cpt", cptArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("artifact.validate: exec: %v", err)}, nil
	}
	// Non-zero exit from cpt analyze is the canonical "findings present"
	// signal in cypilot's existing workflows.  We surface ok=false but
	// keep `report` populated so the judge prompt can read it; the caller
	// gates on `ok` not on Result.Error.
	report := stdout
	if report == "" {
		report = stderr
	}
	findings := parseAnalyzeFindings(stdout)
	return Result{Data: map[string]any{
		"ok":       code == 0,
		"findings": findings,
		"report":   report,
	}}, nil
}

// parseAnalyzeFindings extracts the `findings` list from cpt analyze's
// envelope when one is present.  Cypilot's analyze workflow emits a JSON
// envelope per `cpt --json validate` of the form
//
//	{"status":"PASS","findings":[...]}
//
// or
//
//	{"status":"FAIL","findings":[{...}, ...]}
//
// We accept either; non-JSON stdout yields an empty list.
func parseAnalyzeFindings(stdout string) []any {
	trimmed := strings.TrimSpace(stdout)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return []any{}
	}
	last := lastNonEmptyLine(trimmed)
	if last == "" {
		return []any{}
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(last), &env); err != nil {
		return []any{}
	}
	if list, ok := env["findings"].([]any); ok {
		return list
	}
	return []any{}
}

// cptArtifactDecompose implements artifact.decompose via
// `cpt plan --task <id>` (the proposal's idealized form).
//
// Input  args: id (string, required — a PRD or DECOMPOSITION id).
// Output Data: ok (bool), plan_path (string), phase_count (int).
//
// cpt plan writes `.plans/<slug>/phase-NN-*.md` files under the repo;
// we read the JSON envelope cpt emits to surface the directory + phase
// count back to the story.
func cptArtifactDecompose(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "artifact.decompose: id is required"}, nil
	}
	stdout, stderr, code, err := cliExec(ctx, workdir, "cpt", "plan", "--json", "--task", id)
	if err != nil {
		return Result{Error: fmt.Sprintf("artifact.decompose: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("artifact.decompose: %s", strings.TrimSpace(stderr))}, nil
	}
	planPath, phaseCount := parseDecomposeEnvelope(stdout)
	artifact := map[string]any{
		"plan_path":   planPath,
		"phase_count": phaseCount,
		"kind":        "decomposition",
	}
	return Result{Data: map[string]any{
		"ok":          true,
		"plan_path":   planPath,
		"phase_count": phaseCount,
		"artifact":    artifact,
	}}, nil
}

// parseDecomposeEnvelope extracts plan_path + phase_count from cpt plan's
// stdout when a JSON envelope is present.  Falls back to scanning the
// output for a recognisable `.plans/<slug>/` path token when cpt prints
// human-readable progress instead.
func parseDecomposeEnvelope(stdout string) (string, int) {
	trimmed := strings.TrimSpace(stdout)
	last := lastNonEmptyLine(trimmed)
	if last != "" && (strings.HasPrefix(last, "{") || strings.HasPrefix(last, "[")) {
		var env map[string]any
		if err := json.Unmarshal([]byte(last), &env); err == nil {
			path, _ := env["plan_path"].(string)
			count := 0
			switch v := env["phase_count"].(type) {
			case float64:
				count = int(v)
			case int:
				count = v
			}
			return path, count
		}
	}
	// Best-effort scan for a `.plans/` token in the stdout.
	for _, ln := range strings.Split(trimmed, "\n") {
		if idx := strings.Index(ln, ".plans/"); idx >= 0 {
			// Capture everything from `.plans/` to end-of-token.
			tok := ln[idx:]
			if sp := strings.IndexAny(tok, " \t"); sp > 0 {
				tok = tok[:sp]
			}
			return tok, 0
		}
	}
	return "", 0
}

// frontList projects a frontmatter list-valued key (e.g. depends_on) into
// a []any, accepting both YAML-decoded []any and []string shapes.  Missing
// or wrong-typed keys yield an empty list.
func frontList(front map[string]any, key string) []any {
	if front == nil {
		return []any{}
	}
	v, ok := front[key]
	if !ok {
		return []any{}
	}
	if list, ok := v.([]any); ok {
		return list
	}
	if list, ok := v.([]string); ok {
		out := make([]any, len(list))
		for i, s := range list {
			out[i] = s
		}
		return out
	}
	return []any{}
}
