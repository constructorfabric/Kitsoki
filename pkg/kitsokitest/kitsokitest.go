// Package kitsokitest provides testing helpers for Kitsoki app authors (§10, §12).
// It is a public package (pkg/) so app authors can import it in their own test suites.
package kitsokitest

import (
	// Blank import keeps testify in go.mod after tidy.
	_ "github.com/stretchr/testify/require"
)

// TODO(stage-3): implement Mode 1 (input→intent pass-rate fixtures) and
// Mode 2 (deterministic flow tests) helpers, along with the YAML oracle
// format and the cassette recorder (§10).
