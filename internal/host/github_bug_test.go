package host_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/reportmeta"
)

// TestGitHubFileBug_UploadSuccess proves the opt-in release-asset path: evidence
// is uploaded as release assets and the issue body links the public asset URLs.
func TestGitHubFileBug_UploadSuccess(t *testing.T) {
	dir := t.TempDir()
	shot := filepath.Join(dir, "screenshot.png")
	har := filepath.Join(dir, "har.json")
	if err := os.WriteFile(shot, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(har, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GH_TOKEN", "test-token")
	var releaseCreated bool
	var uploads int
	var issueBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/releases/tags/kitsoki-artifacts":
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/releases":
			releaseCreated = true
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["prerelease"] != true {
				t.Fatalf("release prerelease = %v, want true", payload["prerelease"])
			}
			writeJSON(w, map[string]any{"upload_url": "http://" + r.Host + "/uploads/assets{?name,label}"})
		case r.Method == http.MethodPost && r.URL.Path == "/uploads/assets":
			uploads++
			if r.URL.Query().Get("name") == "" {
				t.Fatal("upload missing asset name")
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			issueBody, _ = payload["body"].(string)
			writeJSON(w, map[string]any{"number": 777, "html_url": "https://github.com/o/r/issues/777"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()

	res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
		Repo:            "o/r",
		Title:           "web: gate",
		Body:            "body",
		TraceRef:        "trace://abc",
		UploadArtifacts: true,
		Evidence: []host.EvidenceFile{
			{Name: "screenshot.png", Path: shot, Image: true, Label: "Screenshot"},
			{Name: "har.json", Path: har, Label: "HAR"},
		},
	})
	if err != nil {
		t.Fatalf("GitHubFileBug: %v", err)
	}
	if !releaseCreated {
		t.Error("expected release create when release missing")
	}
	if uploads != 2 {
		t.Errorf("expected 2 uploads, got %d", uploads)
	}
	// Asset URLs returned and threaded into the body.
	for _, name := range []string{"screenshot.png", "har.json"} {
		if !strings.HasPrefix(res.Assets[name], "https://github.com/o/r/releases/download/kitsoki-artifacts/") {
			t.Errorf("asset %q url = %q", name, res.Assets[name])
		}
	}
	for _, want := range []string{
		"uploaded as GitHub release assets",
		"![Screenshot](https://github.com/o/r/releases/download/kitsoki-artifacts/",
		"[HAR](https://github.com/o/r/releases/download/kitsoki-artifacts/",
	} {
		if !strings.Contains(issueBody, want) {
			t.Errorf("issue body missing %q\nbody: %s", want, issueBody)
		}
	}
	if strings.Contains(issueBody, "not uploaded to GitHub") {
		t.Error("upload path must drop the not-uploaded disclaimer")
	}
}

func TestGitHubFileBug_UploadClobbersExistingAsset(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.txt")
	if err := os.WriteFile(logPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GH_TOKEN", "test-token")
	var uploadAttempts int
	var deleted bool
	var conflictName string
	var issueBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/releases/tags/kitsoki-artifacts":
			writeJSON(w, map[string]any{"id": 42, "upload_url": "http://" + r.Host + "/uploads/assets{?name,label}"})
		case r.Method == http.MethodPost && r.URL.Path == "/uploads/assets":
			uploadAttempts++
			conflictName = r.URL.Query().Get("name")
			if uploadAttempts == 1 {
				http.Error(w, `{"errors":[{"code":"already_exists"}]}`, http.StatusUnprocessableEntity)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/releases/42/assets":
			writeJSON(w, []map[string]any{{"id": 99, "name": conflictName}})
		case r.Method == http.MethodDelete && r.URL.Path == "/repos/o/r/releases/assets/99":
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			issueBody, _ = payload["body"].(string)
			writeJSON(w, map[string]any{"number": 56, "html_url": "https://github.com/o/r/issues/56"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()

	res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
		Repo:            "o/r",
		Title:           "retry upload",
		Body:            "body",
		TraceRef:        "trace://dup",
		UploadArtifacts: true,
		Evidence:        []host.EvidenceFile{{Name: "log.txt", Path: logPath, Label: "Log"}},
	})
	if err != nil {
		t.Fatalf("GitHubFileBug: %v", err)
	}
	if uploadAttempts != 2 || !deleted {
		t.Fatalf("uploadAttempts=%d deleted=%v, want retry after delete", uploadAttempts, deleted)
	}
	if res.Number != "56" || res.Assets["log.txt"] == "" {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(issueBody, "releases/download/kitsoki-artifacts/") {
		t.Fatalf("issue body missing uploaded asset URL:\n%s", issueBody)
	}
}

// TestGitHubFileBug_UploadFailureFallsBack proves a graceful fallback: when the
// upload fails, the issue is still filed with developer-local path references.
func TestGitHubFileBug_UploadFailureFallsBack(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var issueBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/releases/tags/kitsoki-artifacts":
			writeJSON(w, map[string]any{"upload_url": "http://" + r.Host + "/uploads/assets{?name,label}"})
		case r.Method == http.MethodPost && r.URL.Path == "/uploads/assets":
			http.Error(w, `{"message":"boom: network down"}`, http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			issueBody, _ = payload["body"].(string)
			writeJSON(w, map[string]any{"number": 55, "html_url": "https://github.com/o/r/issues/55"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()

	res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
		Repo:            "o/r",
		Title:           "x",
		Body:            "b",
		UploadArtifacts: true,
		Evidence: []host.EvidenceFile{
			{Name: "log.txt", Path: ".artifacts/b/log.txt", Label: "Log"},
		},
	})
	if err != nil {
		t.Fatalf("GitHubFileBug: %v", err)
	}
	if res.Number != "55" {
		t.Fatalf("issue should still be filed; number=%q", res.Number)
	}
	// Fell back to local-path rendering.
	for _, want := range []string{
		"These files are not uploaded to GitHub.",
		"Log: `.artifacts/b/log.txt`",
	} {
		if !strings.Contains(issueBody, want) {
			t.Errorf("fallback body missing %q", want)
		}
	}
	if res.Assets["log.txt"] != ".artifacts/b/log.txt" {
		t.Errorf("fallback asset should be local path, got %q", res.Assets["log.txt"])
	}
}

// TestGitHubFileBug_WithEvidence proves the slice-#2 orchestration: create the
// issue with an Artifacts section (developer-local evidence paths) and the
// ```kitsoki metadata block.
func TestGitHubFileBug_WithEvidence(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var createdIssue bool
	var issueBody string
	var labels []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases") {
			t.Fatalf("evidence must not use release upload when UploadArtifacts=false: %s %s", r.Method, r.URL.String())
		}
		if r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues" {
			createdIssue = true
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			issueBody, _ = payload["body"].(string)
			labels, _ = payload["labels"].([]any)
			writeJSON(w, map[string]any{"number": 321, "html_url": "https://github.com/o/r/issues/321"})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()

	res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
		Repo:      "o/r",
		Title:     "web: surprising judge gate",
		Body:      "The gate fired where I didn't expect.",
		Severity:  "P2",
		Component: "web",
		Target:    "kitsoki",
		TraceRef:  "trace://x",
		FiledBy:   "brad",
		Runtime: reportmeta.Snapshot{
			Engine: reportmeta.Engine{
				Version:        "1.2.3",
				Revision:       "abcdef1234567890",
				RevisionShort:  "abcdef123456",
				Dirty:          "false",
				ChecksumSHA256: "sha256:engine",
			},
			Story: reportmeta.Story{
				AppID:          "cloak-of-darkness",
				Version:        "0.1.0",
				Entry:          "testdata/apps/cloak/app.yaml",
				ChecksumSHA256: "sha256:story",
			},
			PublicStories: []reportmeta.PublicStory{{
				Name:           "bug",
				AppID:          "bug-story",
				Version:        "0.2.0",
				Source:         "embedded",
				Path:           "internal/basestories/stories/bug/app.yaml",
				ChecksumSHA256: "sha256:public",
			}},
		},
		Evidence: []host.EvidenceFile{
			{Name: "screenshot.png", Path: ".artifacts/bug-reports/b1/screenshot.png", Image: true, Label: "Screenshot"},
			{Name: "har.json", Path: ".artifacts/bug-reports/b1/har.json", Label: "HAR (scrubbed)"},
		},
	})
	if err != nil {
		t.Fatalf("GitHubFileBug: %v", err)
	}
	if !createdIssue {
		t.Fatal("expected issue create")
	}
	if res.Number != "321" || !strings.Contains(res.URL, "/issues/321") {
		t.Fatalf("issue result: %+v", res)
	}
	if res.Assets["screenshot.png"] == "" || res.Assets["har.json"] == "" {
		t.Fatalf("asset URLs missing: %+v", res.Assets)
	}
	// The issue body must carry the Artifacts section (developer-local paths) and
	// the ```kitsoki metadata block.
	for _, want := range []string{
		"## Artifacts",
		"These files are not uploaded to GitHub.",
		"Screenshot: `.artifacts/bug-reports/b1/screenshot.png` (screenshot)",
		"HAR (scrubbed): `.artifacts/bug-reports/b1/har.json`",
		"```kitsoki",
		"trace_ref: trace://x",
		"engine_version: 1.2.3",
		"engine_revision: abcdef1234567890",
		"engine_checksum_sha256: sha256:engine",
		"story_app_id: cloak-of-darkness",
		"story_app_version: 0.1.0",
		"story_checksum_sha256: sha256:story",
		`public_stories_json: [{"name":"bug"`,
		`"checksum_sha256":"sha256:public"`,
	} {
		if !strings.Contains(issueBody, want) {
			t.Errorf("issue body missing %q", want)
		}
	}
	gotLabels := strings.Join(anyStrings(labels), ",")
	for _, want := range []string{"P2", "comp:web", "target:kitsoki"} {
		if !strings.Contains(gotLabels, want) {
			t.Errorf("issue labels missing %q: %v", want, labels)
		}
	}
}

// TestGitHubFileBug_NoEvidence skips the release path entirely (text-only file).
func TestGitHubFileBug_NoEvidence(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var touchedRelease bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases") {
			touchedRelease = true
		}
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		writeJSON(w, map[string]any{"number": 9, "html_url": "https://github.com/o/r/issues/9"})
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()

	res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
		Repo: "o/r", Title: "text only", Body: "no evidence", Severity: "P3",
	})
	if err != nil {
		t.Fatalf("GitHubFileBug: %v", err)
	}
	if touchedRelease {
		t.Fatal("no evidence → must not touch the release path")
	}
	if res.Number != "9" {
		t.Fatalf("number: %q", res.Number)
	}
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		panic(fmt.Sprintf("write json: %v", err))
	}
}
