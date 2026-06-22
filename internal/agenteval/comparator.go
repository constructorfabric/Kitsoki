package agenteval

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

func Compare(spec ComparatorSpec, expect, actual map[string]any) CompareResult {
	switch spec.Kind {
	case "exact":
		if reflect.DeepEqual(normalizeJSON(expect), normalizeJSON(actual)) {
			return CompareResult{Pass: true}
		}
		return CompareResult{Reason: "actual does not exactly match expected"}
	case "field_subset":
		if subset(expect, actual) {
			return CompareResult{Pass: true}
		}
		return CompareResult{Reason: "actual does not contain expected field subset"}
	case "enum":
		field := spec.Field
		if field == "" {
			field = inferEnumField(expect)
		}
		if field == "" {
			return CompareResult{Reason: "enum comparator needs a field or single expected key"}
		}
		if fmt.Sprint(expect[field]) == fmt.Sprint(actual[field]) {
			return CompareResult{Pass: true}
		}
		return CompareResult{Reason: fmt.Sprintf("field %q got %v want %v", field, actual[field], expect[field])}
	case "artifact_diff":
		if subset(expect, actual) {
			return CompareResult{Pass: true}
		}
		return CompareResult{Reason: "artifact does not match expected accepted subset"}
	case "judge":
		return CompareResult{Reason: "judge comparator is live-gated and has no deterministic offline result"}
	default:
		return CompareResult{Reason: fmt.Sprintf("unsupported comparator %q", spec.Kind)}
	}
}

func inferEnumField(expect map[string]any) string {
	if len(expect) == 1 {
		for k := range expect {
			return k
		}
	}
	for _, k := range []string{"intent", "verdict", "decision", "status"} {
		if _, ok := expect[k]; ok {
			return k
		}
	}
	return ""
}

func subset(expect, actual any) bool {
	exp := normalizeJSON(expect)
	act := normalizeJSON(actual)
	em, eok := exp.(map[string]any)
	am, aok := act.(map[string]any)
	if eok && aok {
		for k, ev := range em {
			av, ok := am[k]
			if !ok || !subset(ev, av) {
				return false
			}
		}
		return true
	}
	es, eok := exp.([]any)
	as, aok := act.([]any)
	if eok && aok {
		if len(es) > len(as) {
			return false
		}
		for i := range es {
			if !subset(es[i], as[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(exp, act)
}

func normalizeJSON(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

func DiffFields(expect, actual map[string]any) string {
	var missing []string
	for k, ev := range expect {
		if av, ok := actual[k]; !ok || !subset(ev, av) {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	return strings.Join(missing, ", ")
}
