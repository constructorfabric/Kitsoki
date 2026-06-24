package journal

import (
	"encoding/json"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch/v5"

	"kitsoki/internal/app"
)

// Applier applies a sequence of RFC 6902 patch ops to a JSON document.
// For the "world" document, it is schema-aware: after applying ops it
// re-coerces numeric fields that encoding/json would otherwise return
// as float64 back to their declared Go types.
type Applier interface {
	// Apply applies ops to current and returns the updated document.
	Apply(doc DocID, current json.RawMessage, ops []PatchOp) (json.RawMessage, error)
}

// engineInjectedKeys is the set of world-var keys that are maintained by the
// engine itself rather than declared in app.WorldSchema. They round-trip via
// plain JSON without schema-aware coercion: they are not declared world vars,
// so there is no schema type to coerce them back to.
var engineInjectedKeys = map[string]struct{}{
	"$inbox":     {},
	"$proposal":  {},
	"last_error": {},
}

// NewApplier returns an Applier. schema may be nil for tests that don't
// exercise world-coercion. It is used only for the "world" document; all
// other documents use plain RFC 6902.
func NewApplier(schema app.WorldSchema) Applier {
	return &schemaAwareApplier{schema: schema}
}

type schemaAwareApplier struct {
	schema app.WorldSchema
}

func (a *schemaAwareApplier) Apply(doc DocID, current json.RawMessage, ops []PatchOp) (json.RawMessage, error) {
	if len(current) == 0 {
		current = []byte("null")
	}

	// Convert our PatchOp slice to the evanphx library's JSON representation.
	patchJSON, err := marshalOps(ops)
	if err != nil {
		return nil, fmt.Errorf("journal/applier: marshal ops: %w", err)
	}

	patch, err := jsonpatch.DecodePatch(patchJSON)
	if err != nil {
		return nil, fmt.Errorf("journal/applier: decode patch: %w", err)
	}

	result, err := patch.Apply(current)
	if err != nil {
		return nil, fmt.Errorf("journal/applier: apply patch to %s: %w", doc, err)
	}

	// For the world document, re-coerce vars to their declared Go types.
	if doc == "world" && a.schema != nil {
		result, err = coerceWorldDocument(result, a.schema)
		if err != nil {
			return nil, fmt.Errorf("journal/applier: coerce world: %w", err)
		}
	}

	return result, nil
}

// marshalOps converts our []PatchOp into a JSON array suitable for
// jsonpatch.DecodePatch.
func marshalOps(ops []PatchOp) ([]byte, error) {
	// Build a slice of maps so we can omit Value when it is nil.
	type opWire struct {
		Op    string          `json:"op"`
		Path  string          `json:"path"`
		Value json.RawMessage `json:"value,omitempty"`
		From  string          `json:"from,omitempty"`
	}
	wire := make([]opWire, len(ops))
	for i, op := range ops {
		wire[i] = opWire{
			Op:    op.Op,
			Path:  op.Path,
			Value: op.Value,
		}
	}
	return json.Marshal(wire)
}

// CoerceWorldVars applies schema-aware type coercion to the vars map decoded
// from a world document, in place. It mirrors the coerceWorldVar logic in
// internal/store/replay.go. Call this after json.Unmarshal of a world
// document body when the caller needs properly-typed Go values in world.Vars.
//
// Engine-injected keys ($inbox, $proposal, last_error, $jobs.*) are skipped.
// Keys not declared in schema pass through unchanged.
func CoerceWorldVars(vars map[string]any, schema app.WorldSchema) {
	if schema == nil {
		return
	}
	for k, v := range vars {
		if _, injected := engineInjectedKeys[k]; injected {
			continue
		}
		if len(k) > 6 && k[:6] == "$jobs." {
			continue
		}
		vd, declared := schema[k]
		if !declared {
			continue
		}
		vars[k] = coerceVar(vd.Type, v)
	}
}

// coerceWorldDocument walks the decoded world document and applies
// schema-aware type coercion to world.vars entries, mirroring
// internal/store/replay.go coerceWorldVar.
//
// Note: coercion affects the in-memory map[string]any representation. When
// the result is re-marshalled to JSON and the caller does json.Unmarshal into
// map[string]any again, numeric types will be float64 once more — this is a
// fundamental JSON limitation. Callers that need typed world vars should call
// CoerceWorldVars after their own unmarshal, or consume the result via a
// schema-aware world loader.
func coerceWorldDocument(raw json.RawMessage, schema app.WorldSchema) (json.RawMessage, error) {
	// Decode as a generic map.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}

	// Navigate to the vars object. World documents have shape {"vars": {...}}.
	varsRaw, ok := doc["vars"]
	if !ok {
		// No vars key — nothing to coerce.
		return raw, nil
	}

	vars, ok := varsRaw.(map[string]any)
	if !ok {
		return raw, nil
	}

	CoerceWorldVars(vars, schema)

	return json.Marshal(doc)
}

// coerceVar applies type coercion for a single world variable.
// It mirrors internal/store/replay.go coerceWorldVar.
func coerceVar(typ string, v any) any {
	switch typ {
	case "int":
		switch x := v.(type) {
		case float64:
			return int64(x)
		case float32:
			return int64(x)
		case int:
			return int64(x)
		case int32:
			return int64(x)
		case int64:
			return x
		}
	case "float":
		switch x := v.(type) {
		case int64:
			return float64(x)
		case int:
			return float64(x)
		case float32:
			return float64(x)
		case float64:
			return x
		}
	case "bool":
		switch x := v.(type) {
		case bool:
			return x
		case float64:
			// JSON doesn't produce float64 for bools, but guard anyway.
			return x != 0
		}
		// "string", "object", "array" — pass through as-is.
	}
	return v
}
