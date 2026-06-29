package jobs

import (
	"context"
	"testing"
)

// TestAssets_RoundTrip proves the on-disk + metadata asset store: PutAsset
// writes the blob and a gh_job_assets row, GetAssetData reads the same bytes
// and recorded MIME type back, and ListAssets reports the metadata. This is
// exactly the contract the hosted /run/<job-id>/assets/<name> handler relies
// on, so a bare file copy (no DB row) would not be served.
func TestAssets_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestGHStore(t)
	s.DataDir = t.TempDir()

	m := GHMention{OriginRef: "github:o/r/issue/7", Repo: "o/r", ObjectKind: "issue", ObjectNumber: "7"}
	job, _, err := s.Claim(ctx, m, "w1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	body := []byte("<html><body>deck</body></html>")
	if err := s.PutAsset(ctx, job.JobID, "deck.html", "text/html", body); err != nil {
		t.Fatalf("PutAsset: %v", err)
	}

	got, mime, err := s.GetAssetData(ctx, job.JobID, "deck.html")
	if err != nil {
		t.Fatalf("GetAssetData: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("asset bytes = %q, want %q", got, body)
	}
	if mime != "text/html" {
		t.Errorf("mime = %q, want text/html", mime)
	}

	// Overwriting the same name replaces the blob and metadata (INSERT OR REPLACE).
	body2 := []byte("<html><body>deck v2</body></html>")
	if err := s.PutAsset(ctx, job.JobID, "deck.html", "text/html", body2); err != nil {
		t.Fatalf("PutAsset overwrite: %v", err)
	}
	got2, _, err := s.GetAssetData(ctx, job.JobID, "deck.html")
	if err != nil {
		t.Fatalf("GetAssetData after overwrite: %v", err)
	}
	if string(got2) != string(body2) {
		t.Errorf("after overwrite asset bytes = %q, want %q", got2, body2)
	}

	assets, err := s.ListAssets(ctx, job.JobID)
	if err != nil {
		t.Fatalf("ListAssets: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("ListAssets returned %d rows, want 1", len(assets))
	}
	if assets[0].Name != "deck.html" {
		t.Errorf("asset name = %q, want deck.html", assets[0].Name)
	}
	if assets[0].SizeBytes != int64(len(body2)) {
		t.Errorf("asset size = %d, want %d", assets[0].SizeBytes, len(body2))
	}
}

// TestPutAsset_RequiresDataDir proves PutAsset refuses to write when the store
// has no asset directory configured, so a misconfigured server fails loudly
// instead of silently dropping deck uploads.
func TestPutAsset_RequiresDataDir(t *testing.T) {
	ctx := context.Background()
	s := newTestGHStore(t)
	// DataDir intentionally left empty.
	if err := s.PutAsset(ctx, "01ABC", "deck.html", "text/html", []byte("x")); err == nil {
		t.Fatal("PutAsset should fail when DataDir is unset")
	}
}

// TestPutAsset_UnknownJob proves an asset cannot be attached to a job that does
// not exist, keeping the gh_job_assets foreign key intact.
func TestPutAsset_UnknownJob(t *testing.T) {
	ctx := context.Background()
	s := newTestGHStore(t)
	s.DataDir = t.TempDir()
	if err := s.PutAsset(ctx, "01DOESNOTEXIST", "deck.html", "text/html", []byte("x")); err == nil {
		t.Fatal("PutAsset should fail for an unknown job id")
	}
}
