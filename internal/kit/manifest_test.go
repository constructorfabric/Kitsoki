package kit

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const validKitYAML = `schema: kit/v1
kit: iso-9001
namespace: constructorfabric
version: 1.2.0
title: ISO 9001 quality kit
requires:
  kitsoki: ">=0.1.0"
extends:
  - kit: "@constructorfabric/iso-ms"
    constraint: "^2"
parameters:
  registrar:
    type: string
    required: true
  doc_repo:
    type: string
    default: "docs/qms"
provides:
  stories: [qms]
  schemas: [nonconformity/v1]
  interfaces: [reporter]
  onboarding: qms.onboard
conformance:
  flows: ["flows/ms-contract/*.yaml"]
compat:
  renamed:
    exits:
      closed: resolved
`

func TestValidate_ValidManifest(t *testing.T) {
	res, err := Validate([]byte(validKitYAML))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected valid manifest, got schema errors: %v", res.Schema)
	}
}

func TestValidate_RejectsUnknownSchema(t *testing.T) {
	bad := `schema: kit/v2
kit: x
namespace: y
version: 1.0.0
provides:
  stories: [a]
`
	res, err := Validate([]byte(bad))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if res.OK {
		t.Fatal("expected schema mismatch for kit/v2 to fail validation")
	}
}

func TestValidate_RejectsMissingRequired(t *testing.T) {
	bad := `schema: kit/v1
kit: x
namespace: y
version: 1.0.0
`
	res, err := Validate([]byte(bad))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if res.OK {
		t.Fatal("expected missing provides.stories to fail validation")
	}
}

func TestValidate_RejectsAdditionalProperties(t *testing.T) {
	bad := validKitYAML + "\nfrobnicate: true\n"
	res, err := Validate([]byte(bad))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if res.OK {
		t.Fatal("expected unknown top-level key to fail validation (additionalProperties: false)")
	}
}

func TestLoad_DecodesAndChecksStoryDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestFileName), validKitYAML)
	writeFile(t, filepath.Join(dir, "stories", "qms", "app.yaml"), "app:\n  id: qms\n  version: 0.1.0\nroot: idle\nstates:\n  idle:\n    description: idle\n")

	def, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if def.Identity() != "@constructorfabric/iso-9001" {
		t.Fatalf("Identity() = %q, want @constructorfabric/iso-9001", def.Identity())
	}
	if !def.HasStory("qms") {
		t.Fatal("expected HasStory(qms) true")
	}
	if def.HasStory("nope") {
		t.Fatal("expected HasStory(nope) false")
	}
	if got, want := def.Parameters["doc_repo"].Default, "docs/qms"; got != want {
		t.Fatalf("parameters.doc_repo.default = %v, want %v", got, want)
	}
	if got, want := def.Compat.Renamed["exits"]["closed"], "resolved"; got != want {
		t.Fatalf("compat.renamed.exits.closed = %q, want %q", got, want)
	}
	wantDir, _ := filepath.Abs(dir)
	if def.Dir() != wantDir {
		t.Fatalf("Dir() = %q, want %q", def.Dir(), wantDir)
	}
}

func TestLoad_FailsFastOnMissingStoryDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestFileName), validKitYAML)
	// Deliberately omit stories/qms/app.yaml.

	if _, err := LoadDir(dir); err == nil {
		t.Fatal("expected Load to fail when a declared provides.stories entry has no app.yaml on disk")
	}
}

func TestSchemaJSON_ReturnsCopy(t *testing.T) {
	a := SchemaJSON()
	b := SchemaJSON()
	if len(a) == 0 {
		t.Fatal("expected non-empty embedded schema")
	}
	a[0] = 0
	if b[0] == 0 {
		t.Fatal("SchemaJSON should return a defensive copy, not the shared backing array")
	}
}
