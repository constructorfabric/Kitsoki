// Package projectprofile validates Kitsoki project-profile/v1 documents.
package projectprofile

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	goyaml "github.com/goccy/go-yaml"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.json
var schemaBytes []byte

// Result is the machine-readable validation report.
type Result struct {
	OK       bool     `json:"ok"`
	Schema   []string `json:"schema,omitempty"`
	Semantic []string `json:"semantic,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ValidateFile validates a YAML or JSON project profile at path.
func ValidateFile(path, repoRoot string) (Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read profile: %w", err)
	}
	if repoRoot == "" {
		repoRoot = filepath.Dir(path)
	}
	if abs, err := filepath.Abs(repoRoot); err == nil {
		repoRoot = abs
	}
	return Validate(raw, repoRoot)
}

// Validate validates project-profile/v1 bytes. The repoRoot is used for
// semantic checks that need filesystem context; pass "" to skip those checks.
func Validate(raw []byte, repoRoot string) (Result, error) {
	doc, err := decodeYAML(raw)
	if err != nil {
		return Result{}, err
	}
	res := Result{}
	res.Schema = validateSchema(doc)
	res.Semantic, res.Warnings = validateSemantic(doc, repoRoot)
	res.OK = len(res.Schema) == 0 && len(res.Semantic) == 0
	return res, nil
}

// SchemaJSON returns the embedded project-profile/v1 JSON Schema.
func SchemaJSON() []byte {
	out := make([]byte, len(schemaBytes))
	copy(out, schemaBytes)
	return out
}

func decodeYAML(raw []byte) (map[string]any, error) {
	var doc map[string]any
	if err := goyaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse profile yaml: %w", err)
	}
	normalized := normalize(doc)
	root, ok := normalized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("profile must be an object")
	}
	return root, nil
}

func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = normalize(v)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[fmt.Sprint(k)] = normalize(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalize(v)
		}
		return out
	default:
		return v
	}
}

func validateSchema(doc map[string]any) []string {
	var schemaVal any
	if err := json.Unmarshal(schemaBytes, &schemaVal); err != nil {
		return []string{"embedded project-profile schema is invalid JSON: " + err.Error()}
	}
	compiler := jsonschema.NewCompiler()
	const uri = "kitsoki://project-profile/v1"
	if err := compiler.AddResource(uri, schemaVal); err != nil {
		return []string{"embedded project-profile schema is invalid: " + err.Error()}
	}
	schema, err := compiler.Compile(uri)
	if err != nil {
		return []string{"embedded project-profile schema does not compile: " + err.Error()}
	}
	if err := schema.Validate(doc); err != nil {
		return formatValidationErrors(err)
	}
	return nil
}

func formatValidationErrors(err error) []string {
	verr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{err.Error()}
	}
	var out []string
	var walk func(*jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			out = append(out, e.Error())
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(verr)
	sort.Strings(out)
	return out
}

func validateSemantic(doc map[string]any, repoRoot string) ([]string, []string) {
	var errs []string
	var warnings []string

	id := stringAt(doc, "id")
	kitsoki := mapAt(doc, "kitsoki")
	instance := mapAt(kitsoki, "instance")
	instanceID := stringAt(instance, "id")
	instancePath := stringAt(instance, "path")
	if id != "" {
		wantID := id + "-dev"
		if instanceID != "" && instanceID != wantID {
			errs = append(errs, fmt.Sprintf("kitsoki.instance.id = %q, want %q", instanceID, wantID))
		}
		wantPath := ".kitsoki/stories/" + wantID + "/app.yaml"
		if instancePath != "" && filepath.ToSlash(instancePath) != wantPath {
			errs = append(errs, fmt.Sprintf("kitsoki.instance.path = %q, want %q", instancePath, wantPath))
		}
	}
	if story := stringAt(kitsoki, "story"); story != "" && story != "dev-story" {
		errs = append(errs, fmt.Sprintf("kitsoki.story = %q, want dev-story", story))
	}
	bindings := mapAt(instance, "bindings")
	for _, key := range []string{"ticket", "vcs", "ci", "workspace", "transport"} {
		if strings.TrimSpace(stringAt(bindings, key)) == "" {
			errs = append(errs, "kitsoki.instance.bindings."+key+" is required")
		}
	}

	commands := mapAt(doc, "commands")
	testCmd := strings.TrimSpace(stringAt(commands, "test"))
	buildCmd := strings.TrimSpace(stringAt(commands, "build"))
	if testCmd == "" {
		warnings = append(warnings, "commands.test is empty")
	}
	if buildCmd == "" {
		warnings = append(warnings, "commands.build is empty")
	}
	if repoRoot != "" {
		for _, rel := range []string{".kitsoki.yaml", filepath.FromSlash(instancePath)} {
			if rel == "" {
				continue
			}
			if strings.HasPrefix(filepath.ToSlash(rel), "../") || filepath.IsAbs(rel) {
				errs = append(errs, fmt.Sprintf("%s must stay inside the project checkout", rel))
				continue
			}
		}
	}

	setup := mapAt(doc, "setup_plan")
	if len(setup) > 0 {
		writes, _ := setup["writes"].([]any)
		if instancePath != "" && !writeListContains(writes, filepath.ToSlash(instancePath)) {
			warnings = append(warnings, "setup_plan.writes does not mention kitsoki.instance.path")
		}
	}

	return errs, warnings
}

func mapAt(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return v
}

func stringAt(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	default:
		return ""
	}
}

func writeListContains(writes []any, path string) bool {
	for _, item := range writes {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if filepath.ToSlash(stringAt(m, "path")) == path {
			return true
		}
	}
	return false
}
