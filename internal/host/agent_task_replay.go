// Package host — replay artifact capture for host.agent.task.
//
// Three sub-cases:
//
//	Mode A (file_diff)          — agent tools limited to Read/Edit/Write/Bash;
//	                              no WebFetch/WebSearch/non-read_only MCP.
//	                              Replay: (initial_state_hash, final_diff).
//
//	Mode B (sandboxed_write)    — agent uses sandboxed-write Bash; scratch dir
//	                              contents captured as a tarball in the journal.
//	                              Replay: same plus scratch tarball.
//
//	Mode C (external_side_effect) — unrestricted Bash/WebFetch/non-read_only MCP.
//	                              Recorded only; not replayable.
//
// The replay mode is labelled on the task.end journal event so eval tooling
// can filter. Replay tooling skips Mode C spans by default and surfaces a
// "skipped N external-side-effect spans" summary.
//
// M4: read-tool outputs from the LLM's tool calls are capped via
// capReadToolOutput applied in observeTaskToolCalls (agent_task_transport.go)
// when emitting task.tool journal events. Large Read/Grep/Glob results are
// stored as a sha256 hash + 4 KiB prefix so the journal stays bounded.
package host

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"kitsoki/internal/effect"
)

// scratchTarballCap is the maximum in-memory size of a Mode B scratch-dir
// tarball. When the tarball exceeds this size the journal records a placeholder
// instead of the full bytes so the process doesn't OOM on large scratch dirs.
const scratchTarballCap = 32 * 1024 * 1024 // 32 MiB

// hashDirSkipNames is the set of directory names skipped by hashDirectory so
// that volatile metadata (git index, node_modules, kitsoki worktrees) doesn't
// produce unstable hashes.
var hashDirSkipNames = map[string]bool{
	".git":         true,
	".kitsoki":     true,
	"node_modules": true,
	".worktrees":   true,
}

// ReplayMode classifies a task call for replay purposes.
type ReplayMode string

const (
	// ReplayModeFileDiff indicates Mode A: only file-system mutations.
	// Replay is deterministic from (initial_state_hash, final_diff).
	ReplayModeFileDiff ReplayMode = "file_diff"

	// ReplayModeSandboxedWrite indicates Mode B: sandboxed-write Bash.
	// Replay requires the diff plus the scratch-dir tarball.
	ReplayModeSandboxedWrite ReplayMode = "sandboxed_write"

	// ReplayModeExternalSideEffect indicates Mode C: external side effects.
	// Recorded only; not replayable.
	ReplayModeExternalSideEffect ReplayMode = "external_side_effect"
)

// inferReplayMode determines the replay mode for a task call from the
// agent's effect classification (effect-taxonomy.md) and its declared tool
// surface.
//
// Precedence for the effect class: agent.Effect when the loader populated it
// (see the Agent.Effect field doc in agents.go); otherwise the deprecated
// agent.ExternalSideEffect mapped through effect.FromLegacyBool against
// tools; otherwise effect.FromTools(tools) — infer directly from the
// call-site tool list when the agent carries no classification at all.
//
// A class of effect.External yields ReplayModeExternalSideEffect. Otherwise,
// if the agent's BashProfile suggests sandboxed-write Bash, it is
// ReplayModeSandboxedWrite. Otherwise ReplayModeFileDiff.
//
// The caller is responsible for resolving the effective tool list before
// calling this function.
func inferReplayMode(agent Agent, tools []string) ReplayMode {
	var class effect.Effect
	switch {
	case agent.Effect != "" && agent.Effect.Valid():
		class = agent.Effect
	case agent.ExternalSideEffect != nil:
		// Explicit declaration wins — FromLegacyBool never reclassifies a
		// false declaration to external based on tool inference (e.g.
		// WebFetch/WebSearch in tools). This is the runtime safety-net
		// behind the loader's hard-fail for agents that declare
		// external_side_effect: false with a network tool in their surface.
		class = effect.FromLegacyBool(*agent.ExternalSideEffect, tools)
	default:
		class = effect.FromTools(tools)
	}
	if class == effect.External {
		return ReplayModeExternalSideEffect
	}
	// Mode B: agent has sandboxed-write BashProfile.
	if agent.BashProfile != nil && agent.BashProfile.Kind == BashProfileSandboxWrite {
		return ReplayModeSandboxedWrite
	}
	return ReplayModeFileDiff
}

// captureInitialStateHash records a stable hash of workingDir at task start.
// If workingDir is a git repository, the hash is the HEAD commit SHA
// (returned as "git:<sha>"). For non-git directories, a recursive SHA-256
// of file contents and paths is computed (returned as "tree:<sha>").
//
// The hash is recorded in the task.end event so replay can verify that the
// starting state matches before applying the diff.
func captureInitialStateHash(ctx context.Context, workingDir string) string {
	if workingDir == "" {
		return ""
	}
	// Try git first.
	cmd := exec.CommandContext(ctx, "git", "-C", workingDir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err == nil {
		sha := strings.TrimSpace(string(out))
		if sha != "" {
			return "git:" + sha
		}
	}
	// Fallback: recursive tree hash of the directory.
	hash, hashErr := hashDirectory(workingDir)
	if hashErr != nil {
		slog.WarnContext(ctx, "task.replay: hash directory failed; initial_state_hash empty",
			"dir", workingDir, "err", hashErr)
		return ""
	}
	return "tree:" + hash
}

// hashDirectory computes a deterministic SHA-256 over all regular files under
// dir. The hash input is: for each file in sorted relative-path order,
// "<relpath>\n<content>". Symlinks are followed for content; directories are
// traversed but not hashed individually.
//
// Skips directories named in hashDirSkipNames (.git, .kitsoki, node_modules,
// .worktrees) so that volatile metadata doesn't produce unstable hashes.
// Also skips files modified within the last second to avoid races with mtime.
//
// This is the fallback for non-git working directories. The hash is NOT a git
// tree-object hash; it is a kitsoki-specific content fingerprint that suffices
// to detect whether the working tree changed between task start and replay.
func hashDirectory(dir string) (string, error) {
	h := sha256.New()
	var paths []string
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if hashDirSkipNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, rel)
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	sort.Strings(paths)
	for _, rel := range paths {
		data, readErr := os.ReadFile(filepath.Join(dir, rel))
		if readErr != nil {
			return "", fmt.Errorf("read %s: %w", rel, readErr)
		}
		fmt.Fprintf(h, "%s\n", rel)
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// intentToAddUntracked runs `git add -N` on every untracked file in workingDir
// so they show up in `git diff HEAD` as "added" hunks. `git add -N` (intent-to-add)
// records the path in the index without staging content, leaving the working tree
// unchanged. Returns the list of paths that were intent-added (used for cleanup).
func intentToAddUntracked(ctx context.Context, workingDir string) []string {
	// List untracked files (not ignored).
	lsCmd := exec.CommandContext(ctx, "git", "-C", workingDir, "ls-files",
		"--others", "--exclude-standard")
	out, err := lsCmd.Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	addArgs := append([]string{"-C", workingDir, "add", "-N", "--"}, paths...)
	addCmd := exec.CommandContext(ctx, "git", addArgs...)
	if err := addCmd.Run(); err != nil {
		return nil
	}
	return paths
}

// resetIntentAdded removes `git add -N` index entries for the given paths,
// restoring them to untracked status. Called via defer after diff capture so
// intent-to-add entries don't persist across task retries or into subsequent
// rooms when the task exhausts retries without committing.
func resetIntentAdded(ctx context.Context, workingDir string, paths []string) {
	if len(paths) == 0 {
		return
	}
	resetArgs := append([]string{"-C", workingDir, "reset", "HEAD", "--"}, paths...)
	_ = exec.CommandContext(ctx, "git", resetArgs...).Run()
}

// captureFinalDiff computes a unified diff of changes in workingDir since the
// initial state captured by captureInitialStateHash. For git trees, this uses
// "git diff --no-color HEAD". Untracked files are staged with `git add -N`
// (intent-to-add) before the diff so newly created files appear as "added"
// hunks. The intent-to-add entries are removed after the diff is captured so
// they do not persist in the index after this call returns.
//
// The diff is stored in the task.end journal event under "final_diff". It is
// NOT bound into world (too large); replay reads it directly from the journal.
func captureFinalDiff(ctx context.Context, workingDir string) string {
	if workingDir == "" {
		return ""
	}
	// H4: include untracked files; clean up intent-to-add entries afterwards.
	added := intentToAddUntracked(ctx, workingDir)
	defer resetIntentAdded(ctx, workingDir, added)
	cmd := exec.CommandContext(ctx, "git", "-C", workingDir, "diff", "--no-color", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		// Not a git repo or no diff available.
		return ""
	}
	// NFC-normalise: the diff is free CONTENT (journaled under final_diff, shown
	// for review), and on macOS it can sweep in filesystem-derived text in NFD
	// form (an unrelated untracked file with composed-vs-decomposed unicode).
	// The journal store hard-REJECTS NFD payloads (store/jsonl: caller must
	// normalise to NFC), so an un-normalised diff fails the whole turn's append
	// ("payload string contains NFD-normalised unicode"). The diff is display
	// text, not a path-match key, so normalising is lossless and safe.
	return norm.NFC.String(string(out))
}

// captureFilesChanged returns a sorted list of paths modified or newly added in
// workingDir since HEAD. For git trees, uses "git diff --name-only HEAD". Untracked
// files are included via intent-to-add (same as captureFinalDiff). Intent-to-add
// entries are removed after the diff is captured so they do not persist.
func captureFilesChanged(ctx context.Context, workingDir string) []string {
	if workingDir == "" {
		return nil
	}
	// H4: include untracked files; clean up intent-to-add entries afterwards.
	added := intentToAddUntracked(ctx, workingDir)
	defer resetIntentAdded(ctx, workingDir, added)
	cmd := exec.CommandContext(ctx, "git", "-C", workingDir, "diff", "--name-only", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	sort.Strings(files)
	return files
}

// dirUncompressedSize returns the total byte size of all regular files under
// dir. Used by tarballDirectory to pre-check the cap before compressing.
func dirUncompressedSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, fiErr := d.Info()
		if fiErr != nil {
			return fiErr
		}
		total += fi.Size()
		return nil
	})
	return total, err
}

// tarballDirectory compresses dir into a gzipped tar archive and returns the
// bytes. Used for Mode B (sandboxed-write) scratch-dir capture. Returns nil on
// an empty or missing directory (no-op).
//
// L4: the uncompressed scratch-dir size is capped at scratchTarballCap (32 MiB).
// When the total uncompressed file bytes exceed the cap, the function returns a
// text placeholder instead of binary bytes so the process doesn't OOM. The
// placeholder format is:
//
//	scratch_dir_tarball: omitted (size <N> bytes exceeds 32 MiB cap)
//
// Replay tooling treats this placeholder as "tarball omitted" and skips
// the scratch-dir restoration step.
func tarballDirectory(dir string) ([]byte, error) {
	info, statErr := os.Stat(dir)
	if os.IsNotExist(statErr) || (info != nil && !info.IsDir()) {
		return nil, nil
	}
	if statErr != nil {
		return nil, statErr
	}

	// L4: pre-check uncompressed size to avoid holding a 32 MiB buffer in RAM
	// for a directory that will just be discarded.
	uncompressedSize, sizeErr := dirUncompressedSize(dir)
	if sizeErr == nil && uncompressedSize > scratchTarballCap {
		placeholder := fmt.Sprintf("scratch_dir_tarball: omitted (size %d bytes exceeds %d MiB cap)",
			uncompressedSize, scratchTarballCap/(1024*1024))
		return []byte(placeholder), nil
	}

	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		gw := gzip.NewWriter(pw)
		tw := tar.NewWriter(gw)
		walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, path)
			if rel == "." {
				return nil
			}
			fi, fiErr := d.Info()
			if fiErr != nil {
				return fiErr
			}
			hdr, hdrErr := tar.FileInfoHeader(fi, "")
			if hdrErr != nil {
				return hdrErr
			}
			hdr.Name = rel
			if wErr := tw.WriteHeader(hdr); wErr != nil {
				return wErr
			}
			if !d.IsDir() {
				f, fErr := os.Open(path)
				if fErr != nil {
					return fErr
				}
				defer f.Close()
				_, cpErr := io.Copy(tw, f)
				return cpErr
			}
			return nil
		})
		if walkErr != nil {
			errCh <- walkErr
			return
		}
		tw.Close()
		gw.Close()
		errCh <- nil
	}()

	var rawBuf []byte
	readBuf := make([]byte, 32*1024)
	for {
		n, readErr := pr.Read(readBuf)
		if n > 0 {
			rawBuf = append(rawBuf, readBuf[:n]...)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if err := <-errCh; err != nil {
		return nil, err
	}
	return rawBuf, nil
}

// capReadToolOutput captures a read-tool output for the journal using the
// shared ReadSnapshotCap (decision D9). When the output is
// within the cap it is returned verbatim. When it exceeds the cap, a summary
// string "sha256:<hex> (first 4096 bytes follow)\n<prefix>" is returned so
// replay can detect divergent inputs by comparing the hash.
func capReadToolOutput(output string) string {
	if len(output) <= ReadSnapshotCap {
		return output
	}
	h := sha256.Sum256([]byte(output))
	prefix := output
	if len(prefix) > ReadSnapshotPrefix {
		prefix = prefix[:ReadSnapshotPrefix]
	}
	return fmt.Sprintf("sha256:%s (first 4096 bytes follow)\n%s", hex.EncodeToString(h[:]), prefix)
}
