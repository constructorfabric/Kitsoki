package corpusintake_test

import (
	"fmt"
	"kitsoki/internal/corpusintake"
)

func ExampleLoadArenaYAML() {
	data := []byte("kind: arena\ntasks:\n- id: repair-1\n  repo: demo\n  archetype: bugfix\n  baseline_sha: before\n  fix_sha: after\n  ticket: fix parser\n  oracle: {run: go test ./...}\n")
	candidates, err := corpusintake.LoadArenaYAML(data, "review/arena.yaml")
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s %s %s\n", candidates[0].Kind, candidates[0].Source, candidates[0].Provenance.Locator)
	// Output:
	// corpus-case.v1 arena review/arena.yaml#repair-1
}
