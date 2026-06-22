package agenteval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
)

var validComparators = map[string]bool{
	"exact":         true,
	"field_subset":  true,
	"enum":          true,
	"artifact_diff": true,
	"judge":         true,
}

func LoadDataset(path string) (*Dataset, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ds Dataset
	if err := goyaml.Unmarshal(b, &ds); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	ds.Path = abs
	if ds.App != "" {
		ds.AppPath = cleanJoin(filepath.Dir(abs), ds.App)
	}
	return &ds, nil
}

func LoadDatasets(root string) ([]*Dataset, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	var paths []string
	if !info.IsDir() {
		paths = append(paths, root)
	} else {
		matches, err := filepath.Glob(filepath.Join(root, "evals", "*.yaml"))
		if err != nil {
			return nil, err
		}
		paths = append(paths, matches...)
	}
	sort.Strings(paths)
	out := make([]*Dataset, 0, len(paths))
	for _, path := range paths {
		ds, err := LoadDataset(path)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", path, err)
		}
		out = append(out, ds)
	}
	return out, nil
}

func ValidateDataset(path string) (ValidationResult, error) {
	ds, err := LoadDataset(path)
	if err != nil {
		return ValidationResult{}, err
	}
	result := ValidationResult{Dataset: ds}
	if ds.Kind != "agent_eval" {
		result.Errors = append(result.Errors, "kind must be agent_eval")
	}
	if ds.App == "" {
		result.Errors = append(result.Errors, "app is required")
	}
	if ds.Call == "" {
		result.Errors = append(result.Errors, "call is required")
	}
	if ds.Comparator.Kind == "" {
		result.Errors = append(result.Errors, "comparator is required")
	} else if !validComparators[ds.Comparator.Kind] {
		result.Errors = append(result.Errors, fmt.Sprintf("unsupported comparator %q", ds.Comparator.Kind))
	}
	if len(ds.Examples) == 0 {
		result.Errors = append(result.Errors, "at least one example is required")
	}
	names := map[string]bool{}
	for i, ex := range ds.Examples {
		if ex.Name == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("examples[%d].name is required", i))
		}
		if names[ex.Name] {
			result.Errors = append(result.Errors, fmt.Sprintf("duplicate example name %q", ex.Name))
		}
		names[ex.Name] = true
		if len(ex.Expect) == 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("example %q expect is required", ex.Name))
		}
	}
	if len(result.Errors) > 0 && ds.AppPath == "" {
		return result, nil
	}
	def, err := app.Load(ds.AppPath)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("load app: %v", err))
		return result, nil
	}
	call, err := ResolveCallSite(def, ds.Call)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, nil
	}
	call.DatasetHash = fileHash(ds.Path)
	if call.PromptPath != "" {
		call.PromptHash = fileHash(cleanJoin(def.BaseDir, call.PromptPath))
	}
	if call.SchemaPath != "" {
		call.SchemaHash = fileHash(cleanJoin(def.BaseDir, call.SchemaPath))
	}
	call.ToolboxHash = toolboxHash(def, call.Agent)
	result.Call = call
	if ds.Agent != "" && call.Agent != "" && ds.Agent != call.Agent {
		result.Errors = append(result.Errors, fmt.Sprintf("dataset agent %q does not match call-site agent %q", ds.Agent, call.Agent))
	}
	if call.SchemaPath != "" {
		schema, readErr := os.ReadFile(cleanJoin(def.BaseDir, call.SchemaPath))
		if readErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("read schema: %v", readErr))
		} else {
			for _, ex := range ds.Examples {
				raw, rawErr := toRaw(ex.Expect)
				if rawErr != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("example %q expect: %v", ex.Name, rawErr))
					continue
				}
				if err := agent.ValidateSubmission(schema, raw); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("example %q expect schema invalid: %v", ex.Name, err))
				}
			}
		}
	}
	if ds.Comparator.Kind == "judge" {
		result.Warns = append(result.Warns, "judge comparator requires passing evidence for the judge call site before promotion")
	}
	return result, nil
}

func ResolveCallSite(def *app.AppDef, call string) (CallSite, error) {
	var matches []CallSite
	var walkState func(path string, st *app.State)
	walkState = func(path string, st *app.State) {
		if st == nil {
			return
		}
		walkEffects(path, st.OnEnter, &matches)
		for _, transitions := range st.On {
			for _, tr := range transitions {
				walkEffects(path, tr.Effects, &matches)
			}
		}
		keys := make([]string, 0, len(st.States))
		for k := range st.States {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			walkState(childPath, st.States[k])
		}
	}
	keys := make([]string, 0, len(def.States))
	for k := range def.States {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		walkState(k, def.States[k])
	}
	for _, m := range matches {
		if m.Call == call {
			return m, nil
		}
	}
	return CallSite{}, fmt.Errorf("call site %q not found; add id: %s to the target host.agent.* invoke", call, call)
}

func walkEffects(path string, effects []app.Effect, out *[]CallSite) {
	for i, e := range effects {
		if strings.HasPrefix(e.Invoke, "host.agent.") {
			prompt := stringArg(e.With, "prompt_path")
			if prompt == "" {
				prompt = stringArg(e.With, "prompt")
			}
			schema := stringArg(e.With, "schema")
			selection := ""
			if e.Selection != nil {
				selection = e.Selection.Evidence
			}
			*out = append(*out, CallSite{
				Call:         e.Id,
				Handler:      e.Invoke,
				Agent:        stringArg(e.With, "agent"),
				PromptPath:   prompt,
				SchemaPath:   schema,
				SchemaName:   filepath.Base(schema),
				EffectIndex:  i,
				StatePath:    path,
				SelectionRef: selection,
			})
		}
		if len(e.OnComplete) > 0 {
			walkEffects(path, e.OnComplete, out)
		}
		if len(e.Effects) > 0 {
			walkEffects(path, e.Effects, out)
		}
	}
}

func stringArg(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func cleanJoin(base, rel string) string {
	if rel == "" || filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Clean(filepath.Join(base, rel))
}

func fileHash(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func toolboxHash(def *app.AppDef, agentName string) string {
	var tools []string
	if agentName != "" && def.Agents != nil {
		if decl := def.Agents[agentName]; decl != nil {
			tools = append(tools, decl.Tools...)
		}
	}
	sort.Strings(tools)
	sum := sha256.Sum256([]byte(strings.Join(tools, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
