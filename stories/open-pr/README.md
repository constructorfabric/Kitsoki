# open-pr

`open-pr` packages an existing feature or bug branch, verifies the local review
evidence, pushes the branch, and opens the pull request.

It is intended as the reusable tail after a maker/bugfix/feature story has
already produced a committed branch in a worktree.

## Pipeline

```
idle --open--> prepare --prepared--> publishing --opened--> @exit:opened
                    |                         |
                    +---- prepare_failed -----+---- open_failed --> @exit:needs-human
```

`prepare` is deterministic and no-LLM. It writes
`.context/open-pr/<branch>.md` and uses that markdown as the PR body when
`world.body` is empty.

`publishing` is the external side effect: host.git pushes the branch and opens
the PR through the native provider.

## Required World

| Key | Meaning |
|---|---|
| `head_branch` | Branch to push and open. |
| `branch_kind` | `feature` or `bug`; blank infers from branch prefix. |
| `title` | PR title. |
| `feature_description` | Human-facing feature/bug description used in the generated PR body. |
| `product_site_paths` | Newline or comma-separated product-site source/catalog paths changed for the branch. |
| `demo_source_path` | Checked-in source/spec used to render the demo video. |

Branch naming is standardized:

- `feature/*` for feature branches.
- `fix/*` or `bug/*` for bug branches.

## Demo Evidence

The story requires a rendered `.mp4` under `demo_artifact_dir`. If
`demo_artifact_dir` is empty it defaults to `.artifacts/<product_feature_id>` or
`.artifacts/<branch-slug>`.

There are two render modes:

- Set `demo_render_command` to a no-LLM command that renders the local demo.
- Set `product_feature_id` and let the story run `make demo-feature FEATURE=<id>`.

The generated package records:

- the feature/bug summary;
- the standardized head/base branch;
- the product-site source paths;
- the demo source path;
- the rendered local MP4 path under `.artifacts`.

Generated video output stays in `.artifacts`. The source/spec path is what the
PR should commit.

## Exits

| Exit | Requires | Meaning |
|---|---|---|
| `opened` | `pr_url` | The branch was pushed and the pull request was opened. |
| `needs-human` | `last_error` | Package validation, demo rendering, push, or PR creation failed. |
| `abandoned` | | Operator quit before opening. |

## Validation

```
go run ./cmd/kitsoki test flows stories/open-pr/app.yaml
```
