package corpusintake

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadArenaYAMLNormalizesWithoutVerificationFlags(t *testing.T) {
	data := []byte("kind: arena_cost_corpus\ntasks:\n  - id: one\n    repo_label: example/repo\n    archetype: bugfix\n    baseline_sha: base\n    fix_sha: fix\n    ticket: repair it\n    verified_red: true\n    verified_green: true\n    oracle: {kind: command, run: go test ./...}\n")
	candidates, err := LoadArenaYAML(data, "tools/arena/corpus/example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("got %d candidates", len(candidates))
	}
	c := candidates[0]
	if c.Kind != KindCorpusCaseV1 || c.Source != SourceArena {
		t.Fatalf("unexpected candidate: %#v", c)
	}
	if c.Provenance.Locator != "tools/arena/corpus/example.yaml#one" || c.Provenance.SourceDigest == "" {
		t.Fatalf("missing provenance: %#v", c.Provenance)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "verified_red") {
		t.Fatalf("candidate leaked untrusted verification: %s", b)
	}
}

func TestLoadExternalBakeoffYAMLMergesProjectAndBugOracle(t *testing.T) {
	data := []byte("kind: bugfix_bakeoff_external\nproject:\n  id: sample\n  repo: https://example.test/sample.git\n  oracle: {run: 'go test {target}'}\nbugs:\n  - id: b1\n    title: a real bug\n    baseline_sha: base\n    fix_sha: fix\n    oracle_test: test/oracle_test.go\n    oracle_match: TestOracle\n")
	candidates, err := LoadExternalBakeoffYAML(data, "external/sample/manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c := candidates[0]
	if c.ID != "sample-b1" || c.Archetype != "bugfix" {
		t.Fatalf("unexpected candidate: %#v", c)
	}
	var oracle map[string]any
	if err := json.Unmarshal(c.Oracle, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle["oracle_test"] != "test/oracle_test.go" {
		t.Fatalf("missing injected oracle fixture: %#v", oracle)
	}
}

func TestDiscoverLocalRequiresExplicitOptIn(t *testing.T) {
	_, err := DiscoverLocal(context.Background(), fakeHistory{}, DiscoveryOptions{})
	if !errors.Is(err, ErrDiscoveryDisabled) {
		t.Fatalf("got %v, want discovery disabled", err)
	}
	candidates, err := DiscoverLocal(context.Background(), fakeHistory{}, DiscoveryOptions{AllowLive: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidates[0].Source; got != SourceLocalGitHistory {
		t.Fatalf("source = %q", got)
	}
}

func TestCommittedArenaAndExternalManifestsNormalize(t *testing.T) {
	arena, err := os.ReadFile(filepath.Join("..", "..", "tools", "arena", "corpus", "cost-bench.manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	gotArena, err := LoadArenaYAML(arena, "tools/arena/corpus/cost-bench.manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(gotArena) == 0 {
		t.Fatal("Arena manifest yielded no candidates")
	}
	external, err := os.ReadFile(filepath.Join("..", "..", "tools", "bugfix-bakeoff", "external", "projects", "query-string", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	gotExternal, err := LoadExternalBakeoffYAML(external, "tools/bugfix-bakeoff/external/projects/query-string/manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(gotExternal) != 3 {
		t.Fatalf("external candidates = %d, want 3", len(gotExternal))
	}
}

type fakeHistory struct{}

func (fakeHistory) Candidates(context.Context) ([]HistoryRecord, error) {
	return []HistoryRecord{{ID: "local-1", Repo: ".", BaselineRef: "base", FixRef: "fix", Ticket: "repair", Archetype: "bugfix", Locator: "HEAD~1..HEAD", Revision: "HEAD", Oracle: map[string]string{"run": "go test ./..."}}}, nil
}
