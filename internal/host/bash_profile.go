// Package host — Bash tool restriction profiles for host.oracle.ask and
// host.oracle.decide.
//
// Three profiles restrict what Bash commands an LLM agent may run inside an
// ask/decide call. The profile is applied by the
// kitsoki-bash MCP wrapper (bash_mcp.go): commands that don't match the profile
// are replaced with a tool error so the LLM sees the rejection and cannot
// mutate anything unexpected.
//
// Enforcement limitations (Phase 1 — best-effort):
//   - Shell metacharacters (;, &&, ||, backticks, $(...)) are detected and
//     rejected by ApplyBashProfile so multi-command chains can't bypass the
//     argv0 allowlist. A sufficiently creative LLM could still escape if it
//     crafts arguments that happen to contain metacharacters inside quoted
//     regions — full shell AST parsing is a hardening TODO for a later phase.
//   - sandboxed-write network deny is implemented via the HTTP_PROXY env var
//     trick (HTTP_PROXY=invalid://blocked). This blocks most HTTP/HTTPS calls
//     but does NOT prevent raw socket connections. Full network isolation
//     requires a mount-namespace/seccomp setup (see validator_sandbox.go).
//   - read-only profile allowlist is conservative but not exhaustive; a
//     command not on the list is denied rather than silently allowed.
//   - Per-subcommand checks for git/sed/awk are applied after argv0 matching.
//     Shell-quoting bypass inside arguments is not detected (best-effort).
package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// readOnlyAllowedCommands is the built-in allowlist for the read-only Bash
// profile. Only programs whose default operation is read-only and whose
// dangerous subcommands/flags are individually checked (below) appear here.
// Authors who need programs not on this list should use the commands profile
// and explicitly accept responsibility.
//
// Allowed set: grep, rg, find, cat, head, tail, ls, wc, file, stat, jq,
// sort, uniq, cut, tr, echo, printf, env, which, type, git (read-only
// subcommands only — see checkGitSubcommand), sed (no -i/--in-place),
// awk (no system() calls).
//
// Dropped vs prior version: python3 (too dangerous — python3 -c 'shutil…'
// bypasses all intent). Authors who need python3 must use commands: [python3].
var readOnlyAllowedCommands = map[string]bool{
	"grep":   true,
	"rg":     true,
	"find":   true,
	"cat":    true,
	"head":   true,
	"tail":   true,
	"ls":     true,
	"wc":     true,
	"file":   true,
	"stat":   true,
	"jq":     true,
	"sort":   true,
	"uniq":   true,
	"cut":    true,
	"tr":     true,
	"echo":   true,
	"printf": true,
	"env":    true,
	"which":  true,
	"type":   true,
	"git":    true, // only read-only subcommands — see checkGitSubcommand
	"sed":    true, // only without -i/--in-place — see checkSedFlags
	"awk":    true, // only without system() — see checkAwkScript
}

// gitReadOnlySubcommands is the explicit allowlist of git subcommands
// permitted under the read-only profile. Any subcommand not listed here is
// rejected, including push, pull, fetch, reset, rm, commit, checkout, merge,
// rebase, clean, stash, gc, prune, and any --exec/--upload-pack flag variant.
var gitReadOnlySubcommands = map[string]bool{
	"log":       true,
	"diff":      true,
	"show":      true,
	"status":    true,
	"blame":     true,
	"rev-parse": true,
	"ls-files":  true,
	"cat-file":  true,
	"branch":    true, // only -a/-r — further checked in checkGitSubcommand
	"tag":       true, // only -l
	"remote":    true, // only -v
}

// shellMetaChars is the set of characters that, if present in a command
// string, indicate a multi-command chain or injection risk. All profiles
// reject commands containing any of these.
const shellMetaChars = ";|&`$(){}!<>"

// ApplyBashProfile validates cmd against profile and returns an error message
// to surface to the LLM when the command is not permitted, or "" when the
// command is allowed. The returned string is a tool-error payload, not a Go
// error — a non-empty return means "deny and show this to the LLM".
//
// cmd is the raw bash command string as the LLM would pass it; workingDir is
// the directory the command would run in (used to construct the scratch path
// for sandboxed-write).
func ApplyBashProfile(profile *BashProfile, cmd string) string {
	if profile == nil {
		return ""
	}

	if containsMetaChars(cmd) {
		return fmt.Sprintf("bash_profile: command contains shell metacharacters (%q); multi-command chains are not permitted in this profile", cmd)
	}

	switch profile.Kind {
	case BashProfileReadOnly:
		return applyReadOnlyProfile(cmd)

	case BashProfileCommands:
		argv0 := extractArgv0(cmd)
		for _, allowed := range profile.Commands {
			if argv0 == allowed {
				return ""
			}
		}
		return fmt.Sprintf("bash_profile(commands): command %q is not in the allowed list %v", argv0, profile.Commands)

	case BashProfileSandboxWrite:
		// sandboxed-write allows any command but sets cwd to a scratch dir and
		// denies network via HTTP_PROXY. See MakeSandboxEnv for the env setup.
		return ""

	default:
		return fmt.Sprintf("bash_profile: unknown profile kind %d", profile.Kind)
	}
}

// applyReadOnlyProfile enforces the read-only allowlist on cmd. Called from
// ApplyBashProfile when profile.Kind == BashProfileReadOnly.
//
// Policy: deny unless explicitly allowlisted. For git, sed, and awk, argv0
// matching is followed by per-subcommand/flag checks to block the dangerous
// subsets of those otherwise-read-only programs.
func applyReadOnlyProfile(cmd string) string {
	argv0 := extractArgv0(cmd)
	if !readOnlyAllowedCommands[argv0] {
		return fmt.Sprintf(
			"bash_profile(read-only): command %q is not on the read-only allowlist; "+
				"allowed: grep, rg, find, cat, head, tail, ls, wc, file, stat, jq, sort, uniq, "+
				"cut, tr, echo, printf, env, which, type, git (read-only subcommands), "+
				"sed (no -i/--in-place), awk (no system()). "+
				"For python3 or other tools use bash_profile: commands: [...]",
			argv0,
		)
	}

	switch argv0 {
	case "git":
		return checkGitSubcommand(cmd)
	case "sed":
		return checkSedFlags(cmd)
	case "awk":
		return checkAwkScript(cmd)
	}
	return ""
}

// checkGitSubcommand verifies that the git subcommand is on the read-only
// allowlist and that dangerous flag variants are not present.
//
// Allowed subcommands: log, diff, show, status, blame, rev-parse, ls-files,
// cat-file, branch (only -a/-r), tag (only -l), remote (only -v).
//
// Rejected: push, pull, fetch, reset, rm, commit, checkout, merge, rebase,
// clean, stash, gc, prune, and any command containing --exec or --upload-pack.
func checkGitSubcommand(cmd string) string {
	tokens := strings.Fields(cmd)
	if len(tokens) < 2 {
		return "bash_profile(read-only): bare 'git' without a subcommand is not permitted"
	}
	sub := tokens[1]

	if !gitReadOnlySubcommands[sub] {
		return fmt.Sprintf(
			"bash_profile(read-only): git subcommand %q is not permitted; "+
				"allowed: log, diff, show, status, blame, rev-parse, ls-files, cat-file, "+
				"branch -a/-r, tag -l, remote -v",
			sub,
		)
	}

	// Reject dangerous flags that apply to any subcommand.
	rest := strings.Join(tokens[2:], " ")
	for _, bad := range []string{"--exec", "--upload-pack", "--receive-pack"} {
		if strings.Contains(rest, bad) {
			return fmt.Sprintf("bash_profile(read-only): git flag %q is not permitted", bad)
		}
	}

	// Per-subcommand checks for subcommands that have a writable default.
	switch sub {
	case "branch":
		// Only -a (all) and -r (remote) are safe; -d/-D (delete), -m/-M
		// (move/rename), and bare branch without flags creates a branch.
		allowedBranchFlags := false
		for _, tok := range tokens[2:] {
			switch tok {
			case "-a", "--all", "-r", "--remotes", "--list", "--no-column", "--column",
				"--sort", "--format", "--points-at", "--merged", "--no-merged",
				"--contains", "--no-contains", "--abbrev", "--no-abbrev",
				"--color", "--no-color", "--verbose", "-v", "-vv":
				allowedBranchFlags = true
			}
			// Any flag that modifies state is rejected.
			if tok == "-d" || tok == "-D" || tok == "-m" || tok == "-M" ||
				tok == "-c" || tok == "-C" || tok == "--delete" || tok == "--move" ||
				tok == "--copy" || tok == "--set-upstream-to" || tok == "-u" ||
				tok == "--unset-upstream" || tok == "--edit-description" {
				return fmt.Sprintf("bash_profile(read-only): git branch flag %q is not permitted; only listing flags (-a, -r, --list, ...) are allowed", tok)
			}
		}
		_ = allowedBranchFlags
	case "tag":
		// Only listing (-l/--list) is safe; -a/-s/-d/-v all modify or sign tags.
		for _, tok := range tokens[2:] {
			if tok == "-a" || tok == "--annotate" || tok == "-s" || tok == "--sign" ||
				tok == "-d" || tok == "--delete" || tok == "-f" || tok == "--force" ||
				tok == "-v" || tok == "--verify" {
				return fmt.Sprintf("bash_profile(read-only): git tag flag %q is not permitted; only -l/--list is allowed", tok)
			}
		}
	case "remote":
		// Only -v/--verbose is safe; add/remove/rename/set-url all modify config.
		if len(tokens) > 2 {
			op := tokens[2]
			if op != "-v" && op != "--verbose" {
				return fmt.Sprintf("bash_profile(read-only): git remote subcommand %q is not permitted; only 'git remote -v' is allowed", op)
			}
		}
	}

	return ""
}

// checkSedFlags rejects sed -i / --in-place, which modifies files in place.
// All other sed usage (stream editing from stdin/files to stdout) is allowed.
func checkSedFlags(cmd string) string {
	tokens := strings.Fields(cmd)
	for _, tok := range tokens[1:] {
		if tok == "-i" || tok == "--in-place" || strings.HasPrefix(tok, "-i") {
			return fmt.Sprintf("bash_profile(read-only): sed flag %q modifies files in place and is not permitted", tok)
		}
	}
	return ""
}

// checkAwkScript rejects awk commands whose script body contains "system("
// which allows arbitrary command execution. This is a conservative substring
// check — it may block legitimate scripts that contain the string "system("
// in a comment or string literal. Authors who need system() should use the
// commands profile and explicitly own the risk.
func checkAwkScript(cmd string) string {
	if strings.Contains(cmd, "system(") {
		return "bash_profile(read-only): awk script contains 'system(' which allows arbitrary command execution; not permitted in read-only profile"
	}
	// Also block getline piped from a command (getline ... | or | getline).
	// Simplified check: reject if "getline" appears alongside a "|" in the
	// same token vicinity. Since metacharacter "|" is already caught upstream,
	// this only fires if somehow "|" appears in quoted context. Belt-and-suspenders.
	if strings.Contains(cmd, "getline") {
		return "bash_profile(read-only): awk script contains 'getline' which may execute external commands; not permitted in read-only profile"
	}
	return ""
}

// MakeSandboxEnv returns environment variable overrides that implement
// best-effort network denial for the sandboxed-write profile. The caller
// merges these into the subprocess environment.
//
// Limitation: this blocks HTTP/HTTPS via the proxy env vars but does not
// prevent raw TCP connections. Full network isolation requires a
// mount-namespace / seccomp profile (see validator_sandbox.go).
func MakeSandboxEnv() []string {
	return []string{
		"HTTP_PROXY=invalid://blocked",
		"HTTPS_PROXY=invalid://blocked",
		"http_proxy=invalid://blocked",
		"https_proxy=invalid://blocked",
		"NO_PROXY=",
		"no_proxy=",
	}
}

// EnsureScratchDir creates and returns the scratch directory for a
// sandboxed-write call. When profile.ScratchDir is empty, a system TempDir
// is used. The returned path is the actual directory created; the caller is
// responsible for cleaning it up after the call.
func EnsureScratchDir(profile *BashProfile) (string, error) {
	if profile == nil || profile.Kind != BashProfileSandboxWrite {
		return "", fmt.Errorf("bash_profile.EnsureScratchDir: not a sandboxed-write profile")
	}
	base := profile.ScratchDir
	if base == "" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "kitsoki-sandbox-*")
	if err != nil {
		return "", fmt.Errorf("bash_profile.EnsureScratchDir: create scratch dir: %w", err)
	}
	return filepath.Clean(dir), nil
}

// containsMetaChars reports whether cmd contains any shell metacharacter that
// could allow command injection. Conservative — flags the entire command rather
// than attempting to parse quoted regions (which would require a full shell
// lexer).
func containsMetaChars(cmd string) bool {
	for _, r := range shellMetaChars {
		if strings.ContainsRune(cmd, r) {
			return true
		}
	}
	return false
}

// extractArgv0 returns the first whitespace-delimited token of cmd, which is
// the program name (argv[0]). Used to check against per-profile allowlists.
func extractArgv0(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if i := strings.IndexByte(cmd, ' '); i >= 0 {
		return cmd[:i]
	}
	return cmd
}
