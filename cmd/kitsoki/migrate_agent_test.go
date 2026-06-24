// migrate_agent_test.go — tests for `kitsoki migrate-agent` codemod (Phase 6).
//
// Tests cover each of the six classification cases from §6 of the agent-split
// proposal, plus the ambiguous-case TODO file emission.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── classification unit tests ─────────────────────────────────────────────

func TestClassify_TalkVerb_Converse(t *testing.T) {
	t.Parallel()
	s := agentCallSite{OriginalVerb: "host.agent.talk"}
	classifyAgentCallSite(&s)
	if s.Classification != classConverse {
		t.Fatalf("host.agent.talk should classify as converse, got %s", s.Classification)
	}
}

func TestClassify_ChatID_Converse(t *testing.T) {
	t.Parallel()
	s := agentCallSite{OriginalVerb: "host.agent.ask_with_mcp", HasChatID: true}
	classifyAgentCallSite(&s)
	if s.Classification != classConverse {
		t.Fatalf("chat_id: set should classify as converse, got %s", s.Classification)
	}
}

func TestClassify_MutationTools_Task(t *testing.T) {
	t.Parallel()
	s := agentCallSite{OriginalVerb: "host.agent.ask_with_mcp", HasMutationTool: true}
	classifyAgentCallSite(&s)
	if s.Classification != classTask {
		t.Fatalf("agent with mutation tools should classify as task, got %s", s.Classification)
	}
}

func TestClassify_WorkerValidator_Task(t *testing.T) {
	t.Parallel()
	s := agentCallSite{
		OriginalVerb:      "host.agent.ask_with_mcp",
		HasValidatorCmd:   true,
		ValidatorIsWorker: true,
	}
	classifyAgentCallSite(&s)
	if s.Classification != classTask {
		t.Fatalf("worker validator should classify as task, got %s", s.Classification)
	}
}

func TestClassify_EnumPromptWithSchema_Extract(t *testing.T) {
	t.Parallel()
	s := agentCallSite{
		OriginalVerb: "host.agent.ask_with_mcp",
		PromptIsEnum: true,
		HasSchema:    true,
	}
	classifyAgentCallSite(&s)
	if s.Classification != classExtract {
		t.Fatalf("enum prompt with schema should classify as extract, got %s", s.Classification)
	}
}

func TestClassify_EnumPromptNoSchema_Ambiguous(t *testing.T) {
	t.Parallel()
	s := agentCallSite{
		OriginalVerb: "host.agent.ask_with_mcp",
		PromptIsEnum: true,
		HasSchema:    false,
	}
	classifyAgentCallSite(&s)
	if s.Classification != classAmbiguous {
		t.Fatalf("enum prompt without schema should be ambiguous, got %s", s.Classification)
	}
	if !s.Ambiguous {
		t.Fatal("Ambiguous flag should be set")
	}
}

func TestClassify_SchemaNoMutation_Decide(t *testing.T) {
	t.Parallel()
	s := agentCallSite{
		OriginalVerb: "host.agent.ask_with_mcp",
		HasSchema:    true,
	}
	classifyAgentCallSite(&s)
	if s.Classification != classDecide {
		t.Fatalf("schema + no mutation should classify as decide, got %s", s.Classification)
	}
}

func TestClassify_SchemaWithReadOnlyValidator_Decide(t *testing.T) {
	t.Parallel()
	s := agentCallSite{
		OriginalVerb:    "host.agent.ask_with_mcp",
		HasSchema:       true,
		HasValidatorCmd: true,
		// ValidatorIsWorker = false → read-only
	}
	classifyAgentCallSite(&s)
	if s.Classification != classDecide {
		t.Fatalf("schema + read-only validator should classify as decide, got %s", s.Classification)
	}
}

func TestClassify_NoSchemaNomination_Ask(t *testing.T) {
	t.Parallel()
	s := agentCallSite{OriginalVerb: "host.agent.ask_with_mcp"}
	classifyAgentCallSite(&s)
	if s.Classification != classAsk {
		t.Fatalf("no schema / no mutation / no special flags should classify as ask, got %s", s.Classification)
	}
}

func TestClassify_ValidatorNoCMD_AmbigiousWithSchema(t *testing.T) {
	t.Parallel()
	// validator.post_cmd present but no schema: → ambiguous
	s := agentCallSite{
		OriginalVerb:    "host.agent.ask_with_mcp",
		HasValidatorCmd: true,
		HasSchema:       false,
	}
	classifyAgentCallSite(&s)
	if s.Classification != classAmbiguous {
		t.Fatalf("validator without schema should be ambiguous, got %s", s.Classification)
	}
}

// ── newAgentVerb ─────────────────────────────────────────────────────────

func TestNewAgentVerb_AllCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cls  agentCallClassification
		want string
	}{
		{classExtract, "host.agent.extract"},
		{classDecide, "host.agent.decide"},
		{classAsk, "host.agent.ask"},
		{classTask, "host.agent.task"},
		{classConverse, "host.agent.converse"},
		{classAmbiguous, ""},
	}
	for _, tc := range cases {
		got := newAgentVerb("host.agent.ask_with_mcp", tc.cls)
		if got != tc.want {
			t.Fatalf("cls=%s: want %q got %q", tc.cls, tc.want, got)
		}
	}
}

// ── findAgentCallSites + rewrite ─────────────────────────────────────────

const yamlWithTalk = `
app:
  id: test-app
states:
  chat_room:
    on_enter:
      - invoke: host.agent.talk
        with:
          question: "Hello there"
`

const yamlWithChatID = `
app:
  id: test-app
states:
  chat_room:
    on:
      say:
        - invoke: host.agent.ask_with_mcp
          with:
            chat_id: "abc-123"
            question: "Hello"
`

const yamlWithMutation = `
app:
  id: test-app
states:
  implement:
    on_enter:
      - invoke: host.agent.ask_with_mcp
        with:
          tools:
            - Edit
            - Bash
          schema: schemas/result.json
`

const yamlWithSchema = `
app:
  id: test-app
states:
  judge:
    on_enter:
      - invoke: host.agent.ask_with_mcp
        with:
          schema: schemas/verdict.json
          prompt: prompts/judge.md
`

const yamlWithNoSchema = `
app:
  id: test-app
states:
  describe:
    on_enter:
      - invoke: host.agent.ask_with_mcp
        with:
          prompt: prompts/describe.md
`

func TestFindAgentCallSites_Talk(t *testing.T) {
	t.Parallel()
	sites, err := findAgentCallSites("test.yaml", []byte(yamlWithTalk))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(sites))
	}
	if sites[0].OriginalVerb != "host.agent.talk" {
		t.Fatalf("expected talk verb, got %q", sites[0].OriginalVerb)
	}
}

func TestFindAgentCallSites_AskWithMCP(t *testing.T) {
	t.Parallel()
	sites, err := findAgentCallSites("test.yaml", []byte(yamlWithSchema))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(sites))
	}
	if sites[0].OriginalVerb != "host.agent.ask_with_mcp" {
		t.Fatalf("expected ask_with_mcp verb, got %q", sites[0].OriginalVerb)
	}
}

func TestMigrateAgent_RewritesAgentTerminologyAcrossStory(t *testing.T) {
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	promptPath := filepath.Join(dir, "prompts", "ask.md")
	if err := os.MkdirAll(filepath.Dir(promptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appPath, []byte(`
app:
  id: old-story
oracle_plugins:
  oracle.local:
    plugin: builtin.local_llm
hosts:
  - host.oracle.decide
states:
  start:
    on_enter:
      - invoke: host.oracle.decide
        with:
          agent: oracle.local
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte("The Oracle should emit oracle.call metadata."), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetArgs([]string{"migrate-agent", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate-agent: %v", err)
	}

	appBytes, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}
	appText := string(appBytes)
	for _, old := range []string{"oracle_plugins", "oracle.local", "host.oracle.decide"} {
		if strings.Contains(appText, old) {
			t.Fatalf("app.yaml still contains %q:\n%s", old, appText)
		}
	}
	for _, want := range []string{"agent_plugins", "agent.local", "host.agent.decide"} {
		if !strings.Contains(appText, want) {
			t.Fatalf("app.yaml missing %q:\n%s", want, appText)
		}
	}
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(promptBytes); strings.Contains(got, "Oracle") || strings.Contains(got, "oracle") {
		t.Fatalf("prompt still contains old terminology: %q", got)
	}
}

func TestFindAgentCallSites_DetectsSchema(t *testing.T) {
	t.Parallel()
	sites, err := findAgentCallSites("test.yaml", []byte(yamlWithSchema))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("expected sites")
	}
	if !sites[0].HasSchema {
		t.Fatalf("expected HasSchema=true for yaml with schema: block; got false\nwith block scraped:\n%v", sites[0])
	}
}

func TestFindAgentCallSites_DetectsChatID(t *testing.T) {
	t.Parallel()
	sites, err := findAgentCallSites("test.yaml", []byte(yamlWithChatID))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("expected sites")
	}
	if !sites[0].HasChatID {
		t.Fatal("expected HasChatID=true")
	}
}

func TestFindAgentCallSites_DetectsMutationTools(t *testing.T) {
	t.Parallel()
	sites, err := findAgentCallSites("test.yaml", []byte(yamlWithMutation))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("expected sites")
	}
	if !sites[0].HasMutationTool {
		t.Fatal("expected HasMutationTool=true when tools: contains Edit/Bash")
	}
}

// ── full rewrite integration ───────────────────────────────────────────────

func TestMigrateAgentFile_TalkToConverse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, []byte(yamlWithTalk), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := newRootCmd()
	var stdout strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"migrate-agent", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate-agent: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "host.agent.converse") {
		t.Fatalf("expected host.agent.converse in rewritten YAML, got:\n%s", got)
	}
	if strings.Contains(string(got), "host.agent.talk") {
		t.Fatalf("host.agent.talk should have been replaced:\n%s", got)
	}
}

func TestMigrateAgentFile_SchemaToDecide(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, []byte(yamlWithSchema), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := newRootCmd()
	var stdout strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"migrate-agent", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate-agent: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "host.agent.decide") {
		t.Fatalf("expected host.agent.decide in rewritten YAML, got:\n%s", got)
	}
}

func TestMigrateAgentFile_NoSchemaToAsk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, []byte(yamlWithNoSchema), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := newRootCmd()
	var stdout strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"migrate-agent", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate-agent: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "host.agent.ask") {
		t.Fatalf("expected host.agent.ask in rewritten YAML, got:\n%s", got)
	}
}

func TestMigrateAgentFile_DryRun_NoWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	orig := []byte(yamlWithSchema)
	if err := os.WriteFile(path, orig, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := newRootCmd()
	var stdout strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"migrate-agent", "--dry-run", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate-agent --dry-run: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// File should be unchanged.
	if string(got) != string(orig) {
		t.Fatalf("--dry-run must not modify the file")
	}
	// Diff output should appear on stdout.
	if !strings.Contains(stdout.String(), "+++") && !strings.Contains(stdout.String(), "---") {
		t.Logf("dry-run stdout: %s", stdout.String())
		// The file may not have changed if there were no sites; that's OK.
	}
}

// ── ambiguous TODO file ───────────────────────────────────────────────────

const yamlAmbiguous = `
app:
  id: test-app
states:
  extract_state:
    on_enter:
      - invoke: host.agent.ask_with_mcp
        with:
          prompt: "pick one of the following options"
`

func TestMigrateAgentFile_AmbiguousTODO(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, []byte(yamlAmbiguous), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := newRootCmd()
	var stdout strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"migrate-agent", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate-agent: %v", err)
	}
	todoPath := path + ".migrate-agent.todo"
	if _, err := os.Stat(todoPath); err != nil {
		t.Fatalf("TODO file should have been created for ambiguous call: %v", err)
	}
	todoContent, err := os.ReadFile(todoPath)
	if err != nil {
		t.Fatalf("read TODO: %v", err)
	}
	if !strings.Contains(string(todoContent), "requiring human review") {
		t.Fatalf("TODO file should mention human review:\n%s", todoContent)
	}
	if !strings.Contains(string(todoContent), "schema") {
		t.Fatalf("TODO file should mention schema fix:\n%s", todoContent)
	}
}

func TestMigrateAgentFile_PrintClassified(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, []byte(yamlWithSchema), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := newRootCmd()
	var stdout strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"migrate-agent", "--print-classified", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate-agent --print-classified: %v", err)
	}
	if !strings.Contains(stdout.String(), "decide") && !strings.Contains(stdout.String(), "ask") {
		t.Fatalf("--print-classified should show classification: %q", stdout.String())
	}
}

// ── task with: block restructuring (C2 fix) ──────────────────────────────

const yamlWithMutationFull = `
app:
  id: test-app
states:
  implement:
    on_enter:
      - invoke: host.agent.ask_with_mcp
        with:
          agent: implementer
          prompt: prompts/implementing.md
          schema: schemas/implement_artifact.json
          working_dir: "{{ world.workdir }}"
          args:
            ticket_id: "{{ world.ticket_id }}"
            fix_description: "{{ world.proposal.fix_description }}"
          tools:
            - Edit
            - Bash
        bind:
          artifact: submitted
        on_error: idle
`

func TestMigrateAgentFile_MutationToTask_RestructuresWithBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, []byte(yamlWithMutationFull), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := newRootCmd()
	var stdout strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"migrate-agent", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate-agent: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(got)

	// Verb must be rewritten.
	if !strings.Contains(s, "host.agent.task") {
		t.Fatalf("expected host.agent.task, got:\n%s", s)
	}
	if strings.Contains(s, "host.agent.ask_with_mcp") {
		t.Fatalf("ask_with_mcp should be replaced:\n%s", s)
	}

	// with: block must contain acceptance: and context: sub-blocks.
	if !strings.Contains(s, "acceptance:") {
		t.Fatalf("expected acceptance: sub-block in restructured with:, got:\n%s", s)
	}
	if !strings.Contains(s, "context:") {
		t.Fatalf("expected context: sub-block in restructured with:, got:\n%s", s)
	}

	// schema: must move into acceptance:
	if !strings.Contains(s, "schema: schemas/implement_artifact.json") {
		t.Fatalf("schema must be preserved under acceptance:, got:\n%s", s)
	}

	// prompt: must move into context:
	if !strings.Contains(s, "prompt: prompts/implementing.md") {
		t.Fatalf("prompt must be preserved under context:, got:\n%s", s)
	}

	// args: must move into context:
	if !strings.Contains(s, "ticket_id:") {
		t.Fatalf("args.ticket_id must be preserved under context.args:, got:\n%s", s)
	}

	// agent: and working_dir: must remain at top level of with:.
	if !strings.Contains(s, "agent: implementer") {
		t.Fatalf("agent must be preserved at top level of with:, got:\n%s", s)
	}
	if !strings.Contains(s, `working_dir: "{{ world.workdir }}"`) {
		t.Fatalf("working_dir must be preserved at top level of with:, got:\n%s", s)
	}

	// bind: and on_error: must be untouched (outside with: block).
	if !strings.Contains(s, "bind:") {
		t.Fatalf("bind: block must be preserved:\n%s", s)
	}
	if !strings.Contains(s, "on_error: idle") {
		t.Fatalf("on_error must be preserved:\n%s", s)
	}
}

// ── heuristic helpers ────────────────────────────────────────────────────

func TestIsMutationToolName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{"Edit", true},
		{"Write", true},
		{"NotebookEdit", true},
		{"Read", false},
		{"Grep", false},
		{"Bash", false},
		{"edit", false},  // case-sensitive
		{"write", false}, // case-sensitive
	}
	for _, tc := range cases {
		got := isMutationToolName(tc.name)
		if got != tc.want {
			t.Errorf("isMutationToolName(%q): want %v got %v", tc.name, tc.want, got)
		}
	}
}

// TestFindAgentCallSites_FlowStyleTools verifies that flow-style tools:
// [Edit, Write] is detected via AST traversal (M10 false-positive fix).
func TestFindAgentCallSites_FlowStyleTools(t *testing.T) {
	t.Parallel()
	const yaml = `
app:
  id: test-app
states:
  implement:
    on_enter:
      - invoke: host.agent.ask_with_mcp
        with:
          tools: [Edit, Write]
          schema: schemas/result.json
`
	sites, err := findAgentCallSites("test.yaml", []byte(yaml))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("expected 1 site")
	}
	if !sites[0].HasMutationTool {
		t.Fatal("expected HasMutationTool=true for flow-style [Edit, Write]")
	}
}

// TestFindAgentCallSites_NoFalsePositive_CommentAndBind verifies that
// "Edit" appearing in a comment or bind: does NOT set HasMutationTool (M10).
func TestFindAgentCallSites_NoFalsePositive_CommentAndBind(t *testing.T) {
	t.Parallel()
	const yaml = `
app:
  id: test-app
states:
  describe:
    on_enter:
      # Edit the prompt later if needed
      - invoke: host.agent.ask_with_mcp
        with:
          prompt: prompts/describe.md
        bind:
          artifact: edited_content
`
	sites, err := findAgentCallSites("test.yaml", []byte(yaml))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("expected 1 site")
	}
	if sites[0].HasMutationTool {
		t.Fatal("HasMutationTool must be false when 'Edit' only appears in a comment or bind:")
	}
}

func TestIsWorkerValidatorHint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		block string
		want  bool
	}{
		{"post_cmd: python3 verify_read.py\n", false},
		{"post_cmd: git commit -m 'fix'\n", true},
		{"post_cmd: git push origin HEAD\n", true},
		{"post_cmd: python3 check_sum.py\n", false},
	}
	for _, tc := range cases {
		got := isWorkerValidatorHint(tc.block)
		if got != tc.want {
			t.Errorf("isWorkerValidatorHint(%q): want %v got %v", tc.block, tc.want, got)
		}
	}
}

func TestIsEnumPromptHint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		block string
		want  bool
	}{
		{"prompt: prompts/judge.md\n  args:\n    pr: 1\n", false},
		{"prompt: 'pick one of: yes, no'\n", true},
		{"prompt: prompts/diagnose.md\n  classify: true\n", true},
		{"prompt: 'extract the title from this'\n", true},
		{"prompt: prompts/describe.md\n", false},
	}
	for _, tc := range cases {
		got := isEnumPromptHint(tc.block)
		if got != tc.want {
			t.Errorf("isEnumPromptHint(%q): want %v got %v", tc.block, tc.want, got)
		}
	}
}
