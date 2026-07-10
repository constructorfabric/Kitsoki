// github_bug.go — slice #2 orchestration: file a kitsoki bug as a GitHub issue
// with references to captured developer-local evidence.
//
// GitHub Issues cannot attach binaries directly to an issue. Kitsoki treats
// browser-captured evidence as review material: callers save it under
// `.artifacts/` before filing, and online filing can upload those files as
// release assets whose URLs are linked from the issue body.
//
// This reuses the slice-#1 create op (labels + the ```kitsoki metadata block).
// Tests inject a fake GitHub HTTP API so no real network is touched.
package host

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"kitsoki/internal/reportmeta"
)

// ghArtifactsReleaseTag is the dedicated, idempotent GitHub Release used as the
// durable home for bug-evidence assets. Release asset URLs are stable and
// viewable by reviewers with access to the repo.
const ghArtifactsReleaseTag = "kitsoki-artifacts"

// EvidenceFile is one developer-local artifact referenced from a GitHub-filed
// bug. The local file must already be written by the caller; Path is rendered
// into the issue body as a developer-local reference.
type EvidenceFile struct {
	Name string // evidence name; also the body label default
	Path string // developer-local path/reference rendered into the body
	// SourcePath is the actual on-disk file to read when uploading the artifact
	// as a release asset. When empty, Path is used (back-compat for callers whose
	// Path is itself a readable path). Set this when Path is a display-only
	// reference that does not resolve from the process cwd.
	SourcePath string
	Image      bool   // true when the artifact is a screenshot/image
	Label      string // human label in the body (defaults to Name)
}

// GitHubBugFiling is the input to GitHubFileBug.
type GitHubBugFiling struct {
	Repo                          string
	Title, Body                   string
	Severity, Component, Target   string
	TraceRef, KitsokiRev, FiledBy string
	Evidence                      []EvidenceFile
	Runtime                       reportmeta.Snapshot

	// UploadArtifacts, when true, uploads each evidence file as a GitHub Release
	// asset (on the ghArtifactsReleaseTag release) and links the public asset
	// URLs in the issue body so a reviewer on github.com can open the evidence.
	// Default false: local/replay/no-network modes never touch the network and
	// fall back to rendering developer-local paths. Upload failures also fall
	// back gracefully (the issue is still filed with local-path references).
	UploadArtifacts bool
}

// GitHubBugResult is what GitHubFileBug returns.
type GitHubBugResult struct {
	URL    string            // the new issue's URL
	Number string            // the new issue's number
	Assets map[string]string // evidence name → developer-local path
}

// GitHubFileBug builds the issue body (prose + an Artifacts section + the create
// op's ```kitsoki metadata block) and creates the issue. It is the single
// orchestration the web Report-bug RPC and CLI call to file a kitsoki bug on
// GitHub.
func GitHubFileBug(ctx context.Context, in GitHubBugFiling) (GitHubBugResult, error) {
	if strings.TrimSpace(in.Repo) == "" {
		return GitHubBugResult{}, fmt.Errorf("github bug: repo is required for native GitHub issue filing")
	}
	if in.Runtime.Empty() {
		in.Runtime = reportmeta.Capture("", nil)
	}
	if strings.TrimSpace(in.KitsokiRev) == "" {
		in.KitsokiRev = in.Runtime.Engine.RevisionShort
	}

	body := in.Body
	assets := map[string]string{}
	if len(in.Evidence) > 0 {
		// Default: developer-local path references.
		for _, f := range in.Evidence {
			assets[f.Name] = f.Path
		}
		section := ghArtifactsSection(in.Evidence)

		// Opt-in: upload as Release assets and link the public URLs instead.
		// On any failure, fall back to the local-path section (never fail the
		// whole filing for an evidence-upload problem).
		if in.UploadArtifacts {
			prefix := ghArtifactPrefix(in)
			if urls, err := ghUploadEvidence(ctx, in.Repo, ghArtifactsReleaseTag, prefix, in.Evidence); err == nil && len(urls) > 0 {
				for name, url := range urls {
					assets[name] = url
				}
				section = ghArtifactsSectionUploaded(in.Evidence, urls)
			}
		}
		body += section
	}

	args := map[string]any{
		"repo":        in.Repo,
		"title":       in.Title,
		"body":        body,
		"severity":    in.Severity,
		"component":   in.Component,
		"target":      in.Target,
		"trace_ref":   in.TraceRef,
		"kitsoki_rev": in.KitsokiRev,
		"filed_by":    in.FiledBy,
	}
	for _, f := range in.Runtime.Fields() {
		args[f.Key] = f.Value
	}
	res, err := ghTicketCreate(ctx, args)
	if err != nil {
		return GitHubBugResult{}, err
	}
	if res.Error != "" {
		return GitHubBugResult{}, fmt.Errorf("%s", res.Error)
	}
	url, _ := res.Data["url"].(string)
	num, _ := res.Data["id"].(string)
	return GitHubBugResult{URL: url, Number: num, Assets: assets}, nil
}

// ghArtifactsSection renders the "## Artifacts" body block: the screenshot
// identified as an image, the rest listed as developer-local paths.
func ghArtifactsSection(files []EvidenceFile) string {
	var sb strings.Builder
	sb.WriteString("\n\n## Artifacts\n\n")
	sb.WriteString("_Captured in the browser, scrubbed server-side, and saved locally for developer review. These files are not uploaded to GitHub._\n\n")
	for _, f := range files {
		path := strings.TrimSpace(f.Path)
		if path == "" {
			continue
		}
		label := f.Label
		if label == "" {
			label = f.Name
		}
		if f.Image {
			fmt.Fprintf(&sb, "- %s: `%s` (screenshot)\n", label, path)
		} else {
			fmt.Fprintf(&sb, "- %s: `%s`\n", label, path)
		}
	}
	return sb.String()
}

// ghArtifactsSectionUploaded renders the "## Artifacts" block with the
// release-asset URLs returned by ghUploadEvidence: image evidence as an inline
// `![label](url)` so it renders in the issue, everything else as a plain link.
// Any file without an uploaded URL degrades to its developer-local path.
func ghArtifactsSectionUploaded(files []EvidenceFile, urls map[string]string) string {
	var sb strings.Builder
	sb.WriteString("\n\n## Artifacts\n\n")
	sb.WriteString("_Captured in the browser, scrubbed server-side, and uploaded as GitHub release assets so they can be reviewed here._\n\n")
	for _, f := range files {
		label := f.Label
		if label == "" {
			label = f.Name
		}
		url := strings.TrimSpace(urls[f.Name])
		if url == "" {
			// Upload missed this file — fall back to its local path.
			path := strings.TrimSpace(f.Path)
			if path == "" {
				continue
			}
			fmt.Fprintf(&sb, "- %s: `%s`\n", label, path)
			continue
		}
		if f.Image {
			fmt.Fprintf(&sb, "- %s: ![%s](%s)\n", label, label, url)
		} else {
			fmt.Fprintf(&sb, "- %s: [%s](%s)\n", label, label, url)
		}
	}
	return sb.String()
}

// ghArtifactPrefix derives a short, stable filename prefix that namespaces a
// filing's uploaded assets inside the shared release (so two bugs that both
// capture "screenshot.png" don't clobber each other). Keyed on the trace ref
// (or title when absent) so a re-file of the same bug overwrites cleanly.
func ghArtifactPrefix(in GitHubBugFiling) string {
	seed := strings.TrimSpace(in.TraceRef)
	if seed == "" {
		seed = strings.TrimSpace(in.Title)
	}
	sum := sha1.Sum([]byte(seed))
	return fmt.Sprintf("%x", sum[:4])
}

// ghUploadEvidence ensures the dedicated artifacts release exists on the repo,
// then uploads each evidence file as a release asset (collision-namespaced by
// issueRef) and returns a name→public-URL map. The public download URL is
// https://github.com/<repo>/releases/download/<tag>/<filename>.
func ghUploadEvidence(ctx context.Context, repo, tag, issueRef string, files []EvidenceFile) (map[string]string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil, fmt.Errorf("upload evidence: repo is required")
	}
	if err := ghEnsureRelease(ctx, repo, tag); err != nil {
		return nil, err
	}

	out := map[string]string{}
	for _, f := range files {
		// Read from SourcePath (the real on-disk file) when set; otherwise Path is
		// itself a readable path.
		src := strings.TrimSpace(f.SourcePath)
		if src == "" {
			src = strings.TrimSpace(f.Path)
		}
		if src == "" {
			continue
		}
		assetName := ghAssetName(issueRef, f.Name)
		// GitHub release assets use the file's basename as the asset name. Stage
		// a copy under the namespaced name so the public URL is collision-free.
		staged, cleanup, err := ghStageAsset(src, assetName)
		if err != nil {
			return nil, fmt.Errorf("upload evidence: stage %s: %w", f.Name, err)
		}
		release, err := ghEnsureReleaseInfo(ctx, repo, tag)
		if err != nil {
			cleanup()
			return nil, err
		}
		code, resp, err := githubUploadReleaseAsset(ctx, repo, release, assetName, staged)
		cleanup()
		if err != nil {
			return nil, fmt.Errorf("upload evidence: %w", err)
		}
		if code >= 300 {
			return nil, fmt.Errorf("upload evidence: %s", githubAPIError(resp))
		}
		out[f.Name] = fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, assetName)
	}
	return out, nil
}

// ghEnsureRelease makes the dedicated artifacts release idempotent: view it,
// and create it (with a clear title/notes) only when it's missing.
func ghEnsureRelease(ctx context.Context, repo, tag string) error {
	_, err := ghEnsureReleaseInfo(ctx, repo, tag)
	return err
}

type ghReleaseInfo struct {
	ID        int64  `json:"id"`
	UploadURL string `json:"upload_url"`
}

func ghEnsureReleaseInfo(ctx context.Context, repo, tag string) (ghReleaseInfo, error) {
	var release ghReleaseInfo
	code, resp, err := githubAPIJSON(ctx, http.MethodGet, "repos/"+repo+"/releases/tags/"+tag, nil, &release)
	if err != nil {
		return ghReleaseInfo{}, fmt.Errorf("ensure release: %w", err)
	}
	if code == http.StatusNotFound {
		payload := map[string]any{
			"tag_name":   tag,
			"name":       "kitsoki bug artifacts",
			"body":       "Evidence assets uploaded by kitsoki bug filing. Linked from individual issues.",
			"prerelease": true,
		}
		code, resp, err = githubAPIJSON(ctx, http.MethodPost, "repos/"+repo+"/releases", payload, &release)
		if err != nil {
			return ghReleaseInfo{}, fmt.Errorf("ensure release: %w", err)
		}
	}
	if code >= 300 {
		return ghReleaseInfo{}, fmt.Errorf("ensure release: %s", githubAPIError(resp))
	}
	if strings.TrimSpace(release.UploadURL) == "" {
		return ghReleaseInfo{}, fmt.Errorf("ensure release: GitHub response missing upload_url")
	}
	return release, nil
}

func githubUploadReleaseAsset(ctx context.Context, repo string, release ghReleaseInfo, assetName, path string) (int, string, error) {
	uploadURL := strings.Split(strings.TrimSpace(release.UploadURL), "{")[0] + "?name=" + url.QueryEscape(assetName)
	code, resp, err := githubUploadFileOnce(ctx, uploadURL, path)
	if code != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(resp), "already_exists") {
		return code, resp, err
	}
	if release.ID == 0 {
		return code, resp, err
	}
	if err := githubDeleteReleaseAsset(ctx, repo, release.ID, assetName); err != nil {
		return code, resp, err
	}
	return githubUploadFileOnce(ctx, uploadURL, path)
}

func githubUploadFileOnce(ctx context.Context, uploadURL, path string) (int, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", fmt.Errorf("open staged asset: %w", err)
	}
	defer file.Close()
	return githubAPIRequest(ctx, http.MethodPost, uploadURL, "application/octet-stream", file, nil)
}

func githubDeleteReleaseAsset(ctx context.Context, repo string, releaseID int64, assetName string) error {
	var assets []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	code, resp, err := githubAPIJSON(ctx, http.MethodGet, fmt.Sprintf("repos/%s/releases/%d/assets?per_page=100", repo, releaseID), nil, &assets)
	if err != nil {
		return err
	}
	if code >= 300 {
		return fmt.Errorf("list release assets: %s", githubAPIError(resp))
	}
	for _, asset := range assets {
		if asset.Name != assetName {
			continue
		}
		code, resp, err = githubAPIJSON(ctx, http.MethodDelete, fmt.Sprintf("repos/%s/releases/assets/%d", repo, asset.ID), nil, nil)
		if err != nil {
			return err
		}
		if code >= 300 {
			return fmt.Errorf("delete release asset: %s", githubAPIError(resp))
		}
		return nil
	}
	return nil
}

// ghAssetName namespaces an evidence filename with a per-filing prefix so the
// shared release never collides across bugs.
func ghAssetName(prefix, name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "_")
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return name
	}
	return prefix + "-" + name
}

// ghStageAsset copies src to a temp file named assetName so the release asset
// upload (which keys the asset on the file's basename) yields the namespaced URL.
// Returns the staged path and a cleanup func.
func ghStageAsset(src, assetName string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "kitsoki-evidence-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	in, err := os.Open(src)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	defer in.Close()
	dst := filepath.Join(dir, assetName)
	out, err := os.Create(dst)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := out.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return dst, cleanup, nil
}
