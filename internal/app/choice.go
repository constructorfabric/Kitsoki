// choice.go — schema for the choice / multi-select / form widget.
// Author-facing reference: docs/stories/choice-widget.md.
//
// Phase A is schema + types + load-time validation only. No rendering,
// no TUI behavior changes. The renderer (Phase B) and the interactive
// widget (Phase C) consume the typed fields wired here.
//
// The shape of a `choice:` element is governed by the embedded JSON
// Schema at schemas/choice.schema.json (mirrored to
// docs/embedded/schemas/choice.schema.json for IDE consumption). The
// per-element validate() path re-marshals the parsed YAML subtree to
// JSON, hands it to the schema, then compiles every expression-bearing
// field through internal/expr and internal/render to surface load-time
// errors for typos / undefined identifiers.

package app

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	goyaml "github.com/goccy/go-yaml"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"kitsoki/internal/expr"
	"kitsoki/internal/render"
)

// ChoiceSchemaRaw is the embedded draft 2020-12 JSON Schema for the
// `choice:` view element. Exported so other packages (e.g. an IDE-helper
// CLI) can publish it.
//
//go:embed schemas/choice.schema.json
var ChoiceSchemaRaw []byte

// choiceSchema is the compiled schema. Lazily compiled on first use
// via choiceSchemaOnce so packages that never touch a choice element
// don't pay the parse cost.
var (
	choiceSchema     *jsonschema.Schema
	choiceSchemaOnce sync.Once
	choiceSchemaErr  error
)

// compileChoiceSchema parses ChoiceSchemaRaw and produces a compiled
// jsonschema.Schema. Cached behind sync.Once.
func compileChoiceSchema() (*jsonschema.Schema, error) {
	choiceSchemaOnce.Do(func() {
		var doc any
		if err := json.Unmarshal(ChoiceSchemaRaw, &doc); err != nil {
			choiceSchemaErr = fmt.Errorf("compile choice schema: parse: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("choice.schema.json", doc); err != nil {
			choiceSchemaErr = fmt.Errorf("compile choice schema: add resource: %w", err)
			return
		}
		s, err := c.Compile("choice.schema.json")
		if err != nil {
			choiceSchemaErr = fmt.Errorf("compile choice schema: compile: %w", err)
			return
		}
		choiceSchema = s
	})
	return choiceSchema, choiceSchemaErr
}

// ChoiceItem is one entry in a `choice:` element's items list.
//
// Modes use different subsets:
//   - single: Label (required), Intent (required), Hint, Slots, Param, When
//   - multi:  Value (required), Label (default = Value), Hint, When
//
// The discriminator is the enclosing ViewElement.ChoiceMode.
type ChoiceItem struct {
	Value  string         // multi mode (required)
	Label  string         // single mode (required); multi optional (default = Value)
	Hint   string         // optional right-column hint
	Intent string         // single mode (required)
	Slots  map[string]any // single mode pre-bound slots
	Param  *ChoiceParam   // single mode optional one-shot slot capture
	When   string         // per-item expr-lang guard
}

// ChoiceParam describes a one-shot free-form slot capture attached to a
// `single`-mode item. Only meaningful when Type == "enum" if Values is
// populated; the JSON Schema enforces the required-when rule.
type ChoiceParam struct {
	Slot        string
	Type        string // "string" | "int" | "enum"
	Placeholder string
	Values      []string
	Required    bool
}

// ChoiceField is one editable / readonly field inside a `form`-mode
// element. Fields are stored in author-declared YAML order so the
// mad-lib template renders deterministically; Name is lifted from the
// `fields:` map key.
type ChoiceField struct {
	Name        string // YAML map key, lifted here for ordered iteration
	Type        string // "string" | "int" | "float" | "bool" | "enum"
	Hint        string
	Placeholder string
	Unit        string // optional unit suffix rendered after the value (e.g. "head", "lbs")
	Values      []string
	Default     any
	Min         any
	Max         any
	Required    bool
	Readonly    bool
	Expr        string
	When        string
}

// ---- YAML decoder shape -----------------------------------------------------

// rawChoiceYAML is the lossy decoder for a `choice:` element. Every
// field is optional at this layer; the JSON Schema enforces
// required/oneOf rules during validate(). We capture the body
// verbatim via goyaml.MapSlice so the schema validator sees author
// insertion order (matters for diagnostic locations).
type rawChoiceYAML struct {
	body goyaml.MapSlice
}

// UnmarshalYAML grabs the choice subtree as a goyaml.MapSlice so we
// preserve author-declared property order. The strict-unmarshal would
// fail on unknown keys; deferring that to the JSON Schema gives
// uniform diagnostics.
func (r *rawChoiceYAML) UnmarshalYAML(data []byte) error {
	var ms goyaml.MapSlice
	// UseOrderedMap propagates MapSlice into nested mappings; without
	// it, anything beyond the top level decodes as map[string]any and
	// we lose author iteration order on the fields: mapping.
	if err := goyaml.UnmarshalWithOptions(data, &ms, goyaml.UseOrderedMap()); err != nil {
		return fmt.Errorf("choice: must be a mapping: %w", err)
	}
	r.body = ms
	return nil
}

// resolve walks r.body and lifts the typed fields onto the supplied
// ViewElement. It also stashes the body re-marshalled to JSON on
// ChoiceRaw so validate() can hand it to the JSON Schema.
//
// resolve does NOT enforce per-mode required-field rules — that's the
// JSON Schema's job, run from validate(). resolve is intentionally
// lossy-tolerant: it accepts whatever it can decode, leaves unset
// fields as their zero values, and trusts validate() to reject bad
// shapes with locatable errors.
func (r *rawChoiceYAML) resolve(out *ViewElement) error {
	// Re-marshal the parsed subtree to JSON for the schema validator.
	// We marshal the MapSlice via goyaml then convert to JSON because
	// goyaml.Marshal on a MapSlice preserves key order, which matters
	// for "fields:" specifically (the form template renders in author
	// order).
	jsonBytes, err := mapSliceToJSON(r.body)
	if err != nil {
		return fmt.Errorf("choice: re-marshal to JSON: %w", err)
	}
	out.ChoiceRaw = jsonBytes

	// Lift typed fields. Anything not in the known set is left to the
	// schema validator to reject via additionalProperties.
	for _, p := range r.body {
		key, _ := p.Key.(string)
		switch key {
		case "mode":
			if s, ok := p.Value.(string); ok {
				out.ChoiceMode = s
			}
		case "prompt":
			if s, ok := p.Value.(string); ok {
				out.ChoicePrompt = s
			}
		case "when":
			if s, ok := p.Value.(string); ok {
				out.When = s
			}
		case "intent":
			if s, ok := p.Value.(string); ok {
				out.ChoiceIntent = s
			}
		case "slot":
			if s, ok := p.Value.(string); ok {
				out.ChoiceSlot = s
			}
		case "min":
			if n, ok := toInt(p.Value); ok {
				out.ChoiceMin = n
				out.ChoiceMinSet = true
			}
		case "max":
			if n, ok := toInt(p.Value); ok {
				out.ChoiceMax = n
				out.ChoiceMaxSet = true
			}
		case "template":
			if s, ok := p.Value.(string); ok {
				out.ChoiceTemplate = s
			}
		case "items":
			items, err := liftChoiceItems(p.Value)
			if err != nil {
				return fmt.Errorf("choice items: %w", err)
			}
			out.ChoiceItems = items
		case "fields":
			fields, err := liftChoiceFields(p.Value)
			if err != nil {
				return fmt.Errorf("choice fields: %w", err)
			}
			out.ChoiceFields = fields
		}
	}

	// Default mode is "single" (see docs/stories/choice-widget.md). Setting it
	// here means downstream code can rely on ChoiceMode being
	// non-empty after a successful unmarshal even when the author
	// omitted the field — though the JSON Schema requires it
	// explicitly, the default keeps the runtime accessors simple.
	if out.ChoiceMode == "" {
		out.ChoiceMode = "single"
	}
	return nil
}

// liftChoiceItems decodes a slice-of-maps from MapSlice form into a
// typed []ChoiceItem. Unknown fields are tolerated (schema will catch
// them); we only lift what we'll consume downstream.
func liftChoiceItems(v any) ([]ChoiceItem, error) {
	slice, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("items: expected a sequence, got %T", v)
	}
	out := make([]ChoiceItem, 0, len(slice))
	for i, raw := range slice {
		ms, ok := raw.(goyaml.MapSlice)
		if !ok {
			// goyaml may decode as map[string]any depending on path.
			if m, mok := raw.(map[string]any); mok {
				ms = mapToMapSlice(m)
			} else {
				return nil, fmt.Errorf("items[%d]: expected a mapping, got %T", i, raw)
			}
		}
		item := ChoiceItem{}
		for _, p := range ms {
			key, _ := p.Key.(string)
			switch key {
			case "value":
				if s, ok := p.Value.(string); ok {
					item.Value = s
				}
			case "label":
				if s, ok := p.Value.(string); ok {
					item.Label = s
				}
			case "hint":
				if s, ok := p.Value.(string); ok {
					item.Hint = s
				}
			case "intent":
				if s, ok := p.Value.(string); ok {
					item.Intent = s
				}
			case "when":
				if s, ok := p.Value.(string); ok {
					item.When = s
				}
			case "slots":
				slots, err := mapSliceToStringMap(p.Value)
				if err != nil {
					return nil, fmt.Errorf("items[%d].slots: %w", i, err)
				}
				item.Slots = slots
			case "param":
				param, err := liftChoiceParam(p.Value)
				if err != nil {
					return nil, fmt.Errorf("items[%d].param: %w", i, err)
				}
				item.Param = param
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// liftChoiceParam decodes a single param mapping.
func liftChoiceParam(v any) (*ChoiceParam, error) {
	ms, ok := v.(goyaml.MapSlice)
	if !ok {
		if m, mok := v.(map[string]any); mok {
			ms = mapToMapSlice(m)
		} else {
			return nil, fmt.Errorf("expected a mapping, got %T", v)
		}
	}
	p := &ChoiceParam{}
	for _, kv := range ms {
		key, _ := kv.Key.(string)
		switch key {
		case "slot":
			if s, ok := kv.Value.(string); ok {
				p.Slot = s
			}
		case "type":
			if s, ok := kv.Value.(string); ok {
				p.Type = s
			}
		case "placeholder":
			if s, ok := kv.Value.(string); ok {
				p.Placeholder = s
			}
		case "required":
			if b, ok := kv.Value.(bool); ok {
				p.Required = b
			}
		case "values":
			vals, err := stringSlice(kv.Value)
			if err != nil {
				return nil, fmt.Errorf("values: %w", err)
			}
			p.Values = vals
		}
	}
	return p, nil
}

// liftChoiceFields decodes a fields: mapping into an ordered
// []ChoiceField. Author iteration order matters for the mad-lib
// template, so we use the MapSlice key sequence.
func liftChoiceFields(v any) ([]ChoiceField, error) {
	ms, ok := v.(goyaml.MapSlice)
	if !ok {
		if m, mok := v.(map[string]any); mok {
			ms = mapToMapSlice(m)
		} else {
			return nil, fmt.Errorf("expected a mapping, got %T", v)
		}
	}
	out := make([]ChoiceField, 0, len(ms))
	for _, kv := range ms {
		name, _ := kv.Key.(string)
		f := ChoiceField{Name: name}
		inner, ok := kv.Value.(goyaml.MapSlice)
		if !ok {
			if m, mok := kv.Value.(map[string]any); mok {
				inner = mapToMapSlice(m)
			} else {
				return nil, fmt.Errorf("field %q: expected a mapping, got %T", name, kv.Value)
			}
		}
		for _, p := range inner {
			fk, _ := p.Key.(string)
			switch fk {
			case "type":
				if s, ok := p.Value.(string); ok {
					f.Type = s
				}
			case "hint":
				if s, ok := p.Value.(string); ok {
					f.Hint = s
				}
			case "placeholder":
				if s, ok := p.Value.(string); ok {
					f.Placeholder = s
				}
			case "unit":
				if s, ok := p.Value.(string); ok {
					f.Unit = s
				}
			case "values":
				vals, err := stringSlice(p.Value)
				if err != nil {
					return nil, fmt.Errorf("field %q values: %w", name, err)
				}
				f.Values = vals
			case "default":
				f.Default = p.Value
			case "min":
				f.Min = p.Value
			case "max":
				f.Max = p.Value
			case "required":
				if b, ok := p.Value.(bool); ok {
					f.Required = b
				}
			case "readonly":
				if b, ok := p.Value.(bool); ok {
					f.Readonly = b
				}
			case "expr":
				if s, ok := p.Value.(string); ok {
					f.Expr = s
				}
			case "when":
				if s, ok := p.Value.(string); ok {
					f.When = s
				}
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// ---- low-level helpers ------------------------------------------------------

// stringSlice converts a YAML-decoded value into []string. Used for
// param.values / field.values lifting.
func stringSlice(v any) ([]string, error) {
	switch s := v.(type) {
	case []any:
		out := make([]string, 0, len(s))
		for i, raw := range s {
			str, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("values[%d]: expected string, got %T", i, raw)
			}
			out = append(out, str)
		}
		return out, nil
	case []string:
		return append([]string(nil), s...), nil
	default:
		return nil, fmt.Errorf("expected a sequence of strings, got %T", v)
	}
}

// toInt accepts any numeric YAML scalar and reports whether it fits an
// int. We accept the common decoder shapes (uint64, int64, int, float64
// when no fractional part).
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case uint64:
		return int(n), true
	case float64:
		if float64(int(n)) == n {
			return int(n), true
		}
	}
	return 0, false
}

// mapToMapSlice converts a map[string]any to a MapSlice with stable
// (sorted) key order. Used as a fallback when goyaml hands us a plain
// map (e.g. nested under a slice index).
//
// Sorted order is a deliberate choice for the fallback path so test
// fixtures using map literals stay deterministic — production YAML
// authoring goes through the MapSlice branch which preserves source
// order.
func mapToMapSlice(m map[string]any) goyaml.MapSlice {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(goyaml.MapSlice, 0, len(keys))
	for _, k := range keys {
		out = append(out, goyaml.MapItem{Key: k, Value: m[k]})
	}
	return out
}

// mapSliceToStringMap turns a MapSlice (or a plain map[string]any) into
// a map[string]any keyed by string. Slot values may be any scalar; the
// caller (loader cross-ref walk) does its own per-key type-checking.
func mapSliceToStringMap(v any) (map[string]any, error) {
	switch s := v.(type) {
	case goyaml.MapSlice:
		out := make(map[string]any, len(s))
		for _, p := range s {
			key, ok := p.Key.(string)
			if !ok {
				return nil, fmt.Errorf("non-string key %v", p.Key)
			}
			out[key] = p.Value
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(s))
		for k, val := range s {
			out[k] = val
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a mapping, got %T", v)
	}
}

// mapSliceToJSON converts a goyaml.MapSlice subtree to JSON bytes.
// Order is preserved so the schema validator's diagnostic locations
// stay aligned with author source. Nested MapSlice values are
// recursively converted to ordered JSON objects.
func mapSliceToJSON(ms goyaml.MapSlice) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeJSONValue(&buf, ms); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeJSONValue emits a single JSON value. It special-cases MapSlice
// to preserve key order; all other types defer to encoding/json.
func writeJSONValue(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case goyaml.MapSlice:
		buf.WriteByte('{')
		for i, p := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			key, ok := p.Key.(string)
			if !ok {
				return fmt.Errorf("non-string map key %v", p.Key)
			}
			k, err := json.Marshal(key)
			if err != nil {
				return err
			}
			buf.Write(k)
			buf.WriteByte(':')
			if err := writeJSONValue(buf, p.Value); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeJSONValue(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case map[string]any:
		// Fallback path — sort keys for determinism.
		return writeJSONValue(buf, mapToMapSlice(t))
	default:
		// Strings, numbers, bools, nil — encoding/json handles them.
		raw, err := json.Marshal(t)
		if err != nil {
			return err
		}
		buf.Write(raw)
		return nil
	}
}

// ---- validate() implementation ---------------------------------------------

// validateChoice runs the layered choice-element validation:
//  1. JSON Schema (structural shape + per-mode required fields).
//  2. expr-lang compile-pass on every when/expr field.
//  3. pongo2 compile-pass on every templated leaf (anything containing "{{").
//
// Returns the first error found; loader callers wrap with the
// surrounding state/transition/view path.
func validateChoice(e ViewElement) error {
	schema, err := compileChoiceSchema()
	if err != nil {
		return err
	}
	if len(e.ChoiceRaw) == 0 {
		return fmt.Errorf("choice: missing raw subtree (internal error — element was constructed without a YAML source)")
	}
	var instance any
	if err := json.Unmarshal(e.ChoiceRaw, &instance); err != nil {
		return fmt.Errorf("choice: parse raw subtree: %w", err)
	}
	if err := schema.Validate(instance); err != nil {
		return fmt.Errorf("choice: %s", formatChoiceSchemaError(err))
	}

	// Element-level when guard.
	if err := compileWhen(e.When, "when"); err != nil {
		return err
	}

	switch e.ChoiceMode {
	case "single":
		for i, it := range e.ChoiceItems {
			if err := compileWhen(it.When, fmt.Sprintf("items[%d].when", i)); err != nil {
				return err
			}
			// Templated leaves on slot values.
			for k, v := range it.Slots {
				if s, ok := v.(string); ok && containsTemplate(s) {
					if err := render.PongoParse(s); err != nil {
						return fmt.Errorf("items[%d].slots.%s: pongo template syntax error: %w", i, k, err)
					}
				}
			}
			if it.Param != nil && containsTemplate(it.Param.Placeholder) {
				if err := render.PongoParse(it.Param.Placeholder); err != nil {
					return fmt.Errorf("items[%d].param.placeholder: pongo template syntax error: %w", i, err)
				}
			}
		}
	case "multi":
		for i, it := range e.ChoiceItems {
			if err := compileWhen(it.When, fmt.Sprintf("items[%d].when", i)); err != nil {
				return err
			}
		}
	case "form":
		// Template body — pongo compile.
		if containsTemplate(e.ChoiceTemplate) {
			if err := render.PongoParse(e.ChoiceTemplate); err != nil {
				return fmt.Errorf("template: pongo template syntax error: %w", err)
			}
		}
		for _, f := range e.ChoiceFields {
			if err := compileWhen(f.When, fmt.Sprintf("fields.%s.when", f.Name)); err != nil {
				return err
			}
			if f.Readonly && strings.TrimSpace(f.Expr) != "" {
				if _, err := expr.Compile(f.Expr); err != nil {
					return fmt.Errorf("fields.%s.expr: %w", f.Name, err)
				}
			}
			// Templated min/max/default/placeholder leaves.
			if s, ok := f.Min.(string); ok && containsTemplate(s) {
				if err := render.PongoParse(s); err != nil {
					return fmt.Errorf("fields.%s.min: pongo template syntax error: %w", f.Name, err)
				}
			}
			if s, ok := f.Max.(string); ok && containsTemplate(s) {
				if err := render.PongoParse(s); err != nil {
					return fmt.Errorf("fields.%s.max: pongo template syntax error: %w", f.Name, err)
				}
			}
			if s, ok := f.Default.(string); ok && containsTemplate(s) {
				if err := render.PongoParse(s); err != nil {
					return fmt.Errorf("fields.%s.default: pongo template syntax error: %w", f.Name, err)
				}
			}
			if containsTemplate(f.Placeholder) {
				if err := render.PongoParse(f.Placeholder); err != nil {
					return fmt.Errorf("fields.%s.placeholder: pongo template syntax error: %w", f.Name, err)
				}
			}
		}
	}
	return nil
}

// compileWhen runs a when: source through expr.CompileBool when
// non-empty. The error is wrapped with the supplied location label
// for author-locatable diagnostics.
func compileWhen(src, label string) error {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	if _, err := expr.CompileBool(src); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// Load-time pongo template discrimination: see render.PongoParse.
//
// Earlier revisions of this file shipped an `isPongoSyntaxError`
// heuristic that inspected the lowercased error message for tokens
// like "expected" / "unterminated" / "unbalanced" to decide whether a
// failure from render.Pongo was a parse error (load-time fatal) or a
// runtime undefined-identifier / type-mismatch (which we must NOT
// reject at load — choice templates legitimately reference world.* /
// form.* / slots.* which are empty in the load-time probe env).
//
// That approach was fragile: a minor pongo2 version bump could rephrase
// any of those tokens and silently demote real syntax errors to no-ops.
// The current design routes load-time syntax checks through
// render.PongoParse, which calls `pongo2.FromString` directly and
// returns ONLY parse errors (Execute is never invoked, so undefined-
// identifier failures cannot reach us). Discrimination is now
// "did the parse step error?" — structural, not textual.

// formatChoiceSchemaError flattens a jsonschema validation error into
// the format the loader's other validators use ("first leaf reason").
// Mirrors internal/mcp/validator.go's collectBasicOutputLeaves but
// kept private to this package so the choice path doesn't depend on
// the MCP server's internal surface.
func formatChoiceSchemaError(err error) string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err.Error()
	}
	out := ve.DetailedOutput()
	var leaves []string
	walkChoiceSchemaLeaves(out, &leaves)
	if len(leaves) == 0 {
		return ve.Error()
	}
	if len(leaves) == 1 {
		return leaves[0]
	}
	return "schema validation failed:\n  - " + strings.Join(leaves, "\n  - ")
}

// ---- Cross-reference validation (loader-side) ------------------------------

// choicePlaceholderRE matches {name} placeholders inside a form-mode
// template. Only identifier-shaped names are considered placeholders;
// arbitrary curly-brace content (e.g. `{$amount}`) is left alone so
// authors can still embed literal braces in their prose.
var choicePlaceholderRE = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// validateChoiceCrossRefs walks a Kind=="choice" ViewElement and
// resolves every intent / slot reference against the surrounding
// intents map (see docs/stories/choice-widget.md).
//
// Resolution precedence: state-local intents shadow globals so a state
// can specialise an intent's slot set without breaking sibling rooms.
// Callers pass both maps; an empty stateIntents is fine (resolution
// falls through to globalIntents).
func validateChoiceCrossRefs(e ViewElement, globalIntents map[string]Intent, stateIntents map[string]Intent) error {
	resolve := func(name string) (Intent, bool) {
		if name == "" {
			return Intent{}, false
		}
		if in, ok := stateIntents[name]; ok {
			return in, true
		}
		if in, ok := globalIntents[name]; ok {
			return in, true
		}
		return Intent{}, false
	}

	switch e.ChoiceMode {
	case "single":
		for i, it := range e.ChoiceItems {
			in, ok := resolve(it.Intent)
			if !ok {
				return fmt.Errorf("items[%d]: intent %q not declared in state or global intents", i, it.Intent)
			}
			// Item slot keys must be declared slots of the intent.
			// Literal-membership checks for enum slots are skipped when
			// the value is templated (`{{ ... }}`) since the type is
			// only knowable at submit time.
			for k, v := range it.Slots {
				slot, slotOK := in.Slots[k]
				if !slotOK {
					return fmt.Errorf("items[%d].slots: key %q is not a declared slot of intent %q", i, k, it.Intent)
				}
				if slot.Type == "enum" && len(slot.Values) > 0 {
					if s, isStr := v.(string); isStr && !containsTemplate(s) {
						matched := false
						for _, allowed := range slot.Values {
							if allowed == s {
								matched = true
								break
							}
						}
						if !matched {
							return fmt.Errorf("items[%d].slots.%s: value %q is not in the slot's enum values", i, k, s)
						}
					}
				}
			}
			if it.Param != nil {
				if _, ok := in.Slots[it.Param.Slot]; !ok {
					return fmt.Errorf("items[%d].param.slot: %q is not a declared slot of intent %q", i, it.Param.Slot, it.Intent)
				}
				if _, dup := it.Slots[it.Param.Slot]; dup {
					return fmt.Errorf("items[%d].param.slot: %q is already pre-bound in items[%d].slots — choose one or the other", i, it.Param.Slot, i)
				}
			}
		}
	case "multi":
		in, ok := resolve(e.ChoiceIntent)
		if !ok {
			return fmt.Errorf("intent %q not declared in state or global intents", e.ChoiceIntent)
		}
		if _, ok := in.Slots[e.ChoiceSlot]; !ok {
			return fmt.Errorf("slot %q is not a declared slot of intent %q", e.ChoiceSlot, e.ChoiceIntent)
		}
		// Every items[].value must be a literal — a templated value
		// defeats downstream type checks.
		for i, it := range e.ChoiceItems {
			if containsTemplate(it.Value) {
				return fmt.Errorf("items[%d].value: must be a string literal (templated values are not allowed in multi mode)", i)
			}
		}
	case "form":
		in, ok := resolve(e.ChoiceIntent)
		if !ok {
			return fmt.Errorf("intent %q not declared in state or global intents", e.ChoiceIntent)
		}
		// Every non-readonly field must be a declared slot of the intent.
		for _, f := range e.ChoiceFields {
			if f.Readonly {
				continue
			}
			if _, ok := in.Slots[f.Name]; !ok {
				return fmt.Errorf("fields.%s: not a declared slot of intent %q", f.Name, e.ChoiceIntent)
			}
		}
		// Every {name} placeholder in the template body must have a
		// matching fields: entry.
		fieldSet := make(map[string]struct{}, len(e.ChoiceFields))
		for _, f := range e.ChoiceFields {
			fieldSet[f.Name] = struct{}{}
		}
		for _, m := range choicePlaceholderRE.FindAllStringSubmatch(e.ChoiceTemplate, -1) {
			name := m[1]
			if _, ok := fieldSet[name]; !ok {
				return fmt.Errorf("template references {%s} but no fields.%s is declared", name, name)
			}
		}
	}
	return nil
}

func walkChoiceSchemaLeaves(unit *jsonschema.OutputUnit, out *[]string) {
	if unit == nil || unit.Valid {
		return
	}
	// Always descend into sub-errors first; the leaves carry the
	// concrete missing-property / enum-mismatch text. Generic
	// containers like "allOf failed" / "validation failed" at the
	// outer level would shadow those details otherwise.
	if len(unit.Errors) > 0 {
		for i := range unit.Errors {
			walkChoiceSchemaLeaves(&unit.Errors[i], out)
		}
		return
	}
	if unit.Error != nil {
		loc := unit.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		*out = append(*out, loc+": "+unit.Error.String())
	}
}
