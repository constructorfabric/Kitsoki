# Proposal — Remote sync for bug reports (`kitsoki bug sync`)

**Status:** Draft. Not implemented. The on-disk format
([`docs/stories/bugs.md`](../stories/bugs.md)) was deliberately shaped so this
command lands as an additive write to a new frontmatter block,
without breaking any existing file. The format is the contract;
this proposal is the command that consumes it.

**Source.** Distilled from §4 of the (now-deleted)
`bug-format-proposal.md`. Everything else in that proposal shipped
(Phases A/B/C); only the remote-sync sketch remained in design,
and it lives here so the planning record stays current rather than
graveyarded.

---

## 1. Goal

`kitsoki bug create` writes a local markdown file. `kitsoki bug
sync` pushes that file to a configured remote tracker (GitHub
Issues for `target: kitsoki` is the obvious starter; Jira follows)
and writes the remote id back into the file's frontmatter so the
two stay correlated. Re-running `sync` on an already-synced file
updates the remote issue's body when the local body has changed,
and otherwise no-ops.

The local file remains authoritative for the body. The remote
issue is a projection. Comments and labels on the remote side are
deliberately **not** mirrored back — no two-way merge headaches.

## 2. Shape

```
kitsoki bug sync <file.md>                     # one-shot
kitsoki bug sync --target kitsoki --since 7d   # bulk
```

Behaviour, one file at a time:

1. Read the file's frontmatter + body.
2. Compute `sha256(body)` (the markdown after the closing `---`,
   verbatim — no normalization, so the digest survives untouched
   re-saves).
3. If the file has no `external:` block → create a new issue via
   the configured provider for this target. Write
   `external: { provider, id, url, synced_at, digest }` into the
   frontmatter.
4. If `external` is present and the stored digest differs from
   the new digest → `PATCH` the remote issue's body; update
   `digest` + `synced_at`.
5. If the digest matches → no-op.

## 3. The `external:` frontmatter block

This block is **only** written by `kitsoki bug sync`. Hand-edits to
identity / context / classification / evidence / body remain
authoritative; sync rewrites this block and nothing else. The
parser must round-trip through `yaml.Node` so unknown sibling
keys at the top level survive a re-write.

```yaml
external:
  provider: github                                  # github | jira
  id: "kitsoki-foo/kitsoki#142"                     # provider-shaped issue id
  url: https://github.com/kitsoki-foo/kitsoki/issues/142
  synced_at: 2026-05-15T09:00:00Z
  digest: 7a9c…                                     # sha256(body) at last sync
```

## 4. Provider configuration

A future `~/.kitsoki/sync.yaml` (or similar) carries per-target
provider settings: which tracker, which repo / project, auth.
Schema TBD — pick when the first provider lands. For local /
test use, a `file` provider that writes a stub issue to a chosen
directory keeps the sync path exercisable without network access.

## 5. Why the v1 format already supports this

The format choices in [`docs/stories/bugs.md`](../stories/bugs.md) that make this
land as a pure-additive change:

- Frontmatter is at the top → cheap to read without parsing the
  body.
- `external:` is a single isolated block → safe for `sync` to
  rewrite without touching identity / classification / evidence.
- `digest` over the body lets `sync` be idempotent and detect drift.
- `target` field lets `--target kitsoki` filter without scanning
  bodies.
- "Unknown frontmatter keys are preserved" is the contract that
  makes a YAML-node round-trip the right write strategy.

## 6. Decision points for review

1. **Authoritative side.** v1 picks "local file is authoritative,
   remote tracker is a projection" because that matches the
   dev-mode workflow (grep, edit, commit). The alternative —
   "once synced, edit on GitHub" — is what most teams end up
   with in practice; worth a second opinion before §2 lands.
2. **Comment mirroring.** Deliberately out of scope. Open
   question whether even one-way (remote → local-as-comment) is
   worth the merge surface; the current default is no, on the
   theory that discussion happens where the tracker lives.
3. **Bulk delete / archive.** When a remote issue is closed,
   should the local file get `status: resolved` written back?
   v1 says no (one-way push). Worth revisiting once we know how
   teams actually use the feature.
