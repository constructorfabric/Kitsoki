package host

import (
	"context"
	"testing"
)

func TestSessionMiningRunRejectsUnknownOperation(t *testing.T) {
	result, err := SessionMiningRunHandler(context.Background(), map[string]any{
		"op":        "unknown",
		"tools_dir": ".",
		"args":      []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Fatalf("expected structured host error, got %#v", result)
	}
}

func TestSessionMiningRunRequiresArgs(t *testing.T) {
	result, err := SessionMiningRunHandler(context.Background(), map[string]any{
		"op":        "prep",
		"tools_dir": ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "host.session_mining.run: args are required" {
		t.Fatalf("error = %q", result.Error)
	}
}
