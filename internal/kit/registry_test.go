package kit

import (
	"path/filepath"
	"testing"
)

const testdataKitsDir = "../app/testdata/kits"

func TestRegistry_AddAndGet(t *testing.T) {
	def, err := LoadDir(filepath.Join(testdataKitsDir, "synthetic-kit"))
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	reg := NewRegistry()
	if err := reg.Add(def); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, ok := reg.Get("synthetic")
	if !ok {
		t.Fatal("Get(\"synthetic\") ok = false, want true")
	}
	if got.Identity() != "@kitsoki-test/synthetic" {
		t.Errorf("Identity() = %q, want @kitsoki-test/synthetic", got.Identity())
	}
	if reg.Len() != 1 {
		t.Errorf("Len() = %d, want 1", reg.Len())
	}
	if _, ok := reg.Get("nope"); ok {
		t.Error("Get(\"nope\") ok = true, want false")
	}
}

func TestRegistry_Add_DuplicateNameDifferentIdentity(t *testing.T) {
	def, err := LoadDir(filepath.Join(testdataKitsDir, "synthetic-kit"))
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	other := *def
	other.Namespace = "different-namespace"

	reg := NewRegistry()
	if err := reg.Add(def); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := reg.Add(&other); err == nil {
		t.Fatal("expected a collision error registering two different kits under the same short name")
	}
}

func TestDiscoverDir_LoadsEachKitYAML(t *testing.T) {
	reg, err := DiscoverDir(testdataKitsDir)
	if err != nil {
		t.Fatalf("DiscoverDir: %v", err)
	}
	if reg.Len() != 1 {
		t.Fatalf("Len() = %d, want 1 (synthetic-kit)", reg.Len())
	}
	if _, ok := reg.Get("synthetic"); !ok {
		t.Error("expected synthetic-kit to be discovered")
	}
}

func TestDiscoverDir_MissingRootIsEmptyNotError(t *testing.T) {
	reg, err := DiscoverDir("../app/testdata/kits/does-not-exist")
	if err != nil {
		t.Fatalf("DiscoverDir on a missing root should not error, got: %v", err)
	}
	if reg.Len() != 0 {
		t.Errorf("Len() = %d, want 0", reg.Len())
	}
}

func TestDiscoverDir_EmptyRootIsEmptyRegistry(t *testing.T) {
	reg, err := DiscoverDir("")
	if err != nil {
		t.Fatalf("DiscoverDir(\"\"): %v", err)
	}
	if reg.Len() != 0 {
		t.Errorf("Len() = %d, want 0", reg.Len())
	}
}
