#!/usr/bin/env bash
# scripts/setup.sh — install everything `make install` needs on a fresh machine.
#
# Covers macOS (Homebrew), RockyLinux/RHEL-family (dnf) and Debian/Ubuntu (apt).
# It is idempotent: anything already present at a new-enough version is skipped.
#
# Build deps (required for `make install`):
#   - Go >= 1.25     (compiles the binary)
#   - Node >= 20     (builds the runstatus SPA)
#   - pnpm >= 11     (SPA package manager; installed via corepack)
#   - git            (Go module fetches + kitsoki shells out to it at runtime)
#   - bash           (kitsoki shells out to it at runtime)
#
# Optional (some make targets / features):
#   - jq             (make fix-tests)
#   - ffmpeg         (make demo-tour)
#   - gh             (GitHub integration)
#
# Also links the project skills (`.agents/skills/<name>/SKILL.md`) into
# `.claude/skills/<name>` and the project subagents (`.agents/agents/<name>.md`)
# into `.claude/agents/<name>.md` so Claude Code discovers the same skills and
# agents as Codex.
#
# Run via `make setup`. Re-run any time; safe to run repeatedly.
set -euo pipefail

GO_MIN_MAJOR=1
GO_MIN_MINOR=25
NODE_MIN=20
PNPM_VERSION=11.3.0

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwar: \033[0m%s\n' "$*" >&2; }
have() { command -v "$1" >/dev/null 2>&1; }

# --- OS detection ----------------------------------------------------------
OS="" PKG=""
detect_os() {
	case "$(uname -s)" in
		Darwin) OS=macos; PKG=brew ;;
		Linux)
			if [ -r /etc/os-release ]; then . /etc/os-release; fi
			case "${ID:-}${ID_LIKE:-}" in
				*debian*|*ubuntu*) OS=debian; PKG=apt ;;
				*rhel*|*fedora*|*rocky*|*centos*) OS=rocky; PKG=dnf ;;
				*)
					if   have apt-get; then OS=debian; PKG=apt
					elif have dnf;     then OS=rocky;  PKG=dnf
					else warn "unknown Linux distro; install deps manually"; OS=linux; PKG=""
					fi ;;
			esac ;;
		*) warn "unsupported OS $(uname -s); install deps manually"; OS=other; PKG="" ;;
	esac
	log "detected OS=$OS package-manager=${PKG:-none}"
}

SUDO=""
need_sudo() {
	if [ "$(id -u)" -ne 0 ] && have sudo; then SUDO=sudo; fi
}

# --- version helpers -------------------------------------------------------
go_ok() {
	have go || return 1
	local v; v=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
	[ -n "$v" ] || v=$(go version | awk '{print $3}' | sed 's/^go//')
	local maj min; maj=${v%%.*}; min=${v#*.}; min=${min%%.*}
	[ "$maj" -gt "$GO_MIN_MAJOR" ] 2>/dev/null && return 0
	[ "$maj" -eq "$GO_MIN_MAJOR" ] && [ "$min" -ge "$GO_MIN_MINOR" ]
}
node_ok() {
	have node || return 1
	local maj; maj=$(node -v | sed 's/^v//; s/\..*//')
	[ "$maj" -ge "$NODE_MIN" ] 2>/dev/null
}

# --- package-manager installers --------------------------------------------
pm_install() {
	case "$PKG" in
		brew) brew install "$@" ;;
		apt)  $SUDO apt-get install -y --no-install-recommends "$@" ;;
		dnf)  $SUDO dnf install -y "$@" ;;
		*)    warn "no package manager; please install: $*"; return 1 ;;
	esac
}

pm_refresh() {
	case "$PKG" in
		apt) $SUDO apt-get update ;;
		dnf) $SUDO dnf -y makecache || true ;;
	esac
}

ensure_brew() {
	have brew && return 0
	log "Homebrew not found — installing"
	/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
	# Make brew available for the rest of this run.
	if [ -x /opt/homebrew/bin/brew ]; then eval "$(/opt/homebrew/bin/brew shellenv)"
	elif [ -x /usr/local/bin/brew ]; then eval "$(/usr/local/bin/brew shellenv)"; fi
}

# --- individual deps -------------------------------------------------------
install_go() {
	if go_ok; then log "Go $(go version | awk '{print $3}') OK"; return; fi
	log "installing Go >= $GO_MIN_MAJOR.$GO_MIN_MINOR"
	case "$PKG" in
		brew) pm_install go ;;
		apt|dnf)
			# Distro Go is usually too old; install the official tarball.
			local arch tar url
			case "$(uname -m)" in
				x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;;
				*) warn "unknown arch $(uname -m); install Go manually"; return 1 ;;
			esac
			tar="go${GO_MIN_MAJOR}.${GO_MIN_MINOR}.1.linux-${arch}.tar.gz"
			url="https://go.dev/dl/${tar}"
			log "downloading $url"
			curl -fsSL "$url" -o "/tmp/$tar"
			$SUDO rm -rf /usr/local/go
			$SUDO tar -C /usr/local -xzf "/tmp/$tar"
			rm -f "/tmp/$tar"
			export PATH="/usr/local/go/bin:$PATH"
			if [ -w /etc/profile.d ] || [ -n "$SUDO" ]; then
				echo 'export PATH=$PATH:/usr/local/go/bin' | $SUDO tee /etc/profile.d/go.sh >/dev/null
			fi
			warn "added /usr/local/go/bin to PATH — open a new shell or 'source /etc/profile.d/go.sh'" ;;
	esac
}

install_node() {
	if node_ok; then log "Node $(node -v) OK"; return; fi
	log "installing Node >= $NODE_MIN"
	case "$PKG" in
		brew) pm_install node ;;
		apt)
			curl -fsSL https://deb.nodesource.com/setup_22.x | $SUDO -E bash -
			pm_install nodejs ;;
		dnf)
			curl -fsSL https://rpm.nodesource.com/setup_22.x | $SUDO -E bash -
			pm_install nodejs ;;
	esac
}

install_pnpm() {
	if have pnpm; then log "pnpm $(pnpm -v) OK"; return; fi
	log "installing pnpm $PNPM_VERSION via corepack"
	if have corepack; then
		$SUDO corepack enable 2>/dev/null || corepack enable || true
		corepack prepare "pnpm@$PNPM_VERSION" --activate
	elif [ "$PKG" = brew ]; then
		pm_install pnpm
	else
		npm install -g "pnpm@$PNPM_VERSION"
	fi
}

install_required() {
	for tool in git bash curl; do
		have "$tool" && continue
		log "installing $tool"
		pm_install "$tool" || warn "could not install $tool"
	done
}

install_optional() {
	for tool in jq ffmpeg gh; do
		have "$tool" && { log "$tool OK"; continue; }
		log "installing optional: $tool"
		pm_install "$tool" || warn "optional dep '$tool' not installed (some targets need it)"
	done
}

# --- project skills --------------------------------------------------------
# Link .agents/skills/<name> → .claude/skills/<name> (relative symlinks) so
# Claude Code picks them up. Idempotent: refreshes our own symlinks, never
# clobbers a real file/dir a human may have placed there.
install_skills() {
	local root src dst
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
	src="$root/.agents/skills"
	dst="$root/.claude/skills"
	[ -d "$src" ] || { warn "no .agents/skills dir; skipping skill links"; return; }
	mkdir -p "$dst"
	local n=0 name link
	for dir in "$src"/*/; do
		[ -f "${dir}SKILL.md" ] || continue
		name="$(basename "$dir")"
		link="$dst/$name"
		if [ -L "$link" ]; then
			rm -f "$link"
		elif [ -e "$link" ]; then
			warn "skills: $link exists and is not a symlink — leaving as-is"
			continue
		fi
		ln -s "../../.agents/skills/$name" "$link"
		n=$((n + 1))
	done
	log "linked $n project skill(s) into .claude/skills/"
}

# --- project agents --------------------------------------------------------
# Link .agents/agents/<name>.md → .claude/agents/<name>.md (relative symlinks)
# so Claude Code discovers the project's shared subagents. Same idempotent
# contract as install_skills: refreshes our own symlinks, never clobbers a real
# file a human may have placed there.
install_agents() {
	local root src dst
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
	src="$root/.agents/agents"
	dst="$root/.claude/agents"
	[ -d "$src" ] || { warn "no .agents/agents dir; skipping agent links"; return; }
	mkdir -p "$dst"
	local n=0 name link
	for file in "$src"/*.md; do
		[ -f "$file" ] || continue
		name="$(basename "$file")"
		# AGENTS.md / CLAUDE.md are dir-level notes, not agent definitions.
		case "$name" in AGENTS.md|CLAUDE.md) continue ;; esac
		link="$dst/$name"
		if [ -L "$link" ]; then
			rm -f "$link"
		elif [ -e "$link" ]; then
			warn "agents: $link exists and is not a symlink — leaving as-is"
			continue
		fi
		ln -s "../../.agents/agents/$name" "$link"
		n=$((n + 1))
	done
	log "linked $n project agent(s) into .claude/agents/"
}

# --- git hooks -------------------------------------------------------------
# Link scripts/git-hooks/* → .git/hooks/* (relative symlinks) so git itself
# enforces the repo's hooks. The reference-transaction guard pins the primary
# checkout to its default branch — see scripts/git-hooks/reference-transaction.
# prepare-commit-msg auto-appends a DCO Signed-off-by so the upstream cncf/dco2
# check never goes red — see scripts/git-hooks/prepare-commit-msg.
# .git/hooks is shared with linked worktrees, so installing here covers them all.
# Idempotent: refreshes our own symlinks, never clobbers a real hook a human placed.
install_git_hooks() {
	local root src dst common primary
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
	# Resolve the common git dir so this works whether setup runs from the primary
	# checkout or a linked worktree.
	common="$(git -C "$root" rev-parse --git-common-dir 2>/dev/null || true)"
	[ -n "$common" ] || { warn "not a git repo; skipping git hook links"; return; }
	case "$common" in /*) ;; *) common="$root/$common" ;; esac
	common="$(cd "$common" && pwd)"
	dst="$common/hooks"
	# Point the symlinks at the PRIMARY checkout's hook scripts, never the (possibly
	# ephemeral) worktree that setup happens to run from. The common git-dir is
	# <primary>/.git, so its parent is the primary checkout root. A linked worktree
	# under .worktrees/ can be deleted later; if a hook symlink pointed into one it
	# would silently dangle and disarm the reference-transaction guard — which is
	# exactly how the primary checkout drifted off main once. The primary checkout
	# is stable, so anchor there.
	primary="$(cd "$(dirname "$common")" && pwd)"
	src="$primary/scripts/git-hooks"
	[ -d "$src" ] || { warn "no scripts/git-hooks dir at $src; skipping git hook links"; return; }
	mkdir -p "$dst"
	local n=0 name link rel
	for hook in "$src"/*; do
		[ -f "$hook" ] || continue
		name="$(basename "$hook")"
		chmod +x "$hook"
		link="$dst/$name"
		if [ -L "$link" ]; then
			rm -f "$link"
		elif [ -e "$link" ]; then
			warn "git-hooks: $link exists and is not a symlink — leaving as-is"
			continue
		fi
		# Absolute symlink: the hooks dir lives under .git, whose depth from the
		# repo root varies, so a relative path is brittle here.
		ln -s "$hook" "$link"
		n=$((n + 1))
	done
	log "linked $n git hook(s) into $dst"
}

# --- git config (conflict-avoidance for parallel agents) -------------------
# Embed the repo's "excellent git workflow" config into this clone's
# .git/config. These can't live in a committed file (git refuses to read
# repo-tracked config for safety), so `make setup` is the embedding point.
# Everything here is local to the clone and idempotent.
#
#   rerere            — record a conflict resolution once, auto-replay it every
#                       later time the same hunk recurs. THE core no-LLM lever:
#                       N agents rebasing past the same upstream commit hit the
#                       same conflict; the first resolution (human or agent) makes
#                       all the rest free. The git-ops conflict room runs
#                       `git rerere` before its LLM resolver (see conflict.yaml).
#   rerere.autoupdate — stage the replayed resolution automatically.
#   merge.conflictStyle=zdiff3 — show the common base + both sides in conflict
#                       markers; far better input for the rare case an agent IS
#                       needed (and for humans).
#   rebase.autostash  — never lose a dirty tree to a rebase; stash + pop around it.
configure_git() {
	local root train
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
	git -C "$root" rev-parse --git-dir >/dev/null 2>&1 || {
		warn "not a git repo; skipping git config"; return; }
	git -C "$root" config rerere.enabled true
	git -C "$root" config rerere.autoupdate true
	git -C "$root" config merge.conflictStyle zdiff3
	git -C "$root" config rebase.autostash true
	# Seed the rerere cache from already-merged history so known conflict
	# resolutions are available before any agent hits them (best-effort; the
	# contrib script ships with git but its path varies by platform).
	for train in \
		/usr/share/doc/git/contrib/rerere-train.sh \
		/usr/local/share/git-core/contrib/rerere-train.sh \
		"$(git --exec-path 2>/dev/null)/../../share/git-core/contrib/rerere-train.sh"; do
		[ -r "$train" ] || continue
		log "seeding git rerere cache from recent merge history"
		( cd "$root" && bash "$train" --overwrite 'HEAD~200..HEAD' ) >/dev/null 2>&1 || true
		break
	done
	log "configured git (rerere + autoupdate, zdiff3, rebase.autostash)"
}

# --- main ------------------------------------------------------------------
main() {
	detect_os
	need_sudo
	[ "$PKG" = brew ] && ensure_brew
	pm_refresh
	install_required
	install_go
	install_node
	install_pnpm
	install_optional
	install_skills
	install_agents
	install_git_hooks
	configure_git

	log "setup complete"
	echo
	echo "Versions:"
	have go    && go version
	have node  && echo "node $(node -v)"
	have pnpm  && echo "pnpm $(pnpm -v)"
	echo
	echo "Next: run 'make install' (you may need a fresh shell so PATH changes take effect)."
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
	main "$@"
fi
