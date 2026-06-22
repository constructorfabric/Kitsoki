package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
)

// A malformed YAML document must surface a *ValidationError that reports the
// 1-based line where the syntax error occurs — goccy/go-yaml already carries the
// token position; the loader must thread it into ValidationError.Line/Column
// instead of discarding it. Without a line number a malformed app.yaml is a
// guessing game for the author.
func TestLoadBytes_SyntaxErrorReportsLine(t *testing.T) {
	// Line 6 has a tab-indented mapping value, which goccy rejects as a syntax
	// error (tabs are not allowed for indentation in YAML).
	bad := "app:\n" +
		"  id: x\n" +
		"  version: 0.1.0\n" +
		"root: s\n" +
		"states:\n" +
		"\t s:\n" + // line 6: illegal tab indentation
		"    view: \"s\"\n"

	_, err := app.LoadBytes([]byte(bad))
	require.Error(t, err, "malformed YAML must fail to load")

	var ve *app.ValidationError
	require.ErrorAs(t, err, &ve, "a YAML syntax error must surface as *ValidationError")
	require.Greater(t, ve.Line, 0,
		"a YAML syntax error must report a 1-based line number (got %d); the goccy token position must not be discarded", ve.Line)
}
