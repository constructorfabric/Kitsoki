package graphsrv

import "testing"

func TestClassifyWriteReject_NeedsCanonicalization(t *testing.T) {
	reasons := []any{"NEEDS_CANONICALIZATION: catalog.yaml: file is not in canonical re-marshal form"}
	if code := classifyWriteReject(reasons, false); code != CodeNeedsCanonicalization {
		t.Fatalf("code = %s, want %s", code, CodeNeedsCanonicalization)
	}
}

func TestClassifyWriteReject_CatalogLintBlocked(t *testing.T) {
	if code := classifyWriteReject(nil, true); code != CodeCatalogLintBlocked {
		t.Fatalf("code = %s, want %s", code, CodeCatalogLintBlocked)
	}
}

func TestClassifyWriteReject_GenericValidation(t *testing.T) {
	reasons := []any{"graph propose: node \"req-alpha\" changed since Before was captured"}
	if code := classifyWriteReject(reasons, false); code != CodeValidation {
		t.Fatalf("code = %s, want %s", code, CodeValidation)
	}
}

func TestClassifyWriteReject_CanonicalizationTakesPriorityOverLint(t *testing.T) {
	reasons := []any{"NEEDS_CANONICALIZATION: catalog.yaml: ..."}
	if code := classifyWriteReject(reasons, true); code != CodeNeedsCanonicalization {
		t.Fatalf("code = %s, want %s", code, CodeNeedsCanonicalization)
	}
}
