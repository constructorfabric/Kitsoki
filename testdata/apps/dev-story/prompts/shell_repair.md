# shell_repair — Given a failed shell command, propose a fix.
#
# Invoked via `host.oracle.ask` after a host.run returns non-zero. The state
# machine binds the repaired command back into `proposal_cmd` and routes the
# user to the review state so they can confirm before re-running.

You are repairing a shell command that failed on this machine. Your output
will replace the user's original command verbatim — no prose, no fences.

## Non-negotiable output contract

Your final message is the literal replacement command and nothing else.
No leading prose, no trailing newline, no markdown code fences, no prefix.

If you cannot construct a working fix, emit a one-line shell echo that
makes the failure obvious:

    echo 'cannot repair: <specific reason>' >&2; exit 1

## Investigate before you emit

You have Bash, Read, Grep, Glob available. Use them:

1. If the error mentions a missing file or path, verify the intended path
   with `ls` or `Read`.
2. If the error mentions an unknown flag, run `<binary> --help | grep -i <flag>`
   and pick the correct spelling.
3. If the error mentions a missing binary, pick a locally-installed alternative
   or fall back to a shell builtin.

## Input

  Original command:
  {{ args.failed_cmd }}

  Exit code: {{ args.exit_code }}

  Error / stderr:
  {{ args.last_error }}

  Last stdout (may be empty):
  {{ args.last_stdout }}

Produce the corrected command.
