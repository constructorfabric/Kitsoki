package starlark

import (
	"fmt"
	"os"

	goyaml "github.com/goccy/go-yaml"
)

// Sidecar is the parsed .star.yaml that sits beside a script. It is the
// AUTHORITATIVE declaration of a script's interface: the engine validates the
// effect's inputs against Inputs before evaluation and validates main()'s
// return dict against Outputs after. The in-script INPUTS/OUTPUTS dicts some
// authors write are convention/documentation only — the engine ignores them.
//
// Keeping the contract in a sidecar (rather than inferring it from the script)
// lets the loader fail an app at load time with an actionable message when a
// script's declared interface is malformed, long before any turn runs.
type Sidecar struct {
	// Inputs maps input name -> field spec. An input the effect omits that is
	// declared required: true fails the boundary check.
	Inputs map[string]FieldSpec `yaml:"inputs,omitempty"`
	// Outputs maps output name -> field spec. A returned key not declared here
	// is rejected; a declared key absent from the return is rejected unless its
	// spec is not required (outputs are required by default — see validate).
	Outputs map[string]FieldSpec `yaml:"outputs,omitempty"`
}

// FieldSpec declares the type and (for inputs) requiredness of one field.
//
// Type is one of: string, int, number, bool, object, list, any. "int" and
// "number" are both accepted for numeric values (Starlark/JSON do not
// distinguish them cleanly across the boundary); "any" disables the type check.
type FieldSpec struct {
	Type     string `yaml:"type,omitempty"`
	Required bool   `yaml:"required,omitempty"`
}

// LoadSidecar reads and parses the sidecar YAML at path. It returns a typed
// error when the file cannot be read or is not valid YAML so the loader can
// surface a precise, actionable load-time failure.
func LoadSidecar(path string) (*Sidecar, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("starlark sidecar: read %q: %w", path, err)
	}
	var sc Sidecar
	if err := goyaml.Unmarshal(raw, &sc); err != nil {
		return nil, fmt.Errorf("starlark sidecar: parse %q: %w", path, err)
	}
	if err := sc.Validate(); err != nil {
		return nil, fmt.Errorf("starlark sidecar %q: %w", path, err)
	}
	return &sc, nil
}

// ParseSidecar parses sidecar YAML from bytes (used at load time when the
// loader already has the file content). Same validation as LoadSidecar.
func ParseSidecar(raw []byte) (*Sidecar, error) {
	var sc Sidecar
	if err := goyaml.Unmarshal(raw, &sc); err != nil {
		return nil, fmt.Errorf("starlark sidecar: parse: %w", err)
	}
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	return &sc, nil
}

// Validate checks that every declared type is one this package understands.
// Called at parse time so a typo (type: strnig) fails the app load rather than
// silently disabling the check at runtime.
func (sc *Sidecar) Validate() error {
	for name, f := range sc.Inputs {
		if !knownType(f.Type) {
			return fmt.Errorf("input %q: unknown type %q (want string|int|number|bool|object|list|any)", name, f.Type)
		}
	}
	for name, f := range sc.Outputs {
		if reason, ok := reservedOutputReason(name); ok {
			return fmt.Errorf("output %q is reserved by host.starlark.run for %s; choose another name", name, reason)
		}
		if !knownType(f.Type) {
			return fmt.Errorf("output %q: unknown type %q (want string|int|number|bool|object|list|any)", name, f.Type)
		}
	}
	return nil
}

// knownType reports whether t is a recognised field type. The empty type is
// treated as "any" so a bare `name: {}` spec is permissive rather than an error.
func knownType(t string) bool {
	switch t {
	case "", "any", "string", "int", "number", "bool", "object", "list":
		return true
	default:
		return false
	}
}

// validateInputs checks the supplied input values against the sidecar BEFORE
// evaluation. A missing required input or a type mismatch yields a domain-error
// string (returned via the *DomainError sentinel) rather than a panic, so the
// orchestrator routes it to the effect's on_error: arc. Inputs not declared in
// the sidecar are passed through untouched (the script may read them via
// ctx.inputs, but the engine does not police undeclared inputs).
func (sc *Sidecar) validateInputs(inputs map[string]any) error {
	for name, spec := range sc.Inputs {
		v, present := inputs[name]
		// A present-but-nil value is indistinguishable, for validation purposes,
		// from an absent one: an effect's with.inputs block commonly threads an
		// optional value straight from a template expression (e.g.
		// `"{{ world.trace_run_id }}"`), and an undefined world var renders as
		// Go nil rather than omitting the key. Treating that the same as "the
		// author didn't pass this input" matches the documented contract for a
		// non-required field (the script defaults it via ctx.inputs.get(name,
		// default)) instead of hard-failing on a type mismatch the author never
		// intended to assert.
		if !present || v == nil {
			if spec.Required {
				return &DomainError{msg: fmt.Sprintf("missing required input %q", name)}
			}
			continue
		}
		if !valueMatchesType(v, spec.Type) {
			return &DomainError{msg: fmt.Sprintf("input %q: expected %s, got %T", name, normType(spec.Type), v)}
		}
	}
	return nil
}

// validateOutputs checks main()'s returned outputs against the sidecar AFTER
// evaluation. Every declared output must be present (outputs are required by
// default — a script that forgets to return one is a bug worth surfacing), and
// every returned value must match its declared type. Outputs returned but not
// declared are rejected so the world-binding surface stays exactly what the
// sidecar promises.
func (sc *Sidecar) validateOutputs(outputs map[string]any) error {
	for name := range outputs {
		if reason, ok := reservedOutputReason(name); ok {
			return &DomainError{msg: fmt.Sprintf("script returned reserved output %q (used for %s)", name, reason)}
		}
		if _, ok := sc.Outputs[name]; !ok && len(sc.Outputs) > 0 {
			return &DomainError{msg: fmt.Sprintf("script returned undeclared output %q", name)}
		}
	}
	for name, spec := range sc.Outputs {
		v, present := outputs[name]
		if !present {
			return &DomainError{msg: fmt.Sprintf("script did not return declared output %q", name)}
		}
		if !valueMatchesType(v, spec.Type) {
			return &DomainError{msg: fmt.Sprintf("output %q: expected %s, got %T", name, normType(spec.Type), v)}
		}
	}
	return nil
}

// ValueMatchesType is the exported predicate behind validateInputs, for
// static (load-time) callers — the loader checks a non-templated literal input
// value against its declared sidecar type so a mismatch that would only surface
// at runtime (e.g. a bare string wired to a declared `int`) is rejected up
// front. A string is valid only for `string`/`any`: there is NO coercion, so a
// non-template string against a non-string type can never satisfy it.
func ValueMatchesType(v any, t string) bool { return valueMatchesType(v, t) }

// NormType renders a sidecar field type for diagnostics, mapping "" to "any".
func NormType(t string) string { return normType(t) }

func reservedOutputReason(name string) (string, bool) {
	switch name {
	case ExchangesOutputKey:
		return "HTTP exchange summaries", true
	case InspectionsOutputKey:
		return "fs/probe inspection summaries", true
	default:
		return "", false
	}
}

// valueMatchesType reports whether the Go value (already converted from
// Starlark) conforms to the declared type. The empty/any type always matches.
func valueMatchesType(v any, t string) bool {
	switch t {
	case "", "any":
		return true
	case "string":
		_, ok := v.(string)
		return ok
	case "bool":
		_, ok := v.(bool)
		return ok
	case "int", "number":
		switch v.(type) {
		case int, int64, float64:
			return true
		default:
			return false
		}
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "list":
		_, ok := v.([]any)
		return ok
	default:
		return false
	}
}

// normType renders a type for error messages, mapping the empty type to "any".
func normType(t string) string {
	if t == "" {
		return "any"
	}
	return t
}
