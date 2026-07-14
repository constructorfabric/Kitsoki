package webconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func loadConfigText(t *testing.T, text string) (WebConfig, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), DefaultConfigFile)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoad_FeedbackRouting(t *testing.T) {
	cfg, err := loadConfigText(t, `feedback_routing:
  pog-portal:
    sink: catalog
    catalog: pog/catalog.yaml
    type: portal-feedback
    fields: [summary, report]
  slidey-studio:
    sink: local
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := map[string]FeedbackRoute{
		"pog-portal":    {Sink: "catalog", Catalog: "pog/catalog.yaml", Type: "portal-feedback", Fields: []string{"summary", "report"}},
		"slidey-studio": {Sink: "local"},
	}
	if !reflect.DeepEqual(cfg.FeedbackRouting, want) {
		t.Fatalf("FeedbackRouting = %#v, want %#v", cfg.FeedbackRouting, want)
	}
}

func TestLoad_FeedbackRoutingAbsentIsNil(t *testing.T) {
	cfg, err := loadConfigText(t, "story_dirs:\n  - ./stories\n")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.FeedbackRouting != nil {
		t.Fatalf("FeedbackRouting = %v, want nil", cfg.FeedbackRouting)
	}
}

func TestLoad_FeedbackRoutingInvalidSink(t *testing.T) {
	_, err := loadConfigText(t, `feedback_routing:
  pog-portal:
    sink: github
`)
	if err == nil || !strings.Contains(err.Error(), "sink") {
		t.Fatalf("want sink validation error, got %v", err)
	}
}

func TestLoad_FeedbackRoutingCatalogSinkRequiresType(t *testing.T) {
	_, err := loadConfigText(t, `feedback_routing:
  pog-portal:
    sink: catalog
`)
	if err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("want missing-type error, got %v", err)
	}
}
