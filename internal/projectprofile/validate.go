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

	"kitsoki/internal/app"
)

// defaultRootBindingKeys is the fallback set of required kitsoki.instance.
// bindings.<key> entries used when the declared kitsoki.story cannot be
// resolved to a kit manifest at all (no on-disk checkout, or an unknown
// story name — see validateSemantic). It mirrors dev-story's own declared
// root.host_interfaces (stories/dev-story/kit.yaml) — the v1 default before
// D6 (root generalization) made this manifest-driven rather than hardcoded.
var defaultRootBindingKeys = []string{"ticket", "vcs", "ci", "workspace", "transport"}

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
	// D6 (root generalization): kitsoki.story is no longer required to be
	// the literal string "dev-story" — any installed kit whose kit.yaml
	// declares a root: block naming it qualifies (app.ResolveRootKit). An
	// empty story defaults to app.DefaultRootKitName, same as
	// webconfig/synthesis. The resolved manifest's root.host_interfaces
	// becomes the required kitsoki.instance.bindings.<key> set, replacing
	// the formerly hardcoded ticket/vcs/ci/workspace/transport list —
	// falling back to that same list (defaultRootBindingKeys) only when the
	// story can't be resolved at all (no on-disk checkout, or truly unknown
	// story name), so the bindings check still fires something useful.
	story := stringAt(kitsoki, "story")
	resolveName := story
	if resolveName == "" {
		resolveName = app.DefaultRootKitName
	}
	bindingKeys := defaultRootBindingKeys
	if manifest, mErr := app.ResolveRootKit(resolveName, repoRoot, nil); mErr != nil {
		if story != "" && story != app.DefaultRootKitName {
			// An explicit, non-default kitsoki.story that fails to resolve is
			// always a profile error — surface it. The default name resolving
			// to nothing just means no on-disk/repoRoot context was given to
			// check against (e.g. a bare --profile-json call with an
			// arbitrary --repo-root); skip rather than fail, mirroring
			// webconfig.resolveRoot's identical default-unresolvable case.
			errs = append(errs, fmt.Sprintf("kitsoki.story = %q is not a known root kit: %v", story, mErr))
		}
	} else if ifaces := manifest.RootHostInterfaces(); len(ifaces) > 0 {
		keys := make([]string, 0, len(ifaces))
		for k := range ifaces {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		bindingKeys = keys
	}
	bindings := mapAt(instance, "bindings")
	for _, key := range bindingKeys {
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
