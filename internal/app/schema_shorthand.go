// Package app — schema shorthand → JSON Schema converter.
//
// Proposal kinds declare drafts as `field: type` shorthand (see
// stories/oregon-trail/proposals.yaml). Internally the same shorthand can be
// rendered as a draft 2020-12 JSON Schema so the MCP validator pipeline can
// reject malformed drafts structurally. See internal/mcp/validator.go and
// internal/host/agent_ask_with_mcp.go for the live LLM-validated-draft path.
package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ShorthandToJSONSchema converts a {field: type-string} shorthand into a
// draft 2020-12 JSON Schema document (raw bytes). All declared fields are
// required by default; additionalProperties is false.
//
// Supported type strings (case-insensitive):
//
//	"string"
//	"int"     "integer"
//	"number"  "float"
//	"bool"    "boolean"
//	"list"    "array"
//	"map"     "object"
//
// Unknown type strings return an error naming the bad field.
func ShorthandToJSONSchema(shorthand map[string]string) ([]byte, error) {
	// Sort field names so byte output is deterministic.
	fields := make([]string, 0, len(shorthand))
	for k := range shorthand {
		fields = append(fields, k)
	}
	sort.Strings(fields)

	var buf bytes.Buffer
	buf.WriteString(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false`)

	if len(fields) == 0 {
		buf.WriteString("}")
		return buf.Bytes(), nil
	}

	buf.WriteString(`,"properties":{`)
	for i, field := range fields {
		typeStr, err := jsonSchemaTypeFor(shorthand[field])
		if err != nil {
			return nil, fmt.Errorf("app.ShorthandToJSONSchema: field %q: %w", field, err)
		}
		if i > 0 {
			buf.WriteByte(',')
		}
		nameJSON, _ := json.Marshal(field)
		buf.Write(nameJSON)
		buf.WriteString(`:{"type":`)
		buf.WriteString(typeStr)
		buf.WriteString(`}`)
	}
	buf.WriteString(`},"required":[`)
	for i, field := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		nameJSON, _ := json.Marshal(field)
		buf.Write(nameJSON)
	}
	buf.WriteString(`]}`)
	return buf.Bytes(), nil
}

// jsonSchemaTypeFor maps a shorthand type token to its quoted JSON Schema
// type literal. Returns an error for unknown tokens.
func jsonSchemaTypeFor(token string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "string":
		return `"string"`, nil
	case "int", "integer":
		return `"integer"`, nil
	case "number", "float":
		return `"number"`, nil
	case "bool", "boolean":
		return `"boolean"`, nil
	case "list", "array":
		return `"array"`, nil
	case "map", "object":
		return `"object"`, nil
	default:
		return "", fmt.Errorf("unknown shorthand type %q", token)
	}
}
