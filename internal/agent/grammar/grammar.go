// Package grammar decides whether a JSON-Schema is inside the subset that
// llama.cpp's json-schema-to-grammar translator handles soundly, so kitsoki can
// safely ask the local model to grammar-constrain its decode against it.
//
// Why a subset gate at all: llama.cpp's grammar translation FAILS OPEN — when
// it cannot translate a construct it logs the error, decodes unconstrained, and
// still returns HTTP 200. A schema we *believe* is being enforced but silently
// is not would erode the predictability the local-model tier exists to buy.
// SubsetOK is the honest gate: it returns nil only for schemas whose every
// construct llama.cpp translates faithfully, and a descriptive error (naming the
// offending construct and JSON path) otherwise.
//
// This logic lives in its own leaf package (no kitsoki deps) so both the agent
// transport (Ask-time, per call) and the app loader (load-time, to reject
// grammar:true effects pointed at out-of-subset schemas) can call it without an
// import cycle: agent already imports app, so app cannot import agent. The
// agent package re-exports SubsetOK as agent.GrammarSubsetOK to keep its
// in-package call site and tests idiomatic.
//
// Non-goals: this is NOT a JSON-Schema validator and makes no claim about
// whether instances satisfy the schema — it only inspects which schema
// *constructs* are present. It deliberately errs strict: a construct we are not
// certain llama.cpp handles is rejected so grammar stays sound-by-construction.
package grammar

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SubsetOK reports whether schema is inside llama.cpp's reliably translatable
// grammar subset. It returns nil when the whole schema (recursing into
// properties, items, and array branches) uses only constructs llama.cpp
// translates faithfully, or a non-nil error naming the first offending construct
// and its JSON path otherwise.
//
// Rejected because llama.cpp's json-schema-to-grammar omits or mistranslates
// them: $ref, $defs, uniqueItems, not, if/then/else, dependentSchemas, contains,
// and anyOf/oneOf appearing alongside sibling properties or type. Also rejected:
// a pattern that is not fully anchored (^...$), since an unanchored regex
// translates to a grammar that matches a superset of the intended strings.
//
// Accepted: flat and nested objects with properties, scalar leaves (string,
// number, integer, boolean, null), enums, and simply-typed arrays (items as a
// single schema). required imposes no grammar-subset constraint and is left to
// ValidateSubmission. additionalProperties and patternProperties, when given as
// *schemas* (not the bare booleans), are recursed into — llama.cpp translates
// them, so an out-of-subset construct nested there would otherwise slip past.
func SubsetOK(schema json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	var node map[string]json.RawMessage
	if err := json.Unmarshal(schema, &node); err != nil {
		// A schema that is not even a JSON object (e.g. `true`/`false` boolean
		// schemas) carries no constructs we can vouch for — reject to stay sound.
		return fmt.Errorf("schema is not a JSON object")
	}
	return subsetOK(node, "$")
}

// rejectedKeys are schema keywords that, if present at any level, place the
// schema outside the translatable subset. Each is rejected because llama.cpp's
// translator either drops it or produces a grammar that does not enforce it.
var rejectedKeys = []string{
	"$ref",
	"$defs",
	"definitions",
	"uniqueItems",
	"not",
	"if",
	"then",
	"else",
	"dependentSchemas",
	"contains",
}

// subsetOK is the recursive worker. path is a human-readable JSON path
// (e.g. "$.properties.confidence") used only for error messages.
func subsetOK(node map[string]json.RawMessage, path string) error {
	for _, k := range rejectedKeys {
		if _, ok := node[k]; ok {
			return fmt.Errorf("%s: unsupported construct %q is outside the llama.cpp grammar subset", path, k)
		}
	}

	// anyOf/oneOf are translatable only as a standalone choice node. Alongside a
	// sibling `properties` or `type` they form an intersection llama.cpp does not
	// model, so it silently keeps one side — reject to stay sound.
	_, hasProps := node["properties"]
	_, hasType := node["type"]
	for _, comb := range []string{"anyOf", "oneOf"} {
		raw, ok := node[comb]
		if !ok {
			continue
		}
		if hasProps || hasType {
			return fmt.Errorf("%s: %q alongside sibling properties/type is outside the llama.cpp grammar subset", path, comb)
		}
		var branches []json.RawMessage
		if err := json.Unmarshal(raw, &branches); err != nil {
			return fmt.Errorf("%s.%s: not a list of schemas", path, comb)
		}
		for i, b := range branches {
			var sub map[string]json.RawMessage
			if err := json.Unmarshal(b, &sub); err != nil {
				return fmt.Errorf("%s.%s[%d]: not a JSON object schema", path, comb, i)
			}
			if err := subsetOK(sub, fmt.Sprintf("%s.%s[%d]", path, comb, i)); err != nil {
				return err
			}
		}
	}

	// pattern must be fully anchored, else its grammar matches a superset.
	if raw, ok := node["pattern"]; ok {
		var pat string
		if err := json.Unmarshal(raw, &pat); err != nil {
			return fmt.Errorf("%s.pattern: not a string", path)
		}
		if !strings.HasPrefix(pat, "^") || !strings.HasSuffix(pat, "$") {
			return fmt.Errorf("%s.pattern: %q is not anchored (^...$); unanchored patterns translate to an overly permissive grammar", path, pat)
		}
	}

	// Recurse into object properties.
	if raw, ok := node["properties"]; ok {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(raw, &props); err != nil {
			return fmt.Errorf("%s.properties: not a JSON object", path)
		}
		for name, p := range props {
			var sub map[string]json.RawMessage
			if err := json.Unmarshal(p, &sub); err != nil {
				return fmt.Errorf("%s.properties.%s: not a JSON object schema", path, name)
			}
			if err := subsetOK(sub, fmt.Sprintf("%s.properties.%s", path, name)); err != nil {
				return err
			}
		}
	}

	// Recurse into array items. Only a single-schema `items` (homogeneous array)
	// is in subset; tuple form (`items` as a list) plus its prefixItems sibling
	// is not modelled, so reject a list-form items.
	if raw, ok := node["items"]; ok {
		var sub map[string]json.RawMessage
		if err := json.Unmarshal(raw, &sub); err != nil {
			return fmt.Errorf("%s.items: tuple-form or non-object items is outside the llama.cpp grammar subset", path)
		}
		if err := subsetOK(sub, path+".items"); err != nil {
			return err
		}
	}

	// Recurse into additionalProperties / patternProperties when they are given
	// as schemas. llama.cpp emits grammar for these, so a rejected construct
	// hidden inside (e.g. additionalProperties:{$ref:…}) must be caught here.
	// The bare-boolean forms (additionalProperties:true|false) carry no nested
	// construct and are skipped — json.Unmarshal into a map fails for them.
	if raw, ok := node["additionalProperties"]; ok {
		var sub map[string]json.RawMessage
		if err := json.Unmarshal(raw, &sub); err == nil {
			if err := subsetOK(sub, path+".additionalProperties"); err != nil {
				return err
			}
		}
	}
	if raw, ok := node["patternProperties"]; ok {
		var pats map[string]json.RawMessage
		if err := json.Unmarshal(raw, &pats); err != nil {
			return fmt.Errorf("%s.patternProperties: not a JSON object", path)
		}
		for name, p := range pats {
			var sub map[string]json.RawMessage
			if err := json.Unmarshal(p, &sub); err != nil {
				return fmt.Errorf("%s.patternProperties.%s: not a JSON object schema", path, name)
			}
			if err := subsetOK(sub, fmt.Sprintf("%s.patternProperties.%s", path, name)); err != nil {
				return err
			}
		}
	}

	return nil
}
