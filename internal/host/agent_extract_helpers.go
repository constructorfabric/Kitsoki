// Package host — I/O and validation helpers for agent_extract.go.
//
// Separated so agent_extract.go stays focused on the resolver logic and these
// platform-specific helpers can be stubbed in tests.
package host

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"

	goyaml "github.com/goccy/go-yaml"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// jsonUnmarshal is a thin wrapper for JSON unmarshaling that is replaceable in
// tests if needed.
var jsonUnmarshal = json.Unmarshal

// osReadFileForExtract reads a file from disk. The indirection lets tests
// swap this function via an internal test hook if needed.
var osReadFileForExtract = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// unmarshalYAMLForExtract parses YAML bytes into v. The indirection allows
// test substitution.
var unmarshalYAMLForExtract = func(data []byte, v any) error {
	return goyaml.NewDecoder(bytes.NewReader(data)).Decode(v)
}

// stringSliceArg safely extracts a []string from a map[string]any value (L9).
// Handles []string, []any (JSON-decoded), and a single string. Returns nil
// when the key is absent or the value is not a recognised string-list type.
func stringSliceArg(args map[string]any, key string) []string {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok2 := item.(string); ok2 {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}

// jsonSchemaValidate validates payloadJSON against the JSON Schema in schemaRaw.
// Returns true when valid, false when not, and true (permissive) when the
// schema cannot be compiled (authoring errors are caught at load time; at
// runtime we prefer letting a mis-authored schema through rather than killing a
// live session).
func jsonSchemaValidate(ctx context.Context, schemaRaw []byte, payloadJSON []byte) bool {
	// Parse the schema document into a Go value for AddResource (v6 API).
	var schemaDoc any
	if err := goyaml.Unmarshal(schemaRaw, &schemaDoc); err != nil {
		// Try JSON unmarshal as fallback (schemas may be .json files).
		var jdoc any
		if jsonErr := jsonUnmarshal(schemaRaw, &jdoc); jsonErr != nil {
			slog.WarnContext(ctx, "extract.schema_validate",
				slog.String("err", "parse schema: "+err.Error()))
			return true
		}
		schemaDoc = jdoc
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaDoc); err != nil {
		slog.WarnContext(ctx, "extract.schema_validate",
			slog.String("err", "add resource: "+err.Error()))
		return true
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		slog.WarnContext(ctx, "extract.schema_validate",
			slog.String("err", "compile schema: "+err.Error()))
		return true
	}

	// Parse payload for validation.
	var payloadDoc any
	if err := jsonUnmarshal(payloadJSON, &payloadDoc); err != nil {
		slog.WarnContext(ctx, "extract.schema_validate",
			slog.String("err", "parse payload: "+err.Error()))
		return true
	}

	if err := schema.Validate(payloadDoc); err != nil {
		return false
	}
	return true
}
