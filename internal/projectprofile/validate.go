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

	tracker := mapAt(doc, "tracker")
	sources, _ := tracker["sources"].([]any)
	if len(sources) > 0 {
		seenSourceIDs := map[string]struct{}{}
		seenSourceLabels := map[string]struct{}{}
		for _, raw := range sources {
			source := mapValue(raw)
			id := strings.TrimSpace(stringAt(source, "id"))
			if id == "" {
				continue // JSON Schema reports missing and malformed ids.
			}
			if _, exists := seenSourceIDs[id]; exists {
				errs = append(errs, fmt.Sprintf("tracker.sources id %q is declared more than once", id))
			}
			seenSourceIDs[id] = struct{}{}
			label := strings.TrimSpace(stringAt(source, "label"))
			labelKey := strings.ToLower(label)
			if labelKey != "" {
				if _, exists := seenSourceLabels[labelKey]; exists {
					errs = append(errs, fmt.Sprintf("tracker.sources label %q is declared more than once", label))
				}
				seenSourceLabels[labelKey] = struct{}{}
			}
		}
		if ticketBinding := strings.TrimSpace(stringAt(bindings, "ticket")); ticketBinding != "host.ticket_federation" {
			errs = append(errs, fmt.Sprintf("kitsoki.instance.bindings.ticket = %q, want %q when tracker.sources is configured", ticketBinding, "host.ticket_federation"))
		}
	} else if strings.TrimSpace(stringAt(bindings, "ticket")) == "host.ticket_federation" {
		errs = append(errs, "tracker.sources must contain at least one source when kitsoki.instance.bindings.ticket = \"host.ticket_federation\"")
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

	setup := mapAt(doc, "setup_plan")
	verificationGates := map[string]string{}
	if len(setup) > 0 {
		writes, _ := setup["writes"].([]any)
		if instancePath != "" && !writeListContains(writes, filepath.ToSlash(instancePath)) {
			warnings = append(warnings, "setup_plan.writes does not mention kitsoki.instance.path")
		}
		verifications, _ := setup["verifications"].([]any)
		for _, raw := range verifications {
			verification := mapValue(raw)
			verificationID := strings.TrimSpace(stringAt(verification, "id"))
			if verificationID != "" {
				verificationGates[verificationID] = strings.TrimSpace(stringAt(verification, "gate"))
			}
		}
	}

	goals := mapAt(doc, "goals")
	if len(goals) == 0 {
		warnings = append(warnings, "goals is missing; legacy profile has no explicit story or phase goal contract")
	} else {
		errs = append(errs, validateGoalVerifications(goals, verificationGates)...)
	}

	onboarding := mapAt(doc, "onboarding")
	resolutions, _ := onboarding["resolutions"].([]any)
	if len(resolutions) == 0 {
		warnings = append(warnings, "onboarding.resolutions is missing; legacy profile has no per-field provenance or default-update guidance")
	} else {
		errs = append(errs, validateResolutions(resolutions)...)
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

	return errs, warnings
}

func validateGoalVerifications(goals map[string]any, verificationGates map[string]string) []string {
	goalIDs := make([]string, 0, len(goals))
	for goalID := range goals {
		goalIDs = append(goalIDs, goalID)
	}
	sort.Strings(goalIDs)

	var errs []string
	for _, goalID := range goalIDs {
		goal := mapValue(goals[goalID])
		postconditions, _ := goal["postconditions"].([]any)
		for index, raw := range postconditions {
			postcondition := mapValue(raw)
			verificationID := strings.TrimSpace(stringAt(postcondition, "verification"))
			if verificationID == "" {
				continue // JSON Schema reports the missing or malformed reference.
			}
			postconditionID := strings.TrimSpace(stringAt(postcondition, "id"))
			if postconditionID == "" {
				postconditionID = fmt.Sprintf("postconditions[%d]", index)
			}
			verificationGate, ok := verificationGates[verificationID]
			if !ok {
				errs = append(errs, fmt.Sprintf("goals.%s postcondition %q references unknown setup_plan.verifications id %q", goalID, postconditionID, verificationID))
				continue
			}
			if stringAt(postcondition, "gate") == "required" && verificationGate != "required" {
				errs = append(errs, fmt.Sprintf("goals.%s postcondition %q is required but verification %q is %s", goalID, postconditionID, verificationID, verificationGate))
			}
		}
	}
	return errs
}

func validateResolutions(resolutions []any) []string {
	seen := map[string]struct{}{}
	var errs []string
	for index, raw := range resolutions {
		resolution := mapValue(raw)
		field := strings.TrimSpace(stringAt(resolution, "field"))
		if field != "" {
			if _, exists := seen[field]; exists {
				errs = append(errs, fmt.Sprintf("onboarding.resolutions field %q is declared more than once", field))
			} else {
				seen[field] = struct{}{}
			}
		}
		if stringAt(resolution, "source") != "default" {
			continue
		}
		if strings.TrimSpace(stringAt(resolution, "notice")) == "" {
			errs = append(errs, fmt.Sprintf("onboarding.resolutions[%d].notice is required when source = \"default\"", index))
		}
		if strings.TrimSpace(stringAt(resolution, "update")) == "" {
			errs = append(errs, fmt.Sprintf("onboarding.resolutions[%d].update is required when source = \"default\"", index))
		}
	}
	return errs
}

func mapAt(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return v
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
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
