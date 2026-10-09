package testrunner

import "testing"

// ─── PlayedEpisodes / consumption accounting ──────────────────────────────────
//
// PlayedEpisodes is the complement of UnmatchedEpisodes (cassette_test.go);
// these tests pin the shared invariant: every episode is in exactly one of
// the two sets, in cassette position order.

// TestCassette_PlayedEpisodes_EmptyBeforeAnyCall verifies that with no calls
// made, PlayedEpisodes returns nothing.
func TestCassette_PlayedEpisodes_EmptyBeforeAnyCall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: ep1
    match:
      handler: host.run
    response:
      data: {ok: true}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	if played := cas.PlayedEpisodes(); len(played) != 0 {
		t.Errorf("expected no played episodes before any call, got %v", played)
	}
}

// TestCassette_PlayedEpisodes_ComplementsUnmatched verifies that after playing
// one of two episodes, PlayedEpisodes returns exactly the consumed one and
// UnmatchedEpisodes exactly the other.
func TestCassette_PlayedEpisodes_ComplementsUnmatched(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: ep1
    match:
      handler: host.run
    response:
      data: {n: 1}
  - id: ep2
    match:
      handler: host.agent
    response:
      data: {n: 2}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// Play only ep1.
	if _, err := invokeDispatcher(t, cas, "host.run", nil, "", nil, nil); err != nil {
		t.Fatalf("play ep1: %v", err)
	}

	if played := cas.PlayedEpisodes(); len(played) != 1 || played[0] != "ep1" {
		t.Errorf("expected [ep1] as played, got %v", played)
	}
	if unmatched := cas.UnmatchedEpisodes(); len(unmatched) != 1 || unmatched[0] != "ep2" {
		t.Errorf("expected [ep2] as unmatched, got %v", unmatched)
	}
}

// TestCassette_PlayedEpisodes_ReplayAnyListedOnce verifies that a replay: any
// episode consumed several times appears in PlayedEpisodes exactly once —
// the accessor reports the consumed SET, not a play tally (see the doc
// comment for why counts are deliberately not exposed).
func TestCassette_PlayedEpisodes_ReplayAnyListedOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: reusable
    match:
      handler: host.run
    replay: any
    response:
      data: {result: always}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := invokeDispatcher(t, cas, "host.run", nil, "", nil, nil); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}

	if played := cas.PlayedEpisodes(); len(played) != 1 || played[0] != "reusable" {
		t.Errorf("expected [reusable] listed once, got %v", played)
	}
}
