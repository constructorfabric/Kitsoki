package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DecompositionUpdateHandler owns the managed decomposition transaction. The
// story-facing operation is intentionally small: Starlark supplies paths and
// policy, while Go performs validation, versioning, writes, and event logging.
// This replaces the former Python transaction without making stories shell out
// to an interpreter.
func DecompositionUpdateHandler(_ context.Context, args map[string]any) (Result, error) {
	if stringValue(args, "op") == "self-test" {
		return Result{Data: decompositionSelfTest()}, nil
	}
	result, err := applyDecompositionUpdate(args)
	if err != nil {
		return Result{Data: map[string]any{"route": "fail", "ok": false, "exit_code": 1, "summary": err.Error(), "error": err.Error()}}, nil
	}
	data := map[string]any{"route": "ok", "ok": true, "exit_code": 0}
	for key, value := range result {
		data[key] = value
	}
	data["summary"] = "PASS: decomposition-update managed delta transaction"
	return Result{Data: data}, nil
}

func applyDecompositionUpdate(args map[string]any) (map[string]any, error) {
	basePath := stringValue(args, "base")
	deltaPath := stringValue(args, "delta")
	outPath := stringValue(args, "out")
	versionsPath := stringValue(args, "versions_dir")
	eventPath := stringValue(args, "event_log")
	if basePath == "" || deltaPath == "" || outPath == "" || versionsPath == "" || eventPath == "" {
		return nil, fmt.Errorf("base, delta, out, versions_dir, and event_log are required")
	}
	base, err := loadDecompositionYAML(basePath)
	if err != nil {
		return nil, err
	}
	delta, err := loadDecompositionYAML(deltaPath)
	if err != nil {
		return nil, err
	}
	if err := validateDecompositionHeader(delta); err != nil {
		return nil, err
	}
	locked, err := decompositionLockedIDs(stringValue(args, "ledger"))
	if err != nil {
		return nil, err
	}
	listKey := defaultString(stringValue(args, "list_key"), "changes")
	if err := applyDecompositionOperations(base, delta, locked, listKey); err != nil {
		return nil, err
	}
	if !boolValue(args, "skip_validate") {
		if err := validateDecompositionGraph(base, listKey); err != nil {
			return nil, err
		}
	}
	versionID := stringValue(args, "version_id")
	if versionID == "" {
		versionID = nextDecompositionVersion(versionsPath)
	}
	versionPath := filepath.Join(versionsPath, "decomposition."+versionID+".yaml")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	if err := os.MkdirAll(versionsPath, 0o755); err != nil {
		return nil, fmt.Errorf("create versions directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(eventPath), 0o755); err != nil {
		return nil, fmt.Errorf("create event directory: %w", err)
	}
	baseBytes, err := yaml.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("marshal decomposition: %w", err)
	}
	original, err := os.ReadFile(basePath)
	if err != nil {
		return nil, fmt.Errorf("read base: %w", err)
	}
	if err := os.WriteFile(versionPath, original, 0o644); err != nil {
		return nil, fmt.Errorf("write version: %w", err)
	}
	if err := os.WriteFile(outPath, baseBytes, 0o644); err != nil {
		return nil, fmt.Errorf("write output: %w", err)
	}
	added := decompositionAddedIDs(delta)
	event := map[string]any{
		"kind": "plan_evolution", "trigger": stringValue(delta, "trigger"),
		"provenance": delta["provenance"], "version_path": versionPath,
		"delta_path": deltaPath, "added": added,
	}
	line, _ := json.Marshal(event)
	prior, _ := os.ReadFile(eventPath)
	content := string(prior)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += string(line) + "\n"
	if err := os.WriteFile(eventPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write event log: %w", err)
	}
	return map[string]any{"out": outPath, "version_path": versionPath, "event_log": eventPath, "added": added}, nil
}

func loadDecompositionYAML(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("missing file: %s: %w", path, err)
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("invalid YAML %s: %w", path, err)
	}
	return doc, nil
}

func validateDecompositionHeader(delta map[string]any) error {
	if strings.TrimSpace(stringValue(delta, "trigger")) == "" {
		return fmt.Errorf("delta.trigger is required")
	}
	provenance := decompositionMap(delta["provenance"])
	if len(provenance) == 0 {
		return fmt.Errorf("delta.provenance mapping is required")
	}
	if strings.TrimSpace(stringValue(provenance, "kind")) == "" {
		return fmt.Errorf("delta.provenance.kind is required")
	}
	if strings.TrimSpace(stringValue(provenance, "ref")) == "" {
		return fmt.Errorf("delta.provenance.ref is required")
	}
	operations, ok := delta["operations"].([]any)
	if !ok || len(operations) == 0 {
		return fmt.Errorf("delta.operations must be a non-empty list")
	}
	return nil
}

func applyDecompositionOperations(base, delta map[string]any, locked map[string]bool, listKey string) error {
	changes, ok := base[listKey].([]any)
	if !ok {
		return fmt.Errorf("base document must contain %q list", listKey)
	}
	byID := map[string]bool{}
	for _, raw := range changes {
		if item := decompositionMap(raw); len(item) > 0 {
			byID[stringValue(item, "id")] = true
		}
	}
	operations := delta["operations"].([]any)
	for _, raw := range operations {
		op := decompositionMap(raw)
		if len(op) == 0 {
			return fmt.Errorf("each operation must be a mapping")
		}
		opname := stringValue(op, "op")
		if opname == "add_change" {
			change := decompositionMap(op["change"])
			if len(change) == 0 {
				return fmt.Errorf("add_change.change must be a mapping")
			}
			id := strings.TrimSpace(stringValue(change, "id"))
			if id == "" {
				return fmt.Errorf("add_change.change.id is required")
			}
			if byID[id] {
				return fmt.Errorf("change %q already exists", id)
			}
			changes = append(changes, change)
			byID[id] = true
			continue
		}
		if opname == "remove_change" || opname == "replace_change" {
			id := strings.TrimSpace(stringValue(op, "id"))
			if locked[id] {
				return fmt.Errorf("change %q is %s-locked by active ledger state", id, opname)
			}
		}
		return fmt.Errorf("unsupported operation %q", opname)
	}
	base[listKey] = changes
	return nil
}

func decompositionLockedIDs(path string) (map[string]bool, error) {
	locked := map[string]bool{}
	if path == "" {
		return locked, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("invalid ledger: %w", err)
	}
	rows, _ := doc["changes"].([]any)
	for _, raw := range rows {
		row := decompositionMap(raw)
		state := stringValue(row, "state")
		if state == "assigned" || state == "in_flight" || state == "reviewing" || state == "verified" {
			locked[stringValue(row, "change_id")] = true
		}
	}
	return locked, nil
}

func validateDecompositionGraph(doc map[string]any, listKey string) error {
	nodes, ok := doc[listKey].([]any)
	if !ok {
		return fmt.Errorf("%s: expected list", listKey)
	}
	ids := map[string]bool{}
	deps := map[string][]string{}
	for index, raw := range nodes {
		node := decompositionMap(raw)
		if len(node) == 0 {
			return fmt.Errorf("%s[%d] is not an object", listKey, index)
		}
		id := strings.TrimSpace(stringValue(node, "id"))
		if id == "" {
			return fmt.Errorf("%s[%d] missing id", listKey, index)
		}
		if ids[id] {
			return fmt.Errorf("duplicate id %q", id)
		}
		ids[id] = true
		deps[id] = decompositionStringList(node["depends_on"])
		if len(deps[id]) == 0 {
			deps[id] = decompositionStringList(node["deps"])
		}
	}
	for id, required := range deps {
		for _, dep := range required {
			if !ids[dep] {
				return fmt.Errorf("%s: depends_on unknown id %q", id, dep)
			}
		}
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("dependency cycle among ids")
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, dep := range deps[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range ids {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func decompositionStringList(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, stringValue(map[string]any{"value": item}, "value"))
	}
	return out
}

func decompositionAddedIDs(delta map[string]any) []any {
	ids := []any{}
	for _, raw := range delta["operations"].([]any) {
		op := decompositionMap(raw)
		if stringValue(op, "op") == "add_change" {
			ids = append(ids, stringValue(decompositionMap(op["change"]), "id"))
		}
	}
	return ids
}

func nextDecompositionVersion(dir string) string {
	entries, _ := os.ReadDir(dir)
	max := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "decomposition.v") || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		var number int
		_, _ = fmt.Sscanf(strings.TrimSuffix(strings.TrimPrefix(name, "decomposition.v"), ".yaml"), "%d", &number)
		if number > max {
			max = number
		}
	}
	return fmt.Sprintf("v%04d", max+1)
}

func decompositionMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return nil
}

func boolValue(args map[string]any, key string) bool {
	value, _ := args[key].(bool)
	return value
}

func decompositionSelfTest() map[string]any {
	root, err := os.MkdirTemp("", "kitsoki-decomposition-update-")
	if err != nil {
		return map[string]any{"ok": false, "route": "fail", "error": err.Error()}
	}
	defer os.RemoveAll(root)
	base := filepath.Join(root, "base.yaml")
	delta := filepath.Join(root, "delta.yaml")
	baseDoc := "changes:\n  - id: base\n    depends_on: []\n"
	deltaDoc := "trigger: session-fold-in\nprovenance: {kind: test, ref: fixture}\noperations:\n  - op: add_change\n    change: {id: added, depends_on: [base]}\n"
	if err := os.WriteFile(base, []byte(baseDoc), 0o644); err != nil {
		return map[string]any{"ok": false, "route": "fail", "error": err.Error()}
	}
	if err := os.WriteFile(delta, []byte(deltaDoc), 0o644); err != nil {
		return map[string]any{"ok": false, "route": "fail", "error": err.Error()}
	}
	args := map[string]any{"base": base, "delta": delta, "out": filepath.Join(root, "out.yaml"), "versions_dir": filepath.Join(root, "versions"), "event_log": filepath.Join(root, "events.jsonl"), "version_id": "test"}
	if _, err := applyDecompositionUpdate(args); err != nil {
		return map[string]any{"ok": false, "route": "fail", "error": err.Error()}
	}
	return map[string]any{"ok": true, "route": "ok", "summary": "PASS: decomposition-update managed delta transaction"}
}
