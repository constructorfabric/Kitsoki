# Source from an interactive shell in a repository where the launch-policy
# pack is installed. It makes bare Claude/Codex launches policy-aware and also
# routes Kitsoki-hosted backend calls through the same wrappers.
# Works when sourced from bash or zsh (zsh leaves BASH_SOURCE unset, so
# resolve this file's own path per shell; the zsh expansion hides behind eval
# so bash never parses it).
if [ -n "${BASH_SOURCE:-}" ]; then
  _kitsoki_launch_policy_src="${BASH_SOURCE[0]}"
elif [ -n "${ZSH_VERSION:-}" ]; then
  eval '_kitsoki_launch_policy_src="${(%):-%x}"'
else
  _kitsoki_launch_policy_src="$0"
fi
_kitsoki_launch_policy_root="$(cd "$(dirname "$_kitsoki_launch_policy_src")/.." && pwd -P)"
case ":$PATH:" in
  *":$_kitsoki_launch_policy_root/.kitsoki/bin:"*) ;;
  *) export PATH="$_kitsoki_launch_policy_root/.kitsoki/bin:$PATH" ;;
esac
export KITSOKI_AGENT_CLAUDE_BIN="$_kitsoki_launch_policy_root/.kitsoki/bin/claude"
export KITSOKI_AGENT_CODEX_BIN="$_kitsoki_launch_policy_root/.kitsoki/bin/codex"
unset _kitsoki_launch_policy_root _kitsoki_launch_policy_src
