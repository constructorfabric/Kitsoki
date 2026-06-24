// format_jql.go — semantic validation for JQL expressions.
//
// Plugs into the MCP validator's compiler via RegisterFormat so any slot
// schema marked `"format": "jql"` gets caught at submit time. The router
// trace from devstory showed Haiku 4.5 emitting natural-language slot
// values like "open presentation service bugs", which Jira rejects with a
// 400; this validator rejects those at the MCP boundary so the LLM sees
// the error and self-corrects within the same `claude -p` conversation.
package mcp

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// bareIssueKeyRE matches Jira issue keys like PLTFRM-123 (after trim).
	bareIssueKeyRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]+-\d+$`)

	// jqlOperatorRE matches the comparison operators that are unambiguous
	// JQL signals: =, !=, <, >, <=, >=, ~, !~. A bare `!` does not count.
	jqlOperatorRE = regexp.MustCompile(`[!<>=~]=?|~`)

	// jqlKeywordRE matches whole-word JQL keywords used in operator
	// position. The (?i) flag makes the match case-insensitive; the \b
	// boundaries prevent substring false-positives (so "isbn" doesn't
	// match `is`, "winning" doesn't match `in`).
	jqlKeywordRE = regexp.MustCompile(`(?i)\b(in|is|was|changed|not\s+in)\b`)
)

// validateJQL is the format hook registered with the schema compiler.
// Returns nil on accept, an error message on reject.
func validateJQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("must be a string")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("must not be empty")
	}
	if bareIssueKeyRE.MatchString(s) {
		return nil
	}
	if jqlOperatorRE.MatchString(s) {
		return nil
	}
	if jqlKeywordRE.MatchString(s) {
		return nil
	}
	return fmt.Errorf("looks like natural language, not JQL — compile to a JQL expression with operators (=, ~, in, is) or use a single issue key like PLTFRM-123 (got %q)", s)
}
