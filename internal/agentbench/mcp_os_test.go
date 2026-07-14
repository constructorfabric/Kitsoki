package agentbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMCPOSCorpusFixture(t *testing.T) {
	path := filepath.Join("..", "..", "tools", "arena", "corpus", "mcp-os", "corpus.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := LoadMCPOSCorpus(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(corpus.Label, "Current Studio MCP toolbox") {
		t.Fatalf("baseline label must identify current-toolbox behavior: %q", corpus.Label)
	}
	if got, want := len(corpus.Cases), 12; got != want {
		t.Fatalf("case count = %d, want %d", got, want)
	}
	hash, err := MCPOSCorpusSHA256(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hash, "ccc5aac379225fee8fd5d2c16d1f26b31f5c96a0c843d94550512f94d3496277"; got != want {
		t.Fatalf("corpus hash = %s, want %s", got, want)
	}
}

func TestLoadMCPOSCorpusRejectsIncompleteOrInconsistentData(t *testing.T) {
	_, err := LoadMCPOSCorpus([]byte(`{"schema_version":"mcp_os_corpus/v1","label":"x","cases":[]}`))
	if err == nil || !strings.Contains(err.Error(), "want 12") {
		t.Fatalf("incomplete corpus error = %v", err)
	}
	_, err = LoadMCPOSCorpus([]byte(`{"schema_version":"mcp_os_corpus/v1","label":"x","cases":[` + strings.Repeat(`{"id":"same","kind":"story_edit","description":"x"},`, 11) + `{"id":"same","kind":"story_edit","description":"x"}]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate corpus error = %v", err)
	}
}
