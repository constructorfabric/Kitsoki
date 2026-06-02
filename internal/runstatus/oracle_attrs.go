package runstatus

import (
	"encoding/json"
	"strings"
)

// IsOracleCompleteMsg reports whether msg is one of the oracle.<verb>.complete
// slog messages emitted by the oracle handlers.
func IsOracleCompleteMsg(msg string) bool {
	return strings.HasSuffix(msg, ".complete") &&
		strings.HasPrefix(msg, "oracle.")
}

// MergeOracleBodyIntoAttrs merges the KindOracleCall body JSON into the attrs
// map, skipping keys already set (lean slog values win).
func MergeOracleBodyIntoAttrs(attrs map[string]any, body json.RawMessage) {
	if attrs == nil {
		return
	}
	var full map[string]any
	if err := json.Unmarshal(body, &full); err != nil {
		return
	}
	for k, v := range full {
		// Lean slog attrs already set from the slog record win over journal
		// values for the small set of keys both carry (model, duration_ms, etc.).
		// The big keys (system_prompt, prompt, input, response) are only in the
		// journal so they always get merged.
		if _, alreadySet := attrs[k]; alreadySet {
			continue
		}
		attrs[k] = v
	}
}
