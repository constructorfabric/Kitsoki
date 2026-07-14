// Materialize gate checks — the deterministic Starlark assertions a type's
// materialize.checks: declaration binds to its nodes (graph.
// MaterializeCheckDecl). A node's prose gate: field says what "done" means
// to a human; a check is the machine judgment: a sandboxed .star script
// (host.StarlarkRunHandler — no exec, no clock, capability-gated fs/http)
// that returns {ok: bool, reasons: list} over the node's declared inputs.
//
// Checks run in the DRIVER (driveHandler), not in the bound story's rooms:
// the private materialize rig deliberately has no host registry, and running
// them engine-side means every bound story is judged the same way — a story
// cannot skip its own verification. They run after the story's rooms as
// extra stages ("check:<id>" pills), and a false or unresolvable check fails
// the job.
//
// Every result carries the script's sha256 and the exact `kitsoki starlark
// run` command that reproduces the judgment from a shell — the trace/
// reproduce contract: the same script + same inputs is the same verdict,
// with no agent or LLM in the loop.
package materialize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"kitsoki/internal/graph"
	"kitsoki/internal/host"
)

// ResolvedCheck is one materialize check bound to a concrete node: the
// script path settled (type-provided Script or the node's ScriptField
// value) and the inputs merged (decl literals overridden by the node's
// InputsField map). Unresolved carries the reason when the node cannot
// satisfy the declaration (e.g. the script_field is unset) — such a check
// still occupies a stage and fails the job, so "no assertion" is never
// silently equivalent to "assertion passed".
type ResolvedCheck struct {
	ID           string
	Script       string // repo-root-relative .star path; empty when Unresolved
	Inputs       map[string]any
	Capabilities map[string]any
	Unresolved   string
}

// CheckResult is the recorded outcome of running one resolved check.
type CheckResult struct {
	ID           string         `json:"id"`
	Script       string         `json:"script"`
	ScriptSHA256 string         `json:"script_sha256,omitempty"`
	Inputs       map[string]any `json:"inputs,omitempty"`
	OK           bool           `json:"ok"`
	Reasons      []string       `json:"reasons,omitempty"`
	Output       map[string]any `json:"output,omitempty"`
	Error        string         `json:"error,omitempty"`
	Reproduce    string         `json:"reproduce,omitempty"`
}

// CheckStagePrefix namespaces check stages in a job's stage list so the
// portal (and the writeback record) can tell rooms from checks.
const CheckStagePrefix = "check:"

// ResolveChecks binds a type's materialize.checks declarations to node.
// Resolution is pure — no filesystem, no evaluation — so both Prepare (which
// must not fail on a bad check; the check stage fails instead) and the
// pre-flight RPC use it identically.
func ResolveChecks(node *graph.Node, decls []graph.MaterializeCheckDecl) []ResolvedCheck {
	out := make([]ResolvedCheck, 0, len(decls))
	for _, d := range decls {
		rc := ResolvedCheck{ID: d.ID, Capabilities: d.Capabilities}

		script := d.Script
		if d.ScriptField != "" {
			raw := node.Fields[d.ScriptField]
			s, _ := raw.(string)
			if strings.TrimSpace(s) == "" {
				rc.Unresolved = fmt.Sprintf("node field %q names no .star assertion script (a work item must declare its gate check)", d.ScriptField)
				out = append(out, rc)
				continue
			}
			script = s
		}
		rc.Script = script

		inputs := map[string]any{}
		for k, v := range d.Inputs {
			inputs[k] = v
		}
		if d.InputsField != "" {
			if m, ok := node.Fields[d.InputsField].(map[string]any); ok {
				for k, v := range m {
					inputs[k] = v
				}
			}
		}
		rc.Inputs = inputs
		out = append(out, rc)
	}
	return out
}

// CheckStages returns the stage ids checks occupy, in declaration order.
func CheckStages(checks []ResolvedCheck) []string {
	out := make([]string, len(checks))
	for i, c := range checks {
		out[i] = CheckStagePrefix + c.ID
	}
	return out
}

// RunCheck evaluates one resolved check under repoRoot and returns its
// recorded outcome. It never returns a Go error: every failure mode —
// unresolved declaration, unreadable script, sandbox domain error, missing
// or non-bool ok output — is a failed CheckResult with the reason spelled
// out, because "could not judge" must fail the gate, not skip it.
func RunCheck(ctx context.Context, repoRoot string, rc ResolvedCheck) CheckResult {
	res := CheckResult{ID: rc.ID, Script: rc.Script, Inputs: rc.Inputs}
	if rc.Unresolved != "" {
		res.Error = rc.Unresolved
		res.Reasons = []string{rc.Unresolved}
		return res
	}

	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		res.Error = fmt.Sprintf("resolve repo root: %v", err)
		res.Reasons = []string{res.Error}
		return res
	}
	absScript := rc.Script
	if !filepath.IsAbs(absScript) {
		absScript = filepath.Join(absRoot, rc.Script)
	}
	if raw, err := os.ReadFile(absScript); err == nil {
		sum := sha256.Sum256(raw)
		res.ScriptSHA256 = hex.EncodeToString(sum[:])
	} else {
		res.Error = fmt.Sprintf("read check script: %v", err)
		res.Reasons = []string{res.Error}
		return res
	}
	res.Reproduce = reproduceCommand(rc)

	args := map[string]any{
		"script": absScript,
		"inputs": rc.Inputs,
		// Pin the sandbox's fs/probe root to the materialize repo root via
		// the world.workdir override — the FIRST root the handler consults
		// (starlark_run.go's inspector-injection chain). Without this the
		// root falls back to the process-global KITSOKI_APP_DIR, which any
		// concurrently seeded session may have pointed at a DIFFERENT repo
		// (its docstring calls out exactly this race), silently making the
		// check judge against the wrong tree: the web-session materialize
		// path reported "no evidence recorded" for evidence that existed,
		// while the CLI path — same catalog, same check — read it fine.
		// A gate verdict must not depend on which session ran last.
		"world": map[string]any{"workdir": absRoot},
	}
	if len(rc.Capabilities) > 0 {
		args["capabilities"] = rc.Capabilities
	}
	result, err := host.StarlarkRunHandler(ctx, args)
	if err != nil {
		res.Error = fmt.Sprintf("run check script: %v", err)
		res.Reasons = []string{res.Error}
		return res
	}
	if result.Error != "" {
		res.Error = result.Error
		res.Reasons = []string{result.Error}
		return res
	}
	res.Output = result.Data

	okVal, has := result.Data["ok"]
	okBool, isBool := okVal.(bool)
	if !has || !isBool {
		res.Error = fmt.Sprintf("check script %s must return a bool `ok` output", rc.Script)
		res.Reasons = []string{res.Error}
		return res
	}
	res.OK = okBool
	if reasons, ok := result.Data["reasons"].([]any); ok {
		for _, r := range reasons {
			res.Reasons = append(res.Reasons, fmt.Sprint(r))
		}
	}
	return res
}

// reproduceCommand renders the shell command that re-runs the check with
// byte-identical inputs — the reproducibility receipt shown in the portal
// and written into the node's materialization record.
func reproduceCommand(rc ResolvedCheck) string {
	var b strings.Builder
	fmt.Fprintf(&b, "kitsoki starlark run %s", rc.Script)
	if len(rc.Inputs) > 0 {
		b.WriteString(" --inputs '" + canonicalJSON(rc.Inputs) + "'")
	}
	if len(rc.Capabilities) > 0 {
		b.WriteString(" --capabilities '" + canonicalJSON(rc.Capabilities) + "'")
	}
	return b.String()
}

// canonicalJSON marshals with sorted keys (encoding/json sorts map keys) so
// the reproduce command is stable across runs.
func canonicalJSON(m map[string]any) string {
	raw, err := json.Marshal(m)
	if err != nil {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return fmt.Sprintf("<unmarshalable inputs: %v>", keys)
	}
	return string(raw)
}
