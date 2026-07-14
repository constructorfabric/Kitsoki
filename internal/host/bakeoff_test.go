package host

import (
	"context"
	"testing"
)

func TestBakeoffRunRejectsUnknownOperation(t *testing.T) {
	result, err := BakeoffRunHandler(context.Background(), map[string]any{"op": "unknown", "cwd": "."})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Fatalf("expected structured host error, got %#v", result)
	}
}

func TestBakeoffRunRequiresBenchArgsToBeAList(t *testing.T) {
	result, err := BakeoffRunHandler(context.Background(), map[string]any{
		"op":  "bench",
		"cwd": ".",
		"args": "preflight",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Fatalf("expected structured argument error, got %#v", result)
	}
}
