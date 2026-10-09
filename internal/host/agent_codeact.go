// Package host — host.agent.codeact handler.
//
// host.agent.codeact drives the bounded code-act loop in internal/host/codeact:
// instead of a fixed tool menu, the agent emits a Starlark snippet each step,
// observes its structured result, and eventually terminates by emitting
// done(payload) that must conform to the declared schema. See
// internal/host/codeact/executor.go for the loop itself and
// docs/architecture/starlark.md and docs/architecture/hosts.md#hostagentcodeact
// for the author-facing contract.
//
// Args:
//   - agent: (string) — named agent from the agents: block.
//   - goal:  (string) — the natural-language objective handed to the agent.
//   - capabilities: mapping — the shared Starlark capability
//     authority for each emitted snippet. The loader validates the shape and
//     the runtime exposes/enforces exactly the granted ctx surfaces.
//   - budget: (int, optional) — max steps before TerminatedBudgetExhausted;
//     defaults to 5 when absent or non-positive.
//   - schema: (string, optional) — path to the JSON schema validating done() payloads,
//     resolved overlay-first via resolvePromptPathCtx (same convention as
//     host.agent.task's acceptance.schema). When set, a schema-invalid done()
//     payload is rejected and fed back to the agent as an ErrorEnvelope
//     instead of terminating the loop. Optional — when omitted, done()
//     payloads are accepted unvalidated.
//
// Returns Result.Data with:
//   - terminated (string): "done" | "budget_exhausted".
//   - payload    (map[string]any): the schema-valid done() payload (empty
//     when terminated == "budget_exhausted").
//   - steps      ([]any): a journal of each step's snippet + observation, for
//     tracing/debugging.
//
// v1 STATUS: the Agent wired in here (RealCodeactAgent, see
// agent_codeact_real.go) drives one ONE-SHOT `claude -p` call per step,
// composing the Codeact verb contract (sysprompt.Codeact) plus a per-step
// addendum (goal, remaining budget, granted capability names, and the last
// observation or structured error), and reads back a discriminated-union
// {"action":"snippet"|"done",...} verdict through the same mcp-validator
// submit mechanism host.agent.decide uses.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	kitsokimcp "kitsoki/internal/mcp"

	"kitsoki/internal/host/codeact"
	starlarkhost "kitsoki/internal/host/starlark"
)

// defaultCodeactBudget is used when with.budget is absent or non-positive.
const defaultCodeactBudget = 5

// AgentCodeactHandler implements host.agent.codeact.
func AgentCodeactHandler(ctx context.Context, args map[string]any) (Result, error) {
	agentName, _ := args["agent"].(string)
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return Result{Error: "host.agent.codeact: agent: argument is required — declare a named agent in the agents: block", FailureKind: FailureFatal}, nil
	}
	agent, agentOK := resolveAgent(ctx, args)
	if !agentOK {
		return Result{Error: fmt.Sprintf("host.agent.codeact: unknown agent %q — check the agents: block in app.yaml", agentName), FailureKind: FailureFatal}, nil
	}

	goal, _ := args["goal"].(string)
	if strings.TrimSpace(goal) == "" {
		return Result{Error: "host.agent.codeact: goal: argument is required", FailureKind: FailureFatal}, nil
	}
	workingDir, _ := args["working_dir"].(string)
	workingDir = appendDefaultCwd(workingDir, agent)
	if _, policyErr := RequireAgentLaunchAllowed(ctx, "codeact", agentName, workingDir); policyErr != "" {
		return Result{Error: "host.agent.codeact: " + policyErr, FailureKind: FailureFatal}, nil
	}

	budget := defaultCodeactBudget
	switch v := args["budget"].(type) {
	case int:
		if v > 0 {
			budget = v
		}
	case int64:
		if v > 0 {
			budget = int(v)
		}
	case float64:
		if v > 0 {
			budget = int(v)
		}
	}

	capabilities, capErr := starlarkhost.ParseCapabilities(args["capabilities"])
	if capErr != nil {
		return Result{Error: fmt.Sprintf("host.agent.codeact: capabilities: %v", capErr), FailureKind: FailureFatal}, nil
	}
	capabilityLabels := capabilities.CapabilityLabels()

	worldArg, _ := args["world"].(map[string]any)
	runCtx, capCtxErr := codeactCapabilityContext(ctx, args, worldArg, capabilities)
	if capCtxErr != nil {
		return Result{Error: fmt.Sprintf("host.agent.codeact: %v", capCtxErr), FailureKind: FailureFatal}, nil
	}

	var schemaFn func(payload map[string]any) error
	if schemaPath, _ := args["schema"].(string); strings.TrimSpace(schemaPath) != "" {
		fn, err := compileCodeactSchema(ctx, schemaPath)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.agent.codeact: %v", err), FailureKind: FailureFatal}, nil
		}
		schemaFn = fn
	}

	// From here on the call is committed to dispatch: emit the same
	// agent.call.start / agent.call.complete / agent.call.error trace events
	// task/ask/decide emit (via appendAgentCalledEvent/appendAgentReturnedEvent/
	// appendAgentErrorEvent), so codeact calls are visible to any consumer that
	// reads those event kinds (e.g. analyze.py, agentbench). Before this fix,
	// host.agent.codeact emitted NONE of these — a codeact call was invisible
	// to cost/token rollups that only look at agent.call.* events (the M1
	// accounting gap in
	// .context/2026-07-11-gx10-small-model-study-adversarial-review.md).
	// callID is threaded onto ctx (and therefore into runCtx, which derives
	// from ctx) so the per-step claude calls inside the loop tee their usage
	// into the same box AgentReturned/AgentError read from at the end.
	callID := newUUID()
	callStart := time.Now()
	ctx = WithCallID(ctx, callID)
	runCtx = WithCallID(runCtx, callID)
	runtimeKind := codeactRuntimeKind(ctx)
	appendAgentCalledEvent(ctx, callStart, callID, goal, AgentCalledPayload{
		Verb:        "codeact",
		Agent:       agentName,
		Model:       agent.Model,
		RuntimeKind: runtimeKind,
		Input:       marshalInput(map[string]any{"budget": budget, "capabilities": capabilityLabels}),
	})

	finish := func(res Result, err error) (Result, error) {
		callEnd := time.Now()
		durationMS := callEnd.Sub(callStart).Milliseconds()
		if err != nil {
			appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
				Verb:       "codeact",
				Agent:      agentName,
				DurationMS: durationMS,
				Error:      err.Error(),
			})
			return res, err
		}
		if res.Error != "" {
			appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
				Verb:       "codeact",
				Agent:      agentName,
				DurationMS: durationMS,
				Error:      res.Error,
			})
			return res, nil
		}
		appendAgentReturnedEvent(ctx, callEnd, callID, AgentReturnedPayload{
			Verb:       "codeact",
			Agent:      agentName,
			Model:      agent.Model,
			DurationMS: durationMS,
			Response:   marshalResponse(res.Data),
		})
		return res, nil
	}

	// Direct-API path: when the effect's resolved plugin is a builtin.local_llm
	// transport (an OpenAI-compatible HTTP endpoint, e.g. GLM-5.2), drive the
	// loop over HTTP via ApiCodeactAgent instead of forking claude -p per step.
	// Opt-in: only when a plugin is named AND it resolves to a local_llm
	// backend. The default (no plugin named, or a claude_cli plugin) stays on
	// the CLI path below — IsLocalLLM("") and IsLocalLLM("agent.claude") are
	// both false, so existing stories are byte-identical. See
	// agent_codeact_api.go and .context/diy-harness-sandboxing-brief.md item 1.
	if runtimeKind == "direct_api" {
		if reg := AgentRegistryFromCtx(ctx); reg != nil {
			pluginName := AgentPluginNameFromCtx(ctx)
			plug, perr := reg.Resolve(pluginName)
			if perr != nil {
				return finish(Result{Error: fmt.Sprintf("host.agent.codeact: resolve api plugin %q: %v", pluginName, perr), FailureKind: FailureInfra}, nil)
			}
			res, err := runCodeactLoop(runCtx, worldArg, schemaFn, newApiCodeactAgent(ctx, plug, args, goal, budget, capabilityLabels), budget, capabilities)
			return finish(res, err)
		}
	}

	agentImpl, cleanup, err := newRealCodeactAgent(ctx, args, goal, budget, capabilityLabels)
	if err != nil {
		return finish(Result{Error: fmt.Sprintf("host.agent.codeact: %v", err), FailureKind: FailureInfra}, nil)
	}
	defer cleanup()
	res, err := runCodeactLoop(runCtx, worldArg, schemaFn, agentImpl, budget, capabilities)
	return finish(res, err)
}

// codeactRuntimeKind distinguishes CodeAct's two real execution boundaries
// before tracing the outer call. The CLI adapter launches a supervised local
// generator process for every step; builtin.local_llm makes direct API calls
// and therefore has no subprocess receipt to pair. Keeping this fact on the
// outer call lets AgentBench reject a CLI trace with missing/mismatched runtime
// events without misclassifying direct API executions.
func codeactRuntimeKind(ctx context.Context) string {
	if reg := AgentRegistryFromCtx(ctx); reg != nil && reg.IsLocalLLM(AgentPluginNameFromCtx(ctx)) {
		return "direct_api"
	}
	return "cli"
}

func codeactCapabilityContext(ctx context.Context, args map[string]any, worldArg map[string]any, capabilities starlarkhost.CapabilitySpec) (context.Context, error) {
	runCtx := ctx
	if capabilities.RequiresInjectedHTTP() && !starlarkhost.HasHTTPClient(runCtx) {
		return nil, fmt.Errorf("capabilities.http.cassette_required requires an injected Starlark HTTP client")
	}
	if capabilities.NeedsHTTP() && !starlarkhost.HasHTTPClient(runCtx) {
		runCtx = starlarkhost.WithHTTP(runCtx, starlarkhost.NewRecordingClient())
	}
	if capabilities.NeedsInspector() && !starlarkhost.HasInspector(runCtx) {
		root, _ := args["working_dir"].(string)
		if root == "" {
			root, _ = worldArg["workdir"].(string)
		}
		if root == "" || root == "." {
			if pr := PromptRendererFromCtx(ctx); pr != nil {
				if appDir := pr.RootDir(); appDir != "" {
					root = widenToRepoRoot(appDir)
				}
			}
		}
		if root == "" {
			if appDir := os.Getenv(AppDirEnv); appDir != "" {
				root = widenToRepoRoot(appDir)
			}
		}
		if root == "" {
			if cwd, err := os.Getwd(); err == nil {
				root = cwd
			}
		}
		runCtx = starlarkhost.WithInspector(runCtx, starlarkhost.NewProductionInspector(root))
	}
	return runCtx, nil
}

// runCodeactLoop runs codeact.Run over impl and shapes the Result identically
// for both the CLI (RealCodeactAgent) and direct-API (ApiCodeactAgent) paths:
// terminated (done|budget_exhausted), the schema-valid done() payload, and a
// per-step journal of snippet + observation (+ error). Keeping the shaping in
// one place guarantees the two backends produce the same Result.Data shape.
func runCodeactLoop(ctx context.Context, worldArg map[string]any, schemaFn func(map[string]any) error, impl codeact.Agent, budget int, capabilities starlarkhost.CapabilitySpec) (Result, error) {
	res, err := codeact.Run(ctx, codeact.Params{
		Budget:       budget,
		World:        worldArg,
		Agent:        impl,
		Schema:       schemaFn,
		Capabilities: capabilities,
	})
	if err != nil {
		return Result{}, fmt.Errorf("host.agent.codeact: %w", err)
	}

	steps := make([]any, 0, len(res.Steps))
	for _, s := range res.Steps {
		step := map[string]any{
			"snippet":     s.Emission.Snippet,
			"observation": s.Observation,
		}
		if s.Err != nil {
			step["error"] = s.Err.Message
		}
		steps = append(steps, step)
	}

	return Result{
		Data: map[string]any{
			"terminated": res.Terminated,
			"payload":    res.Payload,
			"steps":      steps,
		},
	}, nil
}

// compileCodeactSchema loads and compiles the JSON schema at schemaPath
// (resolved overlay-first via resolvePromptPathCtx, the same convention
// host.agent.task's acceptance.schema and host.agent.decide's schema use) and
// returns a codeact.Params.Schema validator func. The returned func marshals
// the candidate done() payload to JSON and validates it against the compiled
// schema, so a schema-invalid payload is rejected and fed back to the agent
// as an ErrorEnvelope instead of silently terminating the loop.
func compileCodeactSchema(ctx context.Context, schemaPath string) (func(map[string]any) error, error) {
	resolved := resolvePromptPathCtx(ctx, schemaPath)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("schema %q not found: %w", resolved, err)
	}
	compiled, err := kitsokimcp.CompileSchema(data)
	if err != nil {
		return nil, fmt.Errorf("compile schema %q: %w", resolved, err)
	}
	return func(payload map[string]any) error {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("unmarshal payload: %w", err)
		}
		if err := compiled.Validate(doc); err != nil {
			return fmt.Errorf("%s", kitsokimcp.FormatValidationError(err))
		}
		return nil
	}, nil
}
