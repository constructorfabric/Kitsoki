package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	starlarkhost "kitsoki/internal/host/starlark"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// OperatingSystemPolicySchemaVersion versions the one declarative source used
// to derive driver guidance, profiles, and benchmark expectations. A policy
// revision deliberately changes its hash, invalidating a benchmark that was
// recorded for an older effective surface.
const OperatingSystemPolicySchemaVersion = "v1"

// OperatingSystemPolicySpec is the single source of truth for the staged MCP
// operating-system policy. It is intentionally data-only: server registration
// remains the final integration slice's responsibility.
type OperatingSystemPolicySpec struct {
	SchemaVersion        string                     `json:"schema_version"`
	Profiles             []OperatingSystemProfile   `json:"profiles"`
	Preferences          []PolicyPreference         `json:"preferences"`
	DriverGuidance       []string                   `json:"driver_guidance"`
	DescriptionFragments []ToolDescriptionFragment  `json:"description_fragments"`
	Benchmark            BenchmarkPolicyExpectation `json:"benchmark"`
}

// OperatingSystemProfile names a restricted server configuration. Tools are
// exact names, rather than glob patterns, so new MCP tools never silently gain
// authority in strict mode.
type OperatingSystemProfile struct {
	Name           string         `json:"name"`
	Tools          []string       `json:"tools"`
	ForbiddenTools []string       `json:"forbidden_tools,omitempty"`
	HostRunProfile HostRunProfile `json:"host_run_profile"`
}

// PolicyPreference says Preferred should be chosen before Fallback when both
// profiles could serve a task. The compiler rejects preference cycles.
type PolicyPreference struct {
	Preferred string `json:"preferred"`
	Fallback  string `json:"fallback"`
}

// ToolDescriptionFragment pins the concise, meaningful phrase that a real MCP
// schema must retain for a tool referenced by strict guidance. This prevents a
// profile from remaining name-valid while becoming misleading to an agent.
type ToolDescriptionFragment struct {
	Tool     string `json:"tool"`
	Contains string `json:"contains"`
}

// BenchmarkPolicyExpectation ties an offline corpus identity to the effective
// policy hash. Arena stores both values, so it cannot promote a stale run.
type BenchmarkPolicyExpectation struct {
	CorpusVersion      string `json:"corpus_version"`
	ExpectedPolicyHash string `json:"expected_policy_hash"`
}

// CompiledOperatingSystemPolicy is a deterministic, agent-facing rendering of
// OperatingSystemPolicySpec. No live provider or filesystem state participates
// in compilation.
type CompiledOperatingSystemPolicy struct {
	SchemaVersion  string                     `json:"schema_version"`
	PolicyHash     string                     `json:"policy_hash"`
	BenchmarkHash  string                     `json:"benchmark_hash"`
	DriverPreamble string                     `json:"driver_preamble"`
	Profiles       []CompiledToolProfile      `json:"profiles"`
	Benchmark      BenchmarkPolicyExpectation `json:"benchmark"`
}

type CompiledToolProfile struct {
	Name                 string                    `json:"name"`
	Tools                []string                  `json:"tools"`
	HostRunProfile       HostRunProfile            `json:"host_run_profile"`
	ProfileHash          string                    `json:"profile_hash"`
	DescriptionFragments []ToolDescriptionFragment `json:"description_fragments"`
}

// DefaultOperatingSystemPolicySpec returns the only checked-in policy source.
// Callers may copy it for lint tests, but production compilation should always
// begin here rather than duplicating profile lists in a prompt or benchmark.
func DefaultOperatingSystemPolicySpec() OperatingSystemPolicySpec {
	strictTools := []string{
		"evidence.record", "gate.catalog", "gate.run", "objective.close", "objective.get", "objective.open", "objective.reopen", "objective.update", "policy.authorize_mutation", "receipt.list",
		"session.answer", "session.close", "session.explain", "session.inspect", "session.new", "session.status", "session.submit", "session.trace", "session.world", "studio.diagnose", "trace.explain",
		"workspace.codeact", "workspace.commit", "workspace.create", "workspace.list", "workspace.merge", "workspace.patch", "workspace.read", "workspace.search", "workspace.status", "workspace.teardown", "workspace.write",
	}
	spec := OperatingSystemPolicySpec{
		SchemaVersion: OperatingSystemPolicySchemaVersion,
		Profiles: []OperatingSystemProfile{
			{Name: "strict", Tools: strictTools, ForbiddenTools: []string{"host.patch", "host.run"}, HostRunProfile: HostRunProfileStrict},
			{Name: "legacy", Tools: append(append([]string(nil), strictTools...), "host.patch", "host.run", "vcs.commit", "vcs.diff", "vcs.integrate", "vcs.log", "vcs.status", "worktree.create", "worktree.list", "worktree.remove"), HostRunProfile: HostRunProfileLegacy},
			{Name: "escape", Tools: append(append([]string(nil), strictTools...), "host.run"), ForbiddenTools: []string{"host.patch"}, HostRunProfile: HostRunProfileEscape},
		},
		Preferences: []PolicyPreference{{Preferred: "strict", Fallback: "escape"}, {Preferred: "escape", Fallback: "legacy"}},
		DriverGuidance: []string{
			"Open an objective before a mutation and retain its receipts as the completion evidence.",
			"Use server-held workspace.* operations or workspace.codeact; do not create a raw git worktree.",
			"Use gate.run for typed validation; strict mode never exposes arbitrary host.run.",
			"Use studio.diagnose, session.explain, or trace.explain before changing an attachment or policy assumption.",
		},
		DescriptionFragments: []ToolDescriptionFragment{
			{Tool: "objective.open", Contains: "Open a versioned objective"},
			{Tool: "objective.close", Contains: "Close an open objective"},
			{Tool: "evidence.record", Contains: "Attach policy-hashed observed evidence"},
			{Tool: "workspace.create", Contains: "Create a clone-backed managed workspace"},
			{Tool: "workspace.patch", Contains: "Replace a guarded workspace file"},
			{Tool: "workspace.codeact", Contains: "capability-scoped Starlark action"},
			{Tool: "gate.run", Contains: "never accepts a command string"},
			{Tool: "studio.diagnose", Contains: "Read-only attachment diagnosis"},
			{Tool: "session.explain", Contains: "Read-only bounded explanation"},
			{Tool: "session.new", Contains: "Open a new driving session"},
			{Tool: "session.submit", Contains: "Submit a chosen intent"},
			{Tool: "session.status", Contains: "Compact, overflow-proof snapshot"},
			{Tool: "session.trace", Contains: "Read the handle's JSONL trace events"},
			{Tool: "trace.explain", Contains: "Read-only bounded explanation"},
		},
		Benchmark: BenchmarkPolicyExpectation{CorpusVersion: "mcp-os-replay-v1"},
	}
	spec.Benchmark.ExpectedPolicyHash = operatingSystemPolicyHash(spec)
	return spec
}

// CompileOperatingSystemPolicy lints the source then produces stable, sorted
// profile material suitable for an agent renderer and offline benchmark.
func CompileOperatingSystemPolicy(spec OperatingSystemPolicySpec) (CompiledOperatingSystemPolicy, error) {
	if err := LintOperatingSystemPolicy(spec); err != nil {
		return CompiledOperatingSystemPolicy{}, err
	}
	policyHash := operatingSystemPolicyHash(spec)
	profiles := make([]CompiledToolProfile, 0, len(spec.Profiles))
	for _, profile := range sortedProfiles(spec.Profiles) {
		tools := sortedUnique(profile.Tools)
		fragments := fragmentsForTools(spec.DescriptionFragments, tools)
		profileHash := hashJSON(struct {
			SchemaVersion string                    `json:"schema_version"`
			Name          string                    `json:"name"`
			Tools         []string                  `json:"tools"`
			HostRun       HostRunProfile            `json:"host_run_profile"`
			Fragments     []ToolDescriptionFragment `json:"description_fragments"`
		}{spec.SchemaVersion, profile.Name, tools, profile.HostRunProfile, fragments})
		profiles = append(profiles, CompiledToolProfile{Name: profile.Name, Tools: tools, HostRunProfile: profile.HostRunProfile, ProfileHash: profileHash, DescriptionFragments: fragments})
	}
	return CompiledOperatingSystemPolicy{
		SchemaVersion: spec.SchemaVersion,
		PolicyHash:    policyHash,
		BenchmarkHash: hashJSON(struct {
			CorpusVersion string `json:"corpus_version"`
			PolicyHash    string `json:"policy_hash"`
		}{spec.Benchmark.CorpusVersion, policyHash}),
		DriverPreamble: strings.Join(spec.DriverGuidance, "\n"),
		Profiles:       profiles,
		Benchmark:      spec.Benchmark,
	}, nil
}

// LintOperatingSystemPolicy catches policy errors before agent-facing artifacts
// are generated. Its checks are intentionally structural and deterministic.
func LintOperatingSystemPolicy(spec OperatingSystemPolicySpec) error {
	if spec.SchemaVersion != OperatingSystemPolicySchemaVersion {
		return fmt.Errorf("unsupported operating-system policy schema %q", spec.SchemaVersion)
	}
	if len(spec.Profiles) != 3 {
		return errors.New("policy must define strict, legacy, and escape profiles")
	}
	profiles := map[string]OperatingSystemProfile{}
	for _, profile := range spec.Profiles {
		if profile.Name == "" || profiles[profile.Name].Name != "" {
			return fmt.Errorf("profile name is empty or duplicated: %q", profile.Name)
		}
		if profile.Name != "strict" && profile.Name != "legacy" && profile.Name != "escape" {
			return fmt.Errorf("unknown policy profile %q", profile.Name)
		}
		if len(profile.Tools) == 0 || len(sortedUnique(profile.Tools)) != len(profile.Tools) {
			return fmt.Errorf("profile %q has no tools or duplicate tools", profile.Name)
		}
		for _, tool := range append(append([]string(nil), profile.Tools...), profile.ForbiddenTools...) {
			if !validPolicyToolName(tool) {
				return fmt.Errorf("profile %q has invalid tool name %q", profile.Name, tool)
			}
		}
		for _, forbidden := range profile.ForbiddenTools {
			if containsPolicyTool(profile.Tools, forbidden) {
				return fmt.Errorf("profile %q both exposes and forbids %q", profile.Name, forbidden)
			}
		}
		profiles[profile.Name] = profile
	}
	strict, legacy, escape := profiles["strict"], profiles["legacy"], profiles["escape"]
	if strict.HostRunProfile != HostRunProfileStrict || containsPolicyTool(strict.Tools, "host.run") || !containsPolicyTool(strict.ForbiddenTools, "host.run") {
		return errors.New("strict profile must omit and forbid arbitrary host.run")
	}
	if legacy.HostRunProfile != HostRunProfileLegacy || !containsPolicyTool(legacy.Tools, "host.run") {
		return errors.New("legacy profile must retain host.run under legacy policy")
	}
	if escape.HostRunProfile != HostRunProfileEscape || !containsPolicyTool(escape.Tools, "host.run") {
		return errors.New("escape profile must expose host.run only under escape policy")
	}
	if err := lintPreferenceCycles(spec.Preferences, profiles); err != nil {
		return err
	}
	for _, guidance := range spec.DriverGuidance {
		if obsoleteRawWorktreeAdvice(guidance) {
			return fmt.Errorf("obsolete raw-worktree advice: %q", guidance)
		}
	}
	strictSet := map[string]bool{}
	for _, tool := range strict.Tools {
		strictSet[tool] = true
	}
	for _, fragment := range spec.DescriptionFragments {
		if !strictSet[fragment.Tool] || strings.TrimSpace(fragment.Contains) == "" {
			return fmt.Errorf("description fragment must describe a strict tool: %q", fragment.Tool)
		}
	}
	if spec.Benchmark.CorpusVersion == "" || spec.Benchmark.ExpectedPolicyHash != operatingSystemPolicyHash(spec) {
		return errors.New("stale benchmark-policy combination")
	}
	return nil
}

// ValidateOperatingSystemPolicySchema performs the compiler's no-LLM schema
// smoke against a real in-process MCP connection. It proves every generated
// profile tool exists and that the referenced schema descriptions still match.
func ValidateOperatingSystemPolicySchema(ctx context.Context, spec OperatingSystemPolicySpec) error {
	compiled, err := CompileOperatingSystemPolicy(spec)
	if err != nil {
		return err
	}
	server, err := newPolicyCompileSchemaServer()
	if err != nil {
		return err
	}
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		return fmt.Errorf("connect policy schema server: %w", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "mcp-os-policy-schema", Version: OperatingSystemPolicySchemaVersion}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return fmt.Errorf("connect policy schema client: %w", err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("list policy schema tools: %w", err)
	}
	byName := make(map[string]*mcpsdk.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		byName[tool.Name] = tool
	}
	for _, profile := range compiled.Profiles {
		for _, name := range profile.Tools {
			tool := byName[name]
			if tool == nil {
				return fmt.Errorf("profile %q references unregistered tool %q", profile.Name, name)
			}
			rawSchema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return fmt.Errorf("marshal %q input schema: %w", name, err)
			}
			var schema map[string]any
			if err := json.Unmarshal(rawSchema, &schema); err != nil || schema == nil {
				return fmt.Errorf("invalid input schema for %q: %w", name, err)
			}
			for property, value := range schemaProperties(schema) {
				if _, isBooleanSchema := value.(bool); isBooleanSchema {
					return fmt.Errorf("profile tool %q has unsupported boolean schema property %q", name, property)
				}
			}
		}
		for _, fragment := range profile.DescriptionFragments {
			tool := byName[fragment.Tool]
			if tool == nil || !strings.Contains(tool.Description, fragment.Contains) {
				return fmt.Errorf("tool %q no longer matches generated description fragment %q", fragment.Tool, fragment.Contains)
			}
		}
	}
	return nil
}

// RenderStrictPolicyAgentMarkdown and RenderStrictPolicyAgentTOML are the
// deterministic sources for the narrow strict agent variants. Rollout owns
// adoption into the existing canonical driver profiles; these files make the
// proposed operating-system surface reviewable without changing that default.
func RenderStrictPolicyAgentMarkdown(spec OperatingSystemPolicySpec) (string, error) {
	compiled, err := CompileOperatingSystemPolicy(spec)
	if err != nil {
		return "", err
	}
	strict, err := compiledProfile(compiled, "strict")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("---\n"+
		"name: mcp-os-policy-strict\n"+
		"description: Drive the Kitsoki MCP operating system with a strict objective, managed-workspace, CodeAct, typed-gate, and diagnosis policy.\n"+
		"tools: %s\n"+
		"---\n\n"+
		"Strict MCP operating-system policy (schema %s; policy hash: %s).\n\n"+
		"Open an objective before mutation. Use only the server-held `workspace.*` plane\n"+
		"or `workspace.codeact`; do not create a raw git worktree. Run named `gate.run`\n"+
		"gates for validation and retain receipts as completion evidence. Use diagnosis\n"+
		"and bounded explanations before changing attachment or policy assumptions.\n\n"+
		"`host.run` and `host.patch` are deliberately absent. Escalation requires the\n"+
		"separate escape profile and an explicit, recorded exception.\n",
		strings.Join(agentToolNames(strict.Tools), ", "), compiled.SchemaVersion, compiled.PolicyHash), nil
}

func RenderStrictPolicyAgentTOML(spec OperatingSystemPolicySpec) (string, error) {
	compiled, err := CompileOperatingSystemPolicy(spec)
	if err != nil {
		return "", err
	}
	strict, err := compiledProfile(compiled, "strict")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("# Generated from internal/mcp/studio/policycompile.go.\n"+
		"# tools: %s\n"+
		"name = \"mcp-os-policy-strict\"\n"+
		"description = \"Drive the Kitsoki MCP operating system through strict objectives, managed workspaces, CodeAct, typed gates, and evidence-backed diagnosis.\"\n"+
		"model_reasoning_effort = \"medium\"\n"+
		"sandbox_mode = \"read-only\"\n\n"+
		"developer_instructions = \"\"\"\n"+
		"Strict MCP operating-system policy (schema %s; policy hash: %s).\n\n"+
		"Open an objective before mutation. Use only server-held workspace.* operations\n"+
		"or workspace.codeact; do not create a raw git worktree. Run named gate.run\n"+
		"gates and retain receipts as completion evidence. Use studio.diagnose,\n"+
		"session.explain, or trace.explain before changing an attachment or policy\n"+
		"assumption. host.run and host.patch are not available in this profile.\n"+
		"\"\"\"\n",
		strings.Join(agentToolNames(strict.Tools), ", "), compiled.SchemaVersion, compiled.PolicyHash), nil
}

func newPolicyCompileSchemaServer() (*Server, error) {
	objectives, err := NewObjectiveService(NewMemoryObjectiveStore(), nil)
	if err != nil {
		return nil, err
	}
	workspaces, err := NewManagedWorkspaceService(".capsules/workspaces", "policycompile-schema-runner", policyCompileWorkspaceRunner{}, objectives, nil)
	if err != nil {
		return nil, err
	}
	guard, err := NewFSGuard(workspaces)
	if err != nil {
		return nil, err
	}
	codeact, err := NewWorkspaceCodeactService(workspaces, guard, starlarkhost.DefaultCapabilities())
	if err != nil {
		return nil, err
	}
	gates, err := NewGateRunner(DefaultGateCatalog(), workspaces, policyCompileGateRunner{}, policyCompileWorkspaceProbe{}, nil)
	if err != nil {
		return nil, err
	}
	server := NewServer(NewStudioSession(nil))
	RegisterObjectivePolicyTools(server.mcpSrv, objectives)
	RegisterManagedWorkspaceTools(server.mcpSrv, workspaces, guard)
	RegisterWorkspaceCodeactTool(server.mcpSrv, codeact)
	RegisterGateTools(server.mcpSrv, gates)
	server.registerDiagnoseExplainTools()
	return server, nil
}

type policyCompileWorkspaceRunner struct{}

func (policyCompileWorkspaceRunner) Run(context.Context, string, ...string) (WorkspaceCommandResult, error) {
	return WorkspaceCommandResult{}, errors.New("policy schema smoke does not execute workspace commands")
}

type policyCompileGateRunner struct{}

func (policyCompileGateRunner) Run(context.Context, string, string, ...string) (GateCommandResult, error) {
	return GateCommandResult{}, errors.New("policy schema smoke does not execute gates")
}

type policyCompileWorkspaceProbe struct{}

func (policyCompileWorkspaceProbe) Head(context.Context, ManagedWorkspace) (string, error) {
	return "", errors.New("policy schema smoke does not inspect workspaces")
}

func operatingSystemPolicyHash(spec OperatingSystemPolicySpec) string {
	stable := struct {
		SchemaVersion        string                    `json:"schema_version"`
		Profiles             []OperatingSystemProfile  `json:"profiles"`
		Preferences          []PolicyPreference        `json:"preferences"`
		DriverGuidance       []string                  `json:"driver_guidance"`
		DescriptionFragments []ToolDescriptionFragment `json:"description_fragments"`
		CorpusVersion        string                    `json:"corpus_version"`
	}{spec.SchemaVersion, sortedProfiles(spec.Profiles), sortedPreferences(spec.Preferences), append([]string(nil), spec.DriverGuidance...), sortedFragments(spec.DescriptionFragments), spec.Benchmark.CorpusVersion}
	return hashJSON(stable)
}

func hashJSON(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func sortedProfiles(in []OperatingSystemProfile) []OperatingSystemProfile {
	out := append([]OperatingSystemProfile(nil), in...)
	for i := range out {
		out[i].Tools = sortedUnique(out[i].Tools)
		out[i].ForbiddenTools = sortedUnique(out[i].ForbiddenTools)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedPreferences(in []PolicyPreference) []PolicyPreference {
	out := append([]PolicyPreference(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Preferred == out[j].Preferred {
			return out[i].Fallback < out[j].Fallback
		}
		return out[i].Preferred < out[j].Preferred
	})
	return out
}

func sortedFragments(in []ToolDescriptionFragment) []ToolDescriptionFragment {
	out := append([]ToolDescriptionFragment(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

func fragmentsForTools(fragments []ToolDescriptionFragment, tools []string) []ToolDescriptionFragment {
	allowed := map[string]bool{}
	for _, tool := range tools {
		allowed[tool] = true
	}
	out := make([]ToolDescriptionFragment, 0, len(fragments))
	for _, fragment := range fragments {
		if allowed[fragment.Tool] {
			out = append(out, fragment)
		}
	}
	return sortedFragments(out)
}

func sortedUnique(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func validPolicyToolName(name string) bool {
	return name != "" && !strings.ContainsAny(name, " /\\\t\r\n")
}

func containsPolicyTool(tools []string, want string) bool {
	for _, tool := range tools {
		if tool == want {
			return true
		}
	}
	return false
}

func obsoleteRawWorktreeAdvice(guidance string) bool {
	lower := strings.ToLower(guidance)
	for _, phrase := range []string{"git worktree", "git clone"} {
		for from := 0; ; {
			at := strings.Index(lower[from:], phrase)
			if at < 0 {
				break
			}
			at += from
			prefix := lower[maxPolicyCompile(0, at-24):at]
			if !strings.Contains(prefix, "do not") && !strings.Contains(prefix, "never") && !strings.Contains(prefix, "avoid") {
				return true
			}
			from = at + len(phrase)
		}
	}
	return false
}

func schemaProperties(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	return properties
}

func compiledProfile(compiled CompiledOperatingSystemPolicy, name string) (CompiledToolProfile, error) {
	for _, profile := range compiled.Profiles {
		if profile.Name == name {
			return profile, nil
		}
	}
	return CompiledToolProfile{}, fmt.Errorf("compiled profile %q not found", name)
}

func agentToolNames(tools []string) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = "mcp__kitsoki__" + strings.ReplaceAll(tool, ".", "_")
	}
	return out
}

func maxPolicyCompile(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func lintPreferenceCycles(preferences []PolicyPreference, profiles map[string]OperatingSystemProfile) error {
	edges := map[string][]string{}
	for _, preference := range preferences {
		if profiles[preference.Preferred].Name == "" || profiles[preference.Fallback].Name == "" || preference.Preferred == preference.Fallback {
			return fmt.Errorf("invalid profile preference %q -> %q", preference.Preferred, preference.Fallback)
		}
		edges[preference.Preferred] = append(edges[preference.Preferred], preference.Fallback)
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return fmt.Errorf("policy preference cycle includes %q", name)
		}
		if visited[name] {
			return nil
		}
		visiting[name] = true
		for _, next := range edges[name] {
			if err := visit(next); err != nil {
				return err
			}
		}
		visiting[name] = false
		visited[name] = true
		return nil
	}
	for name := range profiles {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}
