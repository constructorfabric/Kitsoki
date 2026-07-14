// Package host — the production, LLM-backed codeact.Agent.
//
// RealCodeactAgent implements internal/host/codeact.Agent with ONE-SHOT `claude
// -p` calls: each Next() composes a per-step prompt (goal, remaining budget,
// granted capability names, and the last observation/error), dispatches a
// single claude call through the SAME machinery host.agent.decide uses
// (buildBaseCLIArgs + AgentStreamer + the kitsoki mcp-validator submit tool),
// and parses the validated JSON back into a codeact.Emission. There is no
// retry loop here — codeact.Run already IS the retry loop, feeding a failed
// step's ErrorEnvelope back into the next Next() call so the model can
// self-correct.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"kitsoki/internal/host/agentruntime"
	"kitsoki/internal/host/codeact"
	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/sysprompt"
)

// codeactStepSchemaJSON is the fixed discriminated-union schema every codeact
// step's submit() call must satisfy: either a Starlark snippet to run next,
// or a done() call carrying the candidate final payload. It is the same
// shape for every codeact call (the author never declares it), so it is
// built inline rather than requiring a schema file on disk.
const codeactStepSchemaJSON = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "action": {"type": "string", "enum": ["snippet", "done"]},
    "snippet": {"type": "string"},
    "payload": {"type": "object"}
  },
  "required": ["action"],
  "if": {"properties": {"action": {"const": "snippet"}}},
  "then": {"required": ["action", "snippet"]},
  "else": {"required": ["action", "payload"]}
}`

// codeactCapabilityDescriptions gives the model a one-line, human-readable
// gloss for each v1 capability name — not a generated signature, just enough
// to let it reason about what ctx surface it was granted. Names outside this
// map (should not happen; the loader validates against the same allowlist)
// fall back to the bare name.
var codeactCapabilityDescriptions = map[string]string{
	"world":         "world — read-only access to ctx.world.get(key) for the current world state.",
	"vcs":           "vcs — read-only version-control probes through ctx.probe(\"git.status\") and ctx.probe(\"git.ls_files\", [pathspec]).",
	"http":          "http — outbound HTTP requests via ctx.http, subject to configured method/host policy and cassettes.",
	"fs.read":       "fs.read — read-only filesystem inspection through ctx.fs.read/exists/glob within granted path patterns.",
	"fs.write":      "fs.write — small filesystem writes through ctx.fs.write within granted path patterns.",
	"github.issues": "github.issues — GitHub issue reads through ctx.probe(\"gh.issue.list\", [owner_repo]) with token/cassette policy.",
	"host":          "host — exact engine host verbs through ctx.host.call(name, args).",
}

// RealCodeactAgent is the production codeact.Agent: it holds everything
// resolved once at construction (the agent config, provider/ladder-adjusted
// ctx, resolved binary, base CLI args, and the step-schema tempfile) so each
// Next() call only has to build its own per-step prompt and MCP config.
type RealCodeactAgent struct {
	ctx            context.Context
	bin            string
	baseCLIArgs    []string
	workingDir     string
	schemaPath     string
	goal           string
	budget         int
	capabilities   []string
	runtimeSandbox *AgentSandboxSpec
}

// codeactCLIRuntimeSandbox is deliberately internal rather than a story YAML
// sandbox: CodeAct's action boundary is its explicit Starlark capabilities,
// while this policy supervises the separate model-generator process. Every
// CLI-backed step must enter this registry-selected baseline or fail closed.
func codeactCLIRuntimeSandbox() *AgentSandboxSpec {
	return &AgentSandboxSpec{
		MinStrength: agentruntime.StrengthSupervised,
		Repo:        agentruntime.RepoNone,
		Network:     agentruntime.NetworkModelOnly,
		Resources: agentruntime.ResourcePolicy{
			Timeout:         15 * time.Minute,
			ActivityTimeout: 90 * time.Second,
		},
		Degrade: agentruntime.DegradeFail,
	}
}

// newRealCodeactAgent resolves the named agent (provider + ladder rung
// applied once, mirroring agent_decide.go), materializes the fixed
// discriminated-union step schema to a tempfile, and returns a ready
// RealCodeactAgent plus a cleanup func the caller must defer.
func newRealCodeactAgent(ctx context.Context, args map[string]any, goal string, budget int, capabilities []string) (*RealCodeactAgent, func(), error) {
	agent, ok := resolveAgent(ctx, args)
	if !ok {
		return nil, nil, fmt.Errorf("unknown agent %q", agentNameFromArgs(args))
	}
	ctx, agent = applyProvider(ctx, args, agent)
	ctx, agent = applyLadderRung(ctx, agent)

	bin, err := resolveAgentBin(ctx)
	if err != nil {
		return nil, nil, err
	}

	workingDir, _ := args["working_dir"].(string)
	workingDir = appendDefaultCwd(workingDir, agent)

	cliArgs := buildBaseCLIArgs(ctx, sysprompt.Codeact, args, agent)

	schemaFile, err := os.CreateTemp("", "kitsoki-codeact-schema-*.json")
	if err != nil {
		return nil, nil, fmt.Errorf("create codeact step-schema tempfile: %w", err)
	}
	if _, err := schemaFile.WriteString(codeactStepSchemaJSON); err != nil {
		_ = schemaFile.Close()
		_ = os.Remove(schemaFile.Name())
		return nil, nil, fmt.Errorf("write codeact step-schema tempfile: %w", err)
	}
	_ = schemaFile.Close()
	schemaPath := schemaFile.Name()
	cleanup := func() { _ = os.Remove(schemaPath) }

	return &RealCodeactAgent{
		ctx:            ctx,
		bin:            bin,
		baseCLIArgs:    cliArgs,
		workingDir:     workingDir,
		schemaPath:     schemaPath,
		goal:           goal,
		budget:         budget,
		capabilities:   capabilities,
		runtimeSandbox: codeactCLIRuntimeSandbox(),
	}, cleanup, nil
}

// Next implements codeact.Agent. It ignores the ctx parameter it is passed
// in favor of the provider/ladder-adjusted ctx captured at construction —
// codeact.Run always passes through the same context object it was given, so
// the two are equivalent except that the stored one additionally carries the
// provider/ladder overrides applied once in newRealCodeactAgent.
func (a *RealCodeactAgent) Next(_ context.Context, step int, obs map[string]any, errEnv *codeact.ErrorEnvelope) (codeact.Emission, error) {
	outFile, err := os.CreateTemp("", "kitsoki-codeact-validated-*.json")
	if err != nil {
		return codeact.Emission{}, fmt.Errorf("codeact step %d: create validator output tempfile: %w", step, err)
	}
	outputPath := outFile.Name()
	_ = outFile.Close()
	_ = os.Remove(outputPath)
	defer os.Remove(outputPath)

	validatorEntry, err := buildValidatorMCPServer(a.ctx, a.schemaPath, outputPath, validatorOptions{})
	if err != nil {
		return codeact.Emission{}, fmt.Errorf("codeact step %d: build validator MCP server: %w", step, err)
	}
	mcpConfigPath, cleanup, err := writeMCPConfigTempfile(map[string]any{"validator": validatorEntry}, "kitsoki-codeact-mcp")
	if err != nil {
		return codeact.Emission{}, fmt.Errorf("codeact step %d: %w", step, err)
	}
	defer cleanup()

	stepArgs := append(append([]string{}, a.baseCLIArgs...), "--mcp-config", mcpConfigPath)
	stdin := a.buildStepPrompt(step, obs, errEnv)

	stepCtx := withCodeactRuntimeStep(a.ctx, step)
	cr, _, runErr := AgentStreamer{
		Bin:        a.bin,
		CLIArgs:    stepArgs,
		Stdin:      stdin,
		WorkingDir: a.workingDir,
		Sandbox:    a.runtimeSandbox,
	}.Run(stepCtx)
	if runErr != nil {
		return codeact.Emission{}, fmt.Errorf("codeact step %d: claude exec failed: %w", step, runErr)
	}
	if cr.Infra != nil {
		return codeact.Emission{}, fmt.Errorf("codeact step %d: %w", step, cr.Infra)
	}

	stepOut := readCodeactSubmission(outputPath, cr.Stdout)
	if stepOut == nil {
		if cr.ExitCode != 0 {
			return codeact.Emission{}, fmt.Errorf("codeact step %d: %s", step, claudeExitErrorMessage(cr.ExitCode, cr.Stderr, cr.Stdout))
		}
		return codeact.Emission{}, fmt.Errorf("codeact step %d: model exited without calling submit with a snippet/done action", step)
	}

	action, _ := stepOut["action"].(string)
	switch action {
	case "snippet":
		snippet, _ := stepOut["snippet"].(string)
		if strings.TrimSpace(snippet) == "" {
			return codeact.Emission{}, fmt.Errorf("codeact step %d: action=snippet but snippet is empty", step)
		}
		return codeact.Emission{Snippet: snippet}, nil
	case "done":
		payload, _ := stepOut["payload"].(map[string]any)
		return codeact.Emission{Done: true, Payload: payload}, nil
	default:
		return codeact.Emission{}, fmt.Errorf("codeact step %d: unknown action %q", step, action)
	}
}

// readCodeactSubmission reads the schema-validated payload the mcp-validator
// captured at outputPath. If nothing was captured (the model narrated JSON
// instead of calling the submit tool), it falls back to recovering a
// fenced ```json code block from stdout — the same tool-bypass recovery
// agent_decide.go's retry loop uses. Returns nil when neither source yields
// a JSON object.
func readCodeactSubmission(outputPath, stdout string) map[string]any {
	if data, err := kitsokimcp.ReadCapturedPayload(outputPath); err == nil && len(data) > 0 {
		var parsed map[string]any
		if json.Unmarshal(data, &parsed) == nil {
			return parsed
		}
	}
	if extracted := extractJSONFromCodeBlock(stdout); extracted != nil {
		if m, ok := extracted.(map[string]any); ok {
			return m
		}
	}
	return nil
}

// buildStepPrompt composes the per-step addendum sent as stdin: the goal,
// remaining budget, granted capability names, and the previous step's
// observation or structured error (mutually exclusive — codeact.Run only
// ever sets one). The Codeact verb contract itself (sysprompt.Codeact) is
// already folded into the system prompt by buildBaseCLIArgs, so this text
// only needs to carry what changes step to step.
func (a *RealCodeactAgent) buildStepPrompt(step int, obs map[string]any, errEnv *codeact.ErrorEnvelope) string {
	// The goal/budget/capabilities/observation/error framing is shared with the
	// direct-API agent (codeactStepContext, agent_codeact_api.go) so the two
	// paths cannot drift on the per-step context; only the submission
	// instruction suffix differs (the CLI path tells the model to call the
	// validator's submit tool, the API path tells it to respond with JSON).
	return codeactStepContext(a.goal, a.budget, step, a.capabilities, obs, errEnv) +
		"\nRespond by calling the validator's submit tool with exactly one of:\n" +
		"  {\"action\": \"snippet\", \"snippet\": \"<Starlark source defining def main(ctx): ...>\"}\n" +
		"  {\"action\": \"done\", \"payload\": {<final result fields>}}\n" +
		"Do not call done() until you are confident the goal is satisfied.\n"
}
