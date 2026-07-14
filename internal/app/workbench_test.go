package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// workbench_test.go — stateless unit coverage for the `workbench:` loader
// desugaring pass (docs/proposals/room-workbench.md Tasks 1.1 + 2.1). Most
// cases use Load(testdata/...) so file-backed path resolution is covered too.

func loadWorkbenchTestdata(t *testing.T, name string) (*AppDef, error) {
	t.Helper()
	return Load(filepath.Join("testdata", "workbench", name))
}

// TestExpandWorkbenches_DesugarsExpectedShape is the table-driven happy-path
// check: a minimal `workbench:` room desugars into exactly the four
// primitives the proposal's Engine seams section names.
func TestExpandWorkbenches_DesugarsExpectedShape(t *testing.T) {
	cases := []struct {
		name            string
		file            string
		wantCaptureSlot string
		wantOffRamp     string
	}{
		{name: "defaults", file: "basic.yaml", wantCaptureSlot: "bench_request", wantOffRamp: "builder"},
		{name: "capture_slot and off_ramp_agent overrides", file: "overrides.yaml", wantCaptureSlot: "custom_field", wantOffRamp: "qa_agent"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def, err := loadWorkbenchTestdata(t, tc.file)
			require.NoError(t, err)
			s := def.States["bench"]
			require.NotNil(t, s)

			// write_mode: read_only.
			require.Equal(t, WriteModeReadOnly, s.WriteMode)
			require.Contains(t, def.World, tc.wantCaptureSlot)
			require.Equal(t, "string", def.World[tc.wantCaptureSlot].Type)
			require.Contains(t, def.World, "bench_note")
			require.Equal(t, "object", def.World["bench_note"].Type)

			// agent_off_ramp.
			require.NotNil(t, s.AgentOffRamp)
			require.Equal(t, tc.wantOffRamp, s.AgentOffRamp.Agent)

			// Synthesized on_enter host.agent.task, appended last.
			require.NotEmpty(t, s.OnEnter)
			eff := s.OnEnter[len(s.OnEnter)-1]
			require.Equal(t, "host.agent.task", eff.Invoke)
			require.True(t, eff.Once)
			require.Equal(t, "world."+tc.wantCaptureSlot+" != ''", eff.When)
			require.Equal(t, "bench", eff.OnError)
			require.Equal(t, "builder", eff.With["agent"])
			require.Equal(t, "{{ world.workdir }}", eff.With["working_dir"])
			acceptance, ok := eff.With["acceptance"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "schemas/out.json", acceptance["schema"])
			ctx, ok := eff.With["context"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "prompts/bench.md", ctx["prompt"])
			args, ok := ctx["args"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "{{ world."+tc.wantCaptureSlot+" }}", args["request"])
			require.Equal(t, "submitted", eff.Bind["bench_note"])

			// Synthesized catch-all capture intent, wired as default_intent.
			require.Equal(t, "bench_capture", s.DefaultIntent)
			intentDef, ok := def.Intents["bench_capture"]
			require.True(t, ok)
			slot, ok := intentDef.Slots["request"]
			require.True(t, ok)
			require.True(t, slot.Required)
			require.Equal(t, "string", slot.Type)

			trs, ok := s.On["bench_capture"]
			require.True(t, ok)
			require.Len(t, trs, 1)
			require.Equal(t, "bench", trs[0].Target)
			require.Len(t, trs[0].Effects, 1)
			require.Equal(t, "{{ slots.request }}", trs[0].Effects[0].Set[tc.wantCaptureSlot])
		})
	}
}

func TestExpandWorkbenches_LoadBytesRunsMacro(t *testing.T) {
	def, err := LoadBytes([]byte(`
app:
  id: wb-load-bytes
  version: 0.1.0
hosts: [host.agent.task]
root: bench
toolboxes:
  builder_toolbox:
    tools: [Read, Grep, Glob, Edit, Write, Bash]
    effect: write
states:
  bench:
    workbench:
      agent: builder
      prompt: prompts/bench.md
      acceptance_schema: schemas/out.json
agents:
  builder:
    system_prompt: "build things"
    toolbox: builder_toolbox
`))
	require.NoError(t, err)
	require.Equal(t, WriteModeReadOnly, def.States["bench"].WriteMode)
	require.Contains(t, def.World, "bench_request")
	require.Contains(t, def.World, "bench_note")
	require.Contains(t, def.Intents, "bench_capture")
}

// TestExpandWorkbenches_MissingRequiredField checks each of the three
// required fields fails to load with a clear, field-naming error.
func TestExpandWorkbenches_MissingRequiredField(t *testing.T) {
	cases := []struct {
		name string
		file string
		want string
	}{
		{name: "missing agent", file: "missing-agent.yaml", want: "agent is required"},
		{name: "missing prompt", file: "missing-prompt.yaml", want: "prompt is required"},
		{name: "missing acceptance_schema", file: "missing-schema.yaml", want: "acceptance_schema is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadWorkbenchTestdata(t, tc.file)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestExpandWorkbenches_ContextArgsMustReferenceDeclaredWorld(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "app.yaml"), `
app:
  id: wb-context-args
  version: 0.1.0
hosts: [host.agent.task]
root: bench
toolboxes:
  builder_toolbox:
    tools: [Read, Grep, Glob, Edit, Write, Bash]
    effect: write
states:
  bench:
    workbench:
      agent: builder
      prompt: prompts/bench.md
      acceptance_schema: schemas/out.json
      context_args:
        prior: "{{ world.missing_prior }}"
agents:
  builder:
    system_prompt: "build things"
    toolbox: builder_toolbox
`)
	_, err := Load(filepath.Join(dir, "app.yaml"))
	require.Error(t, err)
	require.Contains(t, err.Error(), `workbench.context_args.prior references undeclared world key "missing_prior"`)
}

// TestExpandWorkbenches_PlanContract covers the `plan: true` load-time
// schema-contract check directly: acceptance_schema must declare a top-level
// "plan" object property requiring goal/step/verify (the shared plan.json
// shape).
func TestExpandWorkbenches_PlanContract(t *testing.T) {
	dir := t.TempDir()

	validSchema := `{
  "type": "object",
  "properties": {
    "plan": {
      "type": "object",
      "required": ["goal", "step", "verify"]
    }
  }
}`
	validPath := filepath.Join(dir, "valid.json")
	require.NoError(t, os.WriteFile(validPath, []byte(validSchema), 0o644))

	missingPlanSchema := `{"type": "object", "properties": {}}`
	missingPlanPath := filepath.Join(dir, "missing-plan.json")
	require.NoError(t, os.WriteFile(missingPlanPath, []byte(missingPlanSchema), 0o644))

	incompleteSchema := `{
  "type": "object",
  "properties": {
    "plan": { "type": "object", "required": ["goal"] }
  }
}`
	incompletePath := filepath.Join(dir, "incomplete.json")
	require.NoError(t, os.WriteFile(incompletePath, []byte(incompleteSchema), 0o644))

	cases := []struct {
		name       string
		schemaPath string
		wantErr    string
	}{
		{name: "valid plan contract", schemaPath: validPath, wantErr: ""},
		{name: "missing plan property", schemaPath: missingPlanPath, wantErr: `to declare a top-level "plan" object property`},
		{name: "incomplete required list", schemaPath: incompletePath, wantErr: `must require "step"`},
		{name: "unreadable schema file", schemaPath: filepath.Join(dir, "does-not-exist.json"), wantErr: "to be readable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := &AppDef{BaseDir: ""}
			err := validateWorkbenchPlanContract(def, "bench", tc.schemaPath)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestExpandWorkbenches_AgentInvariants is the table-driven check for Task
// 1.2/2.1's load-time agent-capability invariant: the named agent must exist
// in agents:, must declare toolbox:+effect: (WS vocabulary) rather than the
// legacy tools:/bash_profile:/external_side_effect: triplet, and must resolve
// to effect write or external — a read-only workbench agent is a load error
// pointing the author at agent_off_ramp instead.
func TestExpandWorkbenches_AgentInvariants(t *testing.T) {
	cases := []struct {
		name string
		file string
		want string
	}{
		{
			name: "agent not declared in agents:",
			file: "agent-not-declared.yaml",
			want: `agent "ghost" is not declared in agents:`,
		},
		{
			name: "agent uses legacy tools/bash_profile/external_side_effect vocabulary",
			file: "agent-legacy-vocabulary.yaml",
			want: `agent "builder" must declare toolbox: + effect: (WS vocabulary) for workbench use, not the legacy tools:/bash_profile:/external_side_effect:`,
		},
		{
			name: "agent resolves to a read-only effect",
			file: "agent-readonly.yaml",
			want: `agent "reviewer" resolves to effect "read", but a workbench agent must declare effect: write or external`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadWorkbenchTestdata(t, tc.file)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
	// The read-only case's message must also point the author at the
	// escape hatch (agent_off_ramp) rather than just naming the failure.
	_, err := loadWorkbenchTestdata(t, "agent-readonly.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "hand-author agent_off_ramp instead of workbench:")
}

// TestExpandWorkbenches_MutualExclusion covers Task 1.2b: a state with
// workbench: may not also hand-author write_mode, agent_off_ramp, or
// default_intent — workbench: is a macro that sets all three itself, so a
// hand-authored value alongside it is an unresolvable ambiguity, not a
// silent override.
func TestExpandWorkbenches_MutualExclusion(t *testing.T) {
	cases := []struct {
		name string
		file string
		want string
	}{
		{
			name: "hand-authored write_mode",
			file: "conflict-write-mode.yaml",
			want: `cannot combine with hand-authored write_mode: "open"`,
		},
		{
			name: "hand-authored agent_off_ramp",
			file: "conflict-off-ramp.yaml",
			want: `cannot combine with a hand-authored agent_off_ramp:`,
		},
		{
			name: "hand-authored default_intent",
			file: "conflict-default-intent.yaml",
			want: `cannot combine with hand-authored default_intent: "go"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadWorkbenchTestdata(t, tc.file)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestExpandWorkbenches_NoOpWithoutWorkbenchBlock proves the desugaring pass
// is byte-for-byte inert over a room with no `workbench:` block — the
// proposal's backward-compat guarantee ("a room with no workbench: block is
// byte-for-byte unaffected"), exercised directly against expandWorkbenches.
func TestExpandWorkbenches_NoOpWithoutWorkbenchBlock(t *testing.T) {
	def := &AppDef{
		States: map[string]*State{
			"plain": {
				WriteMode:     "open",
				DefaultIntent: "",
				OnEnter: []Effect{
					{Say: "hello"},
				},
				On: map[string][]Transition{
					"go": {{Target: "plain"}},
				},
			},
			"nested_parent": {
				States: map[string]*State{
					"child": {View: View{}},
				},
			},
		},
		Intents: map[string]Intent{
			"go": {Title: "Go"},
		},
	}
	before := deepCopyAppDefForTest(def)

	errs := expandWorkbenches(def, "<test>")
	require.Empty(t, errs)
	require.True(t, reflect.DeepEqual(before, def), "expandWorkbenches must be a no-op over states with no workbench: block")
}

// TestExpandWorkbenches_NoOpOverFullControlApp proves the same no-op
// guarantee end-to-end through the full Load() pipeline, against a control
// app that hand-rolls the same write_mode/host.agent.task shape workbench:
// would synthesize — the pass must not touch a room that never opted in.
func TestExpandWorkbenches_NoOpOverFullControlApp(t *testing.T) {
	def, err := loadWorkbenchTestdata(t, "control.yaml")
	require.NoError(t, err)
	s := def.States["plain"]
	require.NotNil(t, s)
	require.Nil(t, s.Workbench)
	require.Equal(t, "open", s.WriteMode)
	require.Nil(t, s.AgentOffRamp)
	require.Empty(t, s.DefaultIntent)
	require.Empty(t, s.OnEnter)
	require.Len(t, s.On["go"], 1)
	_, hasCapture := def.Intents["plain_capture"]
	require.False(t, hasCapture)
}

// deepCopyAppDefForTest produces a deep copy of the fields expandWorkbenches
// can touch (States, Intents) — hand-copying only what this pass could
// mutate keeps the no-op assertion meaningful without depending on unrelated
// AppDef fields' copyability.
func deepCopyAppDefForTest(def *AppDef) *AppDef {
	cp := &AppDef{
		States:  deepCopyStates(def.States),
		Intents: map[string]Intent{},
	}
	for k, v := range def.Intents {
		cp.Intents[k] = v
	}
	return cp
}

func deepCopyStates(in map[string]*State) map[string]*State {
	if in == nil {
		return nil
	}
	out := make(map[string]*State, len(in))
	for k, v := range in {
		if v == nil {
			out[k] = nil
			continue
		}
		cp := *v
		cp.OnEnter = append([]Effect(nil), v.OnEnter...)
		if v.On != nil {
			cp.On = map[string][]Transition{}
			for ik, trs := range v.On {
				cp.On[ik] = append([]Transition(nil), trs...)
			}
		}
		cp.States = deepCopyStates(v.States)
		out[k] = &cp
	}
	return out
}
