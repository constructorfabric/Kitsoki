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
	"fmt"
	"strings"
)

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
		for _, f := range in.Evidence {
			assets[f.Name] = f.Path
		}
		body += ghArtifactsSection(in.Evidence)
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
