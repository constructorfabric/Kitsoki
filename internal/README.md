# `internal/` — package documentation standard

This is the **source of truth** for how every Go package under
`internal/` documents itself. The bar is one standard, applied
uniformly: a reader should be able to run `go doc ./internal/<pkg>` and
understand what the package is for, how to use it, what its contracts
are, and what it deliberately does not do — without opening the source.

> Writing or auditing a package? The `go-module-docs` skill
> (`.agents/skills/go-module-docs/SKILL.md`) is the operational checklist
> and verification loop for this standard.

## The paragon

[`semroute`](semroute) is the reference implementation of this
standard — read it first and copy its shape:

```
go doc -all ./internal/semroute
```

[`clock`](clock) is the secondary reference: the same discipline on an
interface, plus the smallest possible runnable examples
(`clock/example_test.go`). [`journal`](journal) is a third good model
for a `doc.go` that explains a subsystem's invariants.

## The eight-point rubric

| # | Standard | Why it matters |
|---|----------|----------------|
| **S1** | A `doc.go` whose **first sentence** says what the package is *and where it sits* relative to its neighbours. | That sentence is all `go doc <pkg>` and pkg.go.dev list views show. |
| **S2** | `#`-headed sections in reader-first order — *Algorithm → invariants → Worked example → Lifecycle → Non-goals* — including a concrete input→output **worked example**. | A worked trace teaches faster than prose; godoc renders the headings as navigable sections. |
| **S3** | A **Non-goals** section, each entry with its reason. | Stops future readers from "fixing" a deliberate omission. |
| **S4** | Every exported symbol documented, comment starting with the symbol name, in **why-over-what** voice. | The signature is already visible; the doc's job is the contract and the reasoning. |
| **S5** | The hard **contracts** stated explicitly: zero value, nil receiver, concurrency safety, error conditions. | These are what cause production bugs when left implicit. |
| **S6** | Magic numbers lifted to **documented named constants**. | Discoverable in one place; callers stop repeating literals. |
| **S7** | Runnable **`Example*` functions** with checked `// Output:` blocks. | Compiled and executed by `go test`, so the docs cannot drift from behaviour; they render as canonical usage. |
| **S8** | References resolve to **living docs** — no dangling `docs/proposals/…` `§N.N` pointers. | Proposals are deleted once implemented (see root `CLAUDE.md`); a reference you can't follow is worse than none. |

### On S8 specifically

Proposals under `docs/proposals/` are **deleted when their work
ships**. A package comment that cites `docs/proposals/<x>.md §2.1`
therefore becomes a dangling pointer the moment the feature lands. When
you implement a proposal:

1. Move its durable content into a living narrative doc
   (`docs/architecture/…`, `docs/stories/…`).
2. Repoint the package's `# Reference` section there.
3. Drop or rephrase bare `§N.N` section numbers — the living doc
   renumbers, so the proposal's numbers no longer mean anything.

Audit a package with:

```
grep -rn 'proposals/.*\.md\|proposal §' internal/<pkg>/
```

## Verification loop

```sh
go doc -all ./internal/<pkg>                  # read it as a consumer
go build ./internal/<pkg>/...                 # compiles
go vet   ./internal/<pkg>/...                 # no doc/format complaints
go test  -run '^Example' ./internal/<pkg>/... # examples pass (S7)
grep -rn 'proposal §\|proposals/.*\.md' internal/<pkg>/   # empty (S8)
```

A package that clears all five and reads cleanly under `go doc -all` is
at standard.
