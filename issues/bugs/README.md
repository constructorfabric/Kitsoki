# `issues/bugs/` — dogfood bug fixtures

The files here are real bug reports filed during kitsoki's own dogfood
loop, kept in-tree because the `stories/kitsoki-dev/` story reads
them via `host.local_files.ticket` — they're what makes
`kitsoki run stories/kitsoki-dev/app.yaml` show a non-empty ticket
list out of the box.

**They are not a public issue tracker.** Don't file new bugs by
adding files here; use the project's GitHub issues. These exist as
example fixtures + audit trail of the bugfix pipeline working on
real defects.

Format and frontmatter schema: see [`../README.md`](../README.md).
