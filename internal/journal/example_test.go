// Runnable godoc examples for the journal surface. Each Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/journal/...`.
package journal_test

import (
	"encoding/json"
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
)

// ExampleApplier_Apply is the worked example from the package doc: starting
// from a world snapshot, two patches fold onto it in order. "gold" is declared
// "int" in the schema, so the schema-aware Applier keeps it int64 rather than
// letting encoding/json drift it to float64.
func ExampleApplier_Apply() {
	schema := app.WorldSchema{
		"gold": {Type: "int"},
		"oxen": {Type: "int"},
	}
	a := journal.NewApplier(schema)

	doc := json.RawMessage(`{"vars":{"gold":100}}`)

	doc, err := a.Apply("world", doc, []journal.PatchOp{
		{Op: "replace", Path: "/vars/gold", Value: json.RawMessage("80")},
	})
	if err != nil {
		panic(err)
	}
	doc, err = a.Apply("world", doc, []journal.PatchOp{
		{Op: "add", Path: "/vars/oxen", Value: json.RawMessage("2")},
	})
	if err != nil {
		panic(err)
	}

	// Decode through CoerceWorldVars — the canonical consumer pattern that
	// recovers typed Go values (int64, not float64) from the world body.
	var out struct {
		Vars map[string]any `json:"vars"`
	}
	if err := json.Unmarshal(doc, &out); err != nil {
		panic(err)
	}
	journal.CoerceWorldVars(out.Vars, schema)

	fmt.Printf("gold: %d (%T)\n", out.Vars["gold"], out.Vars["gold"])
	fmt.Printf("oxen: %d (%T)\n", out.Vars["oxen"], out.Vars["oxen"])
	// Output:
	// gold: 80 (int64)
	// oxen: 2 (int64)
}

// ExampleReader_LoadDocument shows the resume path end to end on an in-memory
// store: a checkpoint plus two patches are written, LoadDocument returns the
// checkpoint snapshot and the highest version seen, and ReplayFrom feeds the
// post-checkpoint patches to an Applier to reconstruct the current document.
func ExampleReader_LoadDocument() {
	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	const sid app.SessionID = "demo"

	// v1: a full "world" snapshot.
	if err := w.AppendCheckpoint(sid, 1, 0, "world",
		json.RawMessage(`{"vars":{"gold":100}}`)); err != nil {
		panic(err)
	}
	// v2, v3: two world patches (DocVersion assigned by the writer).
	if err := w.Append(journal.Entry{
		Session: sid, Turn: 2, Seq: 0, Kind: journal.KindWorldPatch, Doc: "world",
		Body: json.RawMessage(`[{"op":"replace","path":"/vars/gold","value":80}]`),
	}); err != nil {
		panic(err)
	}
	if err := w.Append(journal.Entry{
		Session: sid, Turn: 3, Seq: 0, Kind: journal.KindWorldPatch, Doc: "world",
		Body: json.RawMessage(`[{"op":"add","path":"/vars/oxen","value":2}]`),
	}); err != nil {
		panic(err)
	}

	// LoadDocument returns the latest checkpoint snapshot and the highest
	// DocVersion seen for (sid, "world").
	snapshot, version, err := r.LoadDocument(sid, "world")
	if err != nil {
		panic(err)
	}
	fmt.Printf("snapshot: %s\n", snapshot)
	fmt.Printf("version:  %d\n", version)

	// Replay the patches written after the checkpoint, folding each onto the
	// snapshot to reconstruct the current document.
	cp, _, err := r.LatestCheckpoint(sid, "world")
	if err != nil {
		panic(err)
	}
	a := journal.NewApplier(nil)
	doc := snapshot
	seq, errFn := r.ReplayFrom(sid, "world", cp.DocVersion+1)
	for e := range seq {
		var ops []journal.PatchOp
		if err := json.Unmarshal(e.Body, &ops); err != nil {
			panic(err)
		}
		if doc, err = a.Apply("world", doc, ops); err != nil {
			panic(err)
		}
	}
	if err := errFn(); err != nil {
		panic(err)
	}
	fmt.Printf("current:  %s\n", doc)
	// Output:
	// snapshot: {"vars":{"gold":100}}
	// version:  3
	// current:  {"vars":{"gold":80,"oxen":2}}
}
