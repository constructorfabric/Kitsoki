// validate.go holds the schema-validation helpers.
//
// ValidateSubmission is the single entry point for kitsoki-side JSON-Schema
// validation of agent responses. The validation authority lives here, not in
// plugins. Plugins are dumb pipes.
//
// Library choice: github.com/santhosh-tekuri/jsonschema/v6 is already a direct
// dependency in go.mod. It is pure-Go, CGO-free, supports JSON Schema draft
// 2020-12, and accepts json.RawMessage cleanly via UnmarshalJSON +
// AddResource, so no new dependency is needed.
//
// $ref resolution: the compiler here is rooted with no filesystem base URL, so
// a $ref to a sibling file fails at compile time. Story-directory-rooted
// resolution is enforced separately at story-load time by ValidateSchemaRefs.

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
// $ref note: this compiler is rooted with no filesystem base URL, so a $ref to
// a sibling file fails at compile time. The signature is kept narrow so a
// story-directory-rooted variant can be added later without changing existing
// call sites.
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
	// PoC scale (single call per agent turn) this is acceptable. A caching
	// layer can be added later if profiling shows it is hot (see the
	// schema-cache Non-goal in doc.go).
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
// The compiler uses no filesystem loader, so a $ref to a sibling file fails
// at compile time.
func compileSchema(schema json.RawMessage) (*jsonschema.Schema, error) {
	var schemaVal any
	if err := json.Unmarshal(schema, &schemaVal); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}

	c := jsonschema.NewCompiler()
	const schemaURI = "agent://schema"
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

// ValidateSchemaRefs checks that all $ref values in schema are safe relative
// paths within storyDir. This is the story-load-time enforcement of the rule
// that out-of-tree references fail at story-load time, not at Ask time —
// keeping a story's schema graph self-contained and auditable.
//
// Rules:
//   - $ref with an absolute path → rejected.
//   - $ref with ".." that resolves outside storyDir → rejected.
//   - $ref with a relative path that does not exist within storyDir → rejected.
//
// Returns nil when schema has no $ref fields, or when all $refs are safe and
// exist within storyDir. storyDir must be an absolute path.
func ValidateSchemaRefs(schema json.RawMessage, storyDir string) error {
	if len(schema) == 0 || storyDir == "" {
		return nil
	}
	var schemaVal any
	if err := json.Unmarshal(schema, &schemaVal); err != nil {
		return nil // malformed schema is caught by ValidateSubmission
	}
	refs := collectRefs(schemaVal)
	canonBase, err := filepath.Abs(storyDir)
	if err != nil {
		canonBase = filepath.Clean(storyDir)
	}
	for _, ref := range refs {
		if filepath.IsAbs(ref) {
			return fmt.Errorf("schema $ref %q must be relative to the story directory (absolute paths are not allowed)", ref)
		}
		// Allow fragment-only or URI $refs (e.g. "#/$defs/foo", "https://...").
		// Only validate filesystem $refs: those that don't start with "#" or a
		// URI scheme and contain a file path component.
		if strings.HasPrefix(ref, "#") || strings.Contains(ref, "://") {
			continue
		}
		// Resolve relative to storyDir.
		resolved := filepath.Join(canonBase, ref)
		// Check for out-of-tree traversal.
		rel, relErr := filepath.Rel(canonBase, filepath.Clean(resolved))
		if relErr != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("schema $ref %q resolves outside the story directory", ref)
		}
		// Check that the file exists.
		if _, statErr := os.Stat(resolved); statErr != nil {
			return fmt.Errorf("schema $ref %q: referenced file does not exist: %v", ref, statErr)
		}
	}
	return nil
}

// collectRefs walks a JSON value tree and collects all "$ref" string values.
func collectRefs(v any) []string {
	var refs []string
	switch tv := v.(type) {
	case map[string]any:
		for k, child := range tv {
			if k == "$ref" {
				if s, ok := child.(string); ok {
					refs = append(refs, s)
				}
			} else {
				refs = append(refs, collectRefs(child)...)
			}
		}
	case []any:
		for _, item := range tv {
			refs = append(refs, collectRefs(item)...)
		}
	}
	return refs
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
