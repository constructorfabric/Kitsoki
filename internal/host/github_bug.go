// github_bug.go — slice #2 orchestration: file a kitsoki bug as a GitHub issue
// with references to captured developer-local evidence.
//
// `gh` (and a PAT) cannot attach binaries to an issue the way the web UI does —
// that path needs an authenticated github.com web session. Kitsoki treats
// browser-captured evidence as developer-local debugging material: callers save
// it under `.artifacts/` before filing, and the issue body records those paths.
//
// This reuses the slice-#1 create op (labels + the ```kitsoki metadata block)
// and the one cliExec seam, so it's testable with a stubbed runner (no real gh).
package host

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ghArtifactsReleaseTag is the dedicated, idempotent GitHub Release used as the
// durable home for bug-evidence assets. `gh`+PAT cannot attach binaries to an
// issue (that needs a github.com web session), but it CAN upload release assets,
// whose download URLs are stable and viewable by any reviewer.
const ghArtifactsReleaseTag = "kitsoki-artifacts"

// EvidenceFile is one developer-local artifact referenced from a GitHub-filed
// bug. The local file must already be written by the caller; Path is rendered
// into the issue body as a developer-local reference.
type EvidenceFile struct {
	Name  string // evidence name; also the body label default
	Path  string // developer-local path/reference to the saved artifact
	Image bool   // true when the artifact is a screenshot/image
	Label string // human label in the body (defaults to Name)
}

// GitHubBugFiling is the input to GitHubFileBug.
type GitHubBugFiling struct {
	Repo                          string
	Title, Body                   string
	Severity, Component, Target   string
	TraceRef, KitsokiRev, FiledBy string
	Evidence                      []EvidenceFile

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
	if !ghAvailable(ctx) {
		return GitHubBugResult{}, fmt.Errorf("gh CLI not available — install github.com/cli/cli and run `gh auth login`")
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

	res, err := ghTicketCreate(ctx, map[string]any{
		"repo":        in.Repo,
		"title":       in.Title,
		"body":        body,
		"severity":    in.Severity,
		"component":   in.Component,
		"target":      in.Target,
		"trace_ref":   in.TraceRef,
		"kitsoki_rev": in.KitsokiRev,
		"filed_by":    in.FiledBy,
	})
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
// issueRef) and returns a name→public-URL map. It shells through the SAME
// cliExec seam ghTicketCreate uses, so it is unit-testable with a stubbed
// runner. The public download URL is
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
		src := strings.TrimSpace(f.Path)
		if src == "" {
			continue
		}
		assetName := ghAssetName(issueRef, f.Name)
		// gh release upload uses the file's basename as the asset name. Stage a
		// copy under the namespaced name so the public URL is collision-free.
		staged, cleanup, err := ghStageAsset(src, assetName)
		if err != nil {
			return nil, fmt.Errorf("upload evidence: stage %s: %w", f.Name, err)
		}
		_, stderr, code, err := cliExec(ctx, "", "gh",
			"release", "upload", tag, staged, "--clobber", "--repo", repo)
		cleanup()
		if err != nil {
			return nil, fmt.Errorf("upload evidence: exec: %w", err)
		}
		if code != 0 {
			return nil, fmt.Errorf("upload evidence: %s", strings.TrimSpace(stderr))
		}
		out[f.Name] = fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, assetName)
	}
	return out, nil
}

// ghEnsureRelease makes the dedicated artifacts release idempotent: view it,
// and create it (with a clear title/notes) only when it's missing.
func ghEnsureRelease(ctx context.Context, repo, tag string) error {
	_, _, code, err := cliExec(ctx, "", "gh", "release", "view", tag, "--repo", repo)
	if err != nil {
		return fmt.Errorf("ensure release: exec: %w", err)
	}
	if code == 0 {
		return nil
	}
	_, stderr, code, err := cliExec(ctx, "", "gh", "release", "create", tag,
		"--repo", repo,
		"--title", "kitsoki bug artifacts",
		"--notes", "Evidence assets uploaded by kitsoki bug filing. Linked from individual issues.")
	if err != nil {
		return fmt.Errorf("ensure release: exec: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("ensure release: %s", strings.TrimSpace(stderr))
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

// ghStageAsset copies src to a temp file named assetName so `gh release upload`
// (which keys the asset on the file's basename) yields the namespaced URL.
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
