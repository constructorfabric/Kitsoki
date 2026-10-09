// Package bugreport contains the shared, LLM-free core used by Kitsoki's
// bug-reporting surfaces.
package bugreport

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"kitsoki/internal/bugprivacy"
	"kitsoki/internal/host"
	"kitsoki/internal/runstatus/harscrub"
)

// ScrubOptions is the single production redaction config for filed bug
// evidence: $HOME path substitution plus the built-in credential patterns.
func ScrubOptions() harscrub.ScrubOptions {
	return harscrub.ScrubOptions{
		Home:           os.Getenv("HOME"),
		SecretPatterns: harscrub.DefaultSecretPatterns(),
	}
}

// ScrubText applies the production bug-report scrubber to prose or metadata.
func ScrubText(s string) string {
	return harscrub.ScrubString(s, ScrubOptions())
}

// Artifact is one evidence file to persist beside a bug report or upload with a
// GitHub-filed report. Empty Data means the artifact is absent.
type Artifact struct {
	Name  string
	Data  []byte
	Image bool
	Label string
}

// WriteArtifacts writes every non-empty artifact to dir.
func WriteArtifacts(dir string, artifacts []Artifact) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir artifacts: %w", err)
	}
	for _, artifact := range artifacts {
		if len(artifact.Data) == 0 || strings.TrimSpace(artifact.Name) == "" {
			continue
		}
		path := filepath.Join(dir, artifact.Name)
		if err := os.WriteFile(path, artifact.Data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", artifact.Name, err)
		}
	}
	return nil
}

// ArtifactNames returns the names of present artifacts for privacy-gate
// reporting. The caller order is preserved.
func ArtifactNames(artifacts []Artifact) []string {
	out := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if len(artifact.Data) > 0 && strings.TrimSpace(artifact.Name) != "" {
			out = append(out, artifact.Name)
		}
	}
	return out
}

// HasArtifact reports whether a named artifact has non-empty data.
func HasArtifact(artifacts []Artifact, name string) bool {
	for _, artifact := range artifacts {
		if artifact.Name == name && len(artifact.Data) > 0 {
			return true
		}
	}
	return false
}

// EvidenceFiles maps written artifacts into host.GitHubFileBug evidence records.
// displayRoot is the path rendered in issue bodies; dir is the real source dir.
func EvidenceFiles(dir, displayRoot string, artifacts []Artifact) []host.EvidenceFile {
	var out []host.EvidenceFile
	for _, artifact := range artifacts {
		if len(artifact.Data) == 0 || strings.TrimSpace(artifact.Name) == "" {
			continue
		}
		out = append(out, host.EvidenceFile{
			Name:       artifact.Name,
			Path:       filepath.ToSlash(filepath.Join(displayRoot, artifact.Name)),
			SourcePath: filepath.Join(dir, artifact.Name),
			Image:      artifact.Image,
			Label:      artifact.Label,
		})
	}
	return out
}

// PrivacyFollowUpSuffix renders the standard depersonalized-follow-up pointer.
func PrivacyFollowUpSuffix(privacy bugprivacy.Result) string {
	if strings.TrimSpace(privacy.FollowUpPath) == "" {
		return ""
	}
	return "; depersonalized follow-up filed at " + filepath.ToSlash(privacy.FollowUpPath)
}

// PrivacyCommandStatus renders the short status used by interactive commands.
func PrivacyCommandStatus(privacy bugprivacy.Result) string {
	msg := strings.TrimSpace(privacy.Message)
	if msg == "" {
		msg = "privacy check passed"
	}
	return "privacy check started; " + msg + PrivacyFollowUpSuffix(privacy)
}

// GitShortRev returns the short HEAD sha of the repo containing dir
// (best-effort; "" when dir is empty / not a repo / git is unavailable).
func GitShortRev(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
