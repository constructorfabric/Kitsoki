// task_optimization_plan_deck renders an offline Slidey review deck from an
// Arena task-optimization plan. It deliberately reads only JSON evidence and
// never resolves a profile, starts a container, or contacts a provider.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type plan struct {
	Schema      string `json:"schema"`
	StudyID     string `json:"study_id"`
	RepeatPhase string `json:"repeat_phase"`
	CellCount   int    `json:"cell_count"`
	Cells       []cell `json:"cells"`
}

type cell struct {
	TaskID      string `json:"task_id"`
	Split       string `json:"split"`
	CandidateID string `json:"candidate_id"`
	Treatment   string `json:"treatment"`
	Repeat      int    `json:"repeat"`
}

func main() {
	planPath := flag.String("plan", "", "Arena plan.json input")
	outPath := flag.String("out", "", "Slidey JSON output")
	flag.Parse()
	if *planPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: task_optimization_plan_deck --plan <plan.json> --out <deck.slidey.json>")
		os.Exit(2)
	}
	bytes, err := os.ReadFile(*planPath)
	if err != nil {
		fail(err)
	}
	var p plan
	if err := json.Unmarshal(bytes, &p); err != nil {
		fail(fmt.Errorf("decode plan: %w", err))
	}
	if p.Schema != "task-optimization/v1" || p.StudyID == "" || p.CellCount != len(p.Cells) {
		fail(fmt.Errorf("invalid task-optimization plan"))
	}
	bySplit := map[string]int{}
	byCandidate := map[string]int{}
	byTreatment := map[string]int{}
	for _, c := range p.Cells {
		bySplit[c.Split]++
		byCandidate[c.CandidateID]++
		byTreatment[c.Treatment]++
	}
	deck := map[string]any{
		"_comment": "Generated deterministically from Arena plan.json. Review artifact only: no provider was contacted.",
		"meta":     map[string]any{"title": "Task optimization plan: " + p.StudyID, "resolution": map[string]int{"width": 1920, "height": 1080}, "theme": "rose-pine-moon"},
		"scenes": []any{
			map[string]any{"type": "title", "eyebrow": "No-spend campaign review", "title": "Task optimization plan", "subtitle": fmt.Sprintf("%s · %d planned cells · %s repeats", p.StudyID, p.CellCount, p.RepeatPhase)},
			table("Cells by split", "Split", bySplit),
			table("Cells by candidate", "Candidate", byCandidate),
			table("Cells by treatment", "Treatment", byTreatment),
			map[string]any{"type": "cards", "variant": "grid", "title": "Arming boundary", "cards": []map[string]string{
				{"label": "Review", "sub": "Offline; no provider call", "style": "primary"},
				{"label": "Arm", "sub": "Explicit intent + --live + study gate", "style": "secondary"},
				{"label": "Dispatch", "sub": "Separate from the arm receipt", "style": "default"},
			}},
		},
	}
	out, err := json.MarshalIndent(deck, "", "  ")
	if err != nil {
		fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(*outPath, append(out, '\n'), 0o644); err != nil {
		fail(err)
	}
}

func table(title, label string, values map[string]int) map[string]any {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, map[string]any{"cells": []string{key, fmt.Sprint(values[key])}})
	}
	return map[string]any{"type": "table", "title": title, "columns": []string{label, "Cells"}, "rows": rows, "variant": "data"}
}

func fail(err error) { fmt.Fprintln(os.Stderr, "ERROR:", err); os.Exit(1) }
