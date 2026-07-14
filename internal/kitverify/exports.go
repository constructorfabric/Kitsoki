package kitverify

import (
	"fmt"

	"kitsoki/internal/app"
)

// CheckExportsIntents verifies that every intent name def.Exports.Intents
// lists actually exists in def.Intents.
//
// Today this is only checked from the IMPORTER side, at the moment some
// other story actually imports this one and asks for it via
// `imports.<alias>.intents.import` (imports.go's foldChild, the "child does
// not declare it in exports.intents" arm) — and even then only the imported
// name is checked, not every name the child bothers to export. A kit's own
// `exports:` block can name a typo'd or since-removed intent and nothing
// catches it until (if ever) a downstream kit tries to import exactly that
// name. This is the standalone existence check S4 adds so a kit author finds
// out at `kitsoki kit verify` time, not at some future importer's load time.
func CheckExportsIntents(def *app.AppDef) []string {
	if def == nil || def.Exports == nil {
		return nil
	}
	var out []string
	for _, name := range def.Exports.Intents {
		if _, ok := def.Intents[name]; !ok {
			out = append(out, fmt.Sprintf("exports.intents: %q is not defined in intents:", name))
		}
	}
	return out
}
