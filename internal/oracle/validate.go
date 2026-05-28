// Package oracle — schema validation helper.
//
// ValidateSubmission is the single entry point for kitsoki-side JSON-Schema
// validation of oracle responses. The validation authority lives here, not in
// plugins. Plugins are dumb pipes.
//
// Library choice: github.com/santhosh-tekuri/jsonschema/v6 is already a direct
// dependency in go.mod (pulled in by an existing package). It is pure-Go,
// CGO-free, supports JSON Schema draft 2020-12, and accepts json.RawMessage
// cleanly via UnmarshalJSON + AddResource. No new dependency is needed.
//
// $ref resolution: in B-1 the compiler is rooted with no base URL, so $ref to
// sibling files will fail at compile time with a loader error. B-2 wiring
// passes the story directory as the schema base URL so relative $refs resolve
// against the story's schemas/ directory.
package oracle

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// englishPrinter is reused for all validation error message localisation.
var englishPrinter = message.NewPrinter(language.English)

// ValidateSubmission checks submission against schema. Returns nil when:
//   - schema is nil (intentional skip — caller explicitly opted out).
//   - submission is valid JSON and satisfies the schema.
//
// Returns *AskError{Kind: "schema_invalid"} when:
//   - submission is not valid JSON.
//   - submission is valid JSON but fails the schema constraints.
//
// The Detail field of the returned AskError includes the JSON Pointer path
// and the rule that failed, drawn from the ValidationError tree.
//
// B-2 note: $ref resolution is currently rooted with no base URL. When B-2
// wires the story directory, it should construct a Compiler with a URL loader
// rooted at the story's schemas/ directory and pass the base URL as a
// parameter to a lower-level helper. The ValidateSubmission signature is
// intentionally kept narrow here so B-2 can add a variant without changing
// existing call sites.
func ValidateSubmission(schema, submission json.RawMessage) error {
	if schema == nil {
		return nil // intentional skip
	}

	// Parse the submission first so we can give a clear malformed-JSON error
	// before reaching the schema compiler.
	var inst any
	if err := json.Unmarshal(submission, &inst); err != nil {
		return &AskError{
			Kind:       "schema_invalid",
			Underlying: err,
			Detail:     fmt.Sprintf("submission is not valid JSON: %v", err),
		}
	}

	// Compile the schema. Each call to ValidateSubmission compiles fresh; at
	// PoC scale (single call per oracle turn) this is acceptable. A caching
	// layer can be added in B-2 if profiling shows it is hot.
	compiled, err := compileSchema(schema)
	if err != nil {
		return &AskError{
			Kind:       "schema_invalid",
			Underlying: err,
			Detail:     fmt.Sprintf("schema compilation failed: %v", err),
		}
	}

	if verr := compiled.Validate(inst); verr != nil {
		return &AskError{
			Kind:       "schema_invalid",
			Underlying: verr,
			Detail:     formatValidationError(verr),
		}
	}

	return nil
}

// compileSchema parses and compiles schema bytes using jsonschema/v6.
// The compiler uses no filesystem loader so $ref to sibling files will fail
// at compile time in B-1. B-2 wiring adds a URL-rooted loader.
func compileSchema(schema json.RawMessage) (*jsonschema.Schema, error) {
	var schemaVal any
	if err := json.Unmarshal(schema, &schemaVal); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}

	c := jsonschema.NewCompiler()
	const schemaURI = "oracle://schema"
	if err := c.AddResource(schemaURI, schemaVal); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}

	compiled, err := c.Compile(schemaURI)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return compiled, nil
}

// formatValidationError converts a jsonschema ValidationError tree into a
// human-readable Detail string suitable for AskError.Detail.
// It walks the Causes tree to extract the leaf errors, which contain the
// specific field paths and constraint violations.
func formatValidationError(err error) string {
	verr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return err.Error()
	}

	// Collect leaf messages from the error tree.
	var msgs []string
	collectLeaves(verr, &msgs)
	if len(msgs) == 0 {
		return verr.Error()
	}
	return strings.Join(msgs, "; ")
}

// collectLeaves walks the ValidationError tree and appends leaf error messages
// (those with no further Causes) to msgs. Intermediate nodes often just say
// "validation failed" which is less informative than the leaf constraint.
func collectLeaves(verr *jsonschema.ValidationError, msgs *[]string) {
	if len(verr.Causes) == 0 {
		// Leaf node: compose path + message.
		path := jsonPtr(verr.InstanceLocation)
		msg := verr.ErrorKind.LocalizedString(englishPrinter)
		if path == "" || path == "/" {
			*msgs = append(*msgs, msg)
		} else {
			*msgs = append(*msgs, path+": "+msg)
		}
		return
	}
	for _, cause := range verr.Causes {
		collectLeaves(cause, msgs)
	}
}

// jsonPtr converts a slice of JSON Pointer tokens into a JSON Pointer string.
func jsonPtr(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, tok := range tokens {
		sb.WriteByte('/')
		// Escape per RFC 6901: ~ → ~0, / → ~1.
		for i := 0; i < len(tok); i++ {
			switch tok[i] {
			case '~':
				sb.WriteString("~0")
			case '/':
				sb.WriteString("~1")
			default:
				sb.WriteByte(tok[i])
			}
		}
	}
	return sb.String()
}
