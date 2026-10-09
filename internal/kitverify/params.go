package kitverify

import (
	"fmt"
	"sort"

	"kitsoki/internal/kit"
)

// CheckParameters validates a kit manifest's `parameters:` block, optionally
// against a caller-supplied set of provided values (e.g. what a downstream
// consumer intends to pass via app.KitImportSpec.Parameters).
//
//   - provided == nil: manifest-only invariants. A declared `default:`'s Go
//     value must match its declared `type:` (a kit manifest is
//     schema-valid YAML long before anyone checks that its default is even
//     the right shape — internal/kit's jsonschema pass only requires
//     `type:` to be one of the enum values, it never looks at `default:`
//     at all). A non-required parameter with no default is also flagged:
//     nothing downstream (BuildKitImporter/SynthesizeKit) invents a value
//     for it, so an importer that simply omits the key gets no value ever
//     — almost certainly an authoring mistake (either the parameter should
//     be required, or it should carry a default).
//   - provided != nil: additionally checks that every required parameter
//     is present (or has a manifest default) and every provided value's Go
//     type matches the declared type. An unknown provided key is flagged
//     too — BuildKitImporter enforces this already, fail-fast at synthesis
//     time (synthesis.go:364-370), but duplicating the check here lets
//     `kitsoki kit verify` catch it standalone, without synthesizing an
//     importer.
func CheckParameters(manifest *kit.Def, provided map[string]any) []string {
	if manifest == nil {
		return nil
	}
	var out []string
	names := sortedParamNames(manifest.Parameters)

	for _, name := range names {
		p := manifest.Parameters[name]
		if p.Default != nil && !valueMatchesParamType(p.Default, p.Type) {
			out = append(out, fmt.Sprintf("parameters.%s: default %v does not match declared type %q", name, p.Default, p.Type))
		}
		if !p.Required && p.Default == nil {
			out = append(out, fmt.Sprintf("parameters.%s: not required but has no default — a consumer that omits it gets no value", name))
		}
	}

	if provided == nil {
		return out
	}

	providedNames := make([]string, 0, len(provided))
	for name := range provided {
		providedNames = append(providedNames, name)
	}
	sort.Strings(providedNames)
	for _, name := range providedNames {
		if _, ok := manifest.Parameters[name]; !ok {
			out = append(out, fmt.Sprintf("parameters.%s: provided but not declared in the kit manifest", name))
		}
	}

	for _, name := range names {
		p := manifest.Parameters[name]
		v, has := provided[name]
		if !has {
			if p.Required && p.Default == nil {
				out = append(out, fmt.Sprintf("parameters.%s: required but not provided (and no default)", name))
			}
			continue
		}
		if !valueMatchesParamType(v, p.Type) {
			out = append(out, fmt.Sprintf("parameters.%s: provided value %v does not match declared type %q", name, v, p.Type))
		}
	}

	return out
}

func sortedParamNames(m map[string]kit.Parameter) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// valueMatchesParamType checks a Go value decoded from YAML against a
// kit/v1 parameter type name (string|int|float|bool|object|list).
func valueMatchesParamType(v any, t string) bool {
	switch t {
	case "string":
		_, ok := v.(string)
		return ok
	case "int":
		switch v.(type) {
		case int, int64, int32, uint, uint64:
			return true
		default:
			return false
		}
	case "float":
		switch v.(type) {
		case float32, float64, int, int64:
			return true
		default:
			return false
		}
	case "bool":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "list":
		_, ok := v.([]any)
		return ok
	default:
		// Unknown type name: internal/kit's schema already constrains
		// `type:` to the enum, so this is unreachable for a schema-valid
		// manifest. Fail open rather than block on an unrelated bug.
		return true
	}
}
