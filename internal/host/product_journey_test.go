package host

import (
	"context"
	"testing"
)

func TestProductJourneyRunHandlerRequiresArgs(t *testing.T) {
	result, err := ProductJourneyRunHandler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "host.product_journey.run: args are required" {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestLastJSONLine(t *testing.T) {
	value, ok := lastJSONLine("progress\n{\"status\":\"ready\"}\n")
	if !ok || value.(map[string]any)["status"] != "ready" {
		t.Fatalf("value = %#v, ok = %v", value, ok)
	}
}
