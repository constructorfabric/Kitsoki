You are repairing a slidey deck JSON spec that FAILED TO RENDER. The renderer
rejected the deck; your job is to fix the deck spec so it renders cleanly,
changing as little as possible.

Do not use shell commands or generic filesystem tools. The only allowed deck
IO is the Slidey MCP. Read the deck with `slidey_read_spec`, apply the smallest
fix with `slidey_patch_spec` or `slidey_write_spec`, and call `slidey_validate`
before submitting unless the failure is clearly outside the deck content.

If you are running in Codex and one of these tools is not immediately visible,
call `tool_search` for the exact Slidey tool name, then use the returned tool.
Do not report the Slidey MCP as unavailable until `tool_search` has failed for
the needed `slidey_*` tool and for `submit`.

{% block spec_project_context %}{% endblock %}

## Workspace

Repository workspace: `{{ args.workspace }}`
Managed workdir: `{{ args.workdir|default:"(current checkout)" }}`

The Slidey MCP root is this workspace. When calling Slidey MCP tools, use the
workspace-relative path, not the repository path joined onto the workspace.

## Deck

Repository path: `{{ args.deck.spec_path }}`
Workspace-relative path: `{{ args.deck.workspace_spec_path|default:args.deck.spec_path }}`

{{ args.deck.summary }}

Read the workspace-relative path first through the Slidey MCP, then edit it in
place to fix the failure.

## The render error

The renderer reported:

```
{{ args.last_error }}
```

## How to repair

- Read the deck JSON at the workspace-relative path above and diagnose what the
  error is pointing at: a malformed scene, a missing or wrong field, an invalid
  scene `type`, a bad value, or a structural problem.
- Apply the **smallest** change that makes the deck valid. Preserve the deck's
  content and intent — fix the defect, don't rewrite the deck.
- If the error does NOT describe a problem with the deck content itself (e.g. a
  missing input file, a renderer/tool failure, a path or environment problem),
  the deck is not the cause: leave the spec unchanged and say so in `summary`.
  Re-rendering an unchanged deck will surface the same failure and the loop will
  stop on its own once retries run out.

## What to produce

Submit the deck object: the same repository-render `spec_path` (repo-relative,
not an absolute filesystem path), and a one-line `summary` of what you changed
to fix the render (or a note that the deck was already correct and the failure
is external).
