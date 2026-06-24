package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFeature creates root/features/<id>.yaml under dir so discoverFeatureRoot
// can find it. It returns the directory that now holds the feature.
func writeFeature(t *testing.T, dir, featureID string) {
	t.Helper()
	featuresDir := filepath.Join(dir, "features")
	if err := os.MkdirAll(featuresDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", featuresDir, err)
	}
	path := filepath.Join(featuresDir, featureID+".yaml")
	if err := os.WriteFile(path, []byte("steps: []\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDiscoverFeatureRoot(t *testing.T) {
	const featureID = "gears-prd-design"

	t.Run("cwd walk finds nearest ancestor and ignores $KITSOKI_REPO", func(t *testing.T) {
		// The feature root is an ancestor of the start dir.
		featureRoot := t.TempDir()
		writeFeature(t, featureRoot, featureID)
		startDir := filepath.Join(featureRoot, "stories", "gears-rust")
		if err := os.MkdirAll(startDir, 0o755); err != nil {
			t.Fatal(err)
		}

		// A DIFFERENT $KITSOKI_REPO also has the feature — it must lose to the cwd walk.
		kitsokiRepo := t.TempDir()
		writeFeature(t, kitsokiRepo, featureID)
		getenv := func(k string) string {
			if k == "KITSOKI_REPO" {
				return kitsokiRepo
			}
			return ""
		}

		got := discoverFeatureRoot(featureID, startDir, getenv)
		if got != featureRoot {
			t.Fatalf("got %q, want feature root %q (cwd walk must beat $KITSOKI_REPO)", got, featureRoot)
		}
	})

	t.Run("falls back to $KITSOKI_REPO when cwd walk finds nothing", func(t *testing.T) {
		// Start dir tree has no feature anywhere up the chain.
		startDir := t.TempDir()

		kitsokiRepo := t.TempDir()
		writeFeature(t, kitsokiRepo, featureID)
		getenv := func(k string) string {
			if k == "KITSOKI_REPO" {
				return kitsokiRepo
			}
			return ""
		}

		got := discoverFeatureRoot(featureID, startDir, getenv)
		if got != kitsokiRepo {
			t.Fatalf("got %q, want $KITSOKI_REPO fallback %q", got, kitsokiRepo)
		}
	})

	t.Run("returns start dir when neither has the feature", func(t *testing.T) {
		startDir := t.TempDir()

		// $KITSOKI_REPO is set but does NOT contain the feature → must not be returned.
		kitsokiRepo := t.TempDir()
		getenv := func(k string) string {
			if k == "KITSOKI_REPO" {
				return kitsokiRepo
			}
			return ""
		}

		got := discoverFeatureRoot(featureID, startDir, getenv)
		if got != startDir {
			t.Fatalf("got %q, want start dir %q (caller emits 'feature not found')", got, startDir)
		}
	})
}
