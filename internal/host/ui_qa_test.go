package host

import (
	"context"
	"testing"
)

func TestUIQARunHandlerRequiresGateOperation(t *testing.T) {
	result, err := UIQARunHandler(context.Background(), map[string]any{"op": "other"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != `host.ui_qa.run: unknown op "other"` {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestUIQARunHandlerRequiresInputs(t *testing.T) {
	result, err := UIQARunHandler(context.Background(), map[string]any{"op": "gate", "cwd": "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "host.ui_qa.run: video_path is required" {
		t.Fatalf("error = %q", result.Error)
	}
}
