# Writing bake-off oracles

An oracle is the hidden test that decides whether a candidate's fix is correct.
It is the single most important artifact in a cell: a bad oracle silently
corrupts every verdict that depends on it. Two failure modes matter, and the
harness now guards both.

## The contract: RED / GREEN / divergent

Every oracle must satisfy three legs. `bench.py verify` proves the first two
automatically against the manifest's `baseline_sha` / `fix_sha`:

1. **RED at baseline** — the bug is present; the oracle fails. (If it passes at
   baseline it isn't testing the bug.)
2. **GREEN at the canonical fix** — `fix_sha`'s tree makes it pass.
3. **GREEN at a *divergent* correct fix** — *any* implementation that satisfies
   the spec passes, not just the author's. This one can't be proven mechanically
   (it needs a second correct implementation), so it is a **design obligation on
   the author**, enforced by the rule below.

## Assert behavior, not prose

The rule that makes leg 3 hold: **assert the observable contract, never one
implementation's exact human-readable wording.**

This is not hypothetical. The `bug9` oracle originally asserted:

```go
strings.Contains(resB.Error, "already checked out by session")   // ❌ brittle
```

GLM-5.2 produced a fully correct host-layer fix whose refusal said *"is already
**in use by** session"* — semantically identical, arguably clearer. The oracle
failed it on the word "in use" vs "checked out". The oracle was wrong, not the
fix.

The behavior-based version accepts any equivalent wording plus the load-bearing
identifier:

```go
ownershipConflict := strings.Contains(e, "checked out by") ||
    strings.Contains(e, "in use by") ||
    strings.Contains(e, "owned by") ||
    strings.Contains(e, "held by")
if !ownershipConflict || !strings.Contains(e, "session-A") { ... }   // ✅ behavior
```

Guidelines:

- Match **short, stable tokens** (a status word, an error code, a load-bearing
  id like `session-A`), or a **set of synonyms** — never a long sentence.
- Prefer asserting **structured outcomes** (a returned error is non-empty; a
  field has a value; a file changed) over string scraping.
- Failure **messages** (`t.Fatalf("expected X; got %q", v)`) can read like prose
  freely — they are diagnostics, not match targets.
- **Never** put the oracle's match string into the bug ticket. That is
  teaching-to-the-test and invalidates the result. Steer the *fix layer* and the
  required *behavior* in the ticket; let the model find the words.

## The linter

`bench.py lint-oracles --project <p> [--bug <id>] [--strict]` flags string
literals used in containment/match calls (`strings.Contains`, `.includes`,
`toContain`, …) that look like natural-language sentences (≥3 alphabetic words,
≥18 chars, mostly letters). Findings are advisory by default; `--strict` exits
nonzero (wire it into CI once a project's oracles are clean). It deliberately
ignores `%`-format strings and generic `assert`/`expect` message arguments, so
failure messages aren't flagged.
