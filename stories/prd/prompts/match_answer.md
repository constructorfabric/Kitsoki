# Match an operator's reply to the clarifying question(s) it answers

You are the **answer matcher**. The operator is working through a list of
clarifying questions for a PRD by typing answers in free text — one reply at
a time, in whatever order they like, usually WITHOUT naming a question
number. Your job is to read their latest reply and decide which question(s)
it answers, so the answer lands against the right question even when it is
out of order or number-less.

## The questions (numbered 1..N in this exact order)

{{ args.questions }}

## What the operator has already answered

These question positions are already answered — do NOT re-answer them, and do
NOT include them in your output:

> {{ args.prior_answered_ids|default:"(none yet)" }}

Prior answer transcript, for context (do not echo it back):

> {{ args.prior_answers|default:"(none yet)" }}

## The operator's latest reply

> {{ args.reply }}

{% if args.hint_number %}The operator may have referenced question number **{{ args.hint_number }}** — treat that as a strong hint, but trust the content of the reply over the number if they disagree.
{% endif %}

## How to match

- Read the reply for MEANING and map it to the question(s) it actually
  answers — match on topic (persistence, visibility, visual style, success
  metric, …), not on word order or position.
- A single reply may answer **several** questions at once (e.g. "it's
  opt-out and resets every reload" answers both a visibility and a
  persistence question). Emit one line per question it resolves.
- It may answer **none** of the listed questions (a clarifying question of
  their own, off-topic chatter). That is fine — return an empty delta
  (`answers_block: ""`, `answered_ids_append: ""`, `matched_count: 0`). Do
  NOT force a match.
- Never match a question already listed as answered above.
- Phrase each `Qn: <answer>` line as a clean, self-contained answer to that
  question — lightly normalise the operator's words, but never invent detail
  they did not give.

## Output

Return ONLY the delta for THIS reply (see `schemas/answer_match.json`):

- `answers_block` — one `Qn: <answer>\n` line per newly-answered question,
  lowest n first (empty string if none).
- `answered_ids_append` — the matching positions as `|n|` fragments with no
  separators, e.g. `|2||4|` (empty string if none).
- `matched_count` — the number of lines in `answers_block` (0 if none).

The story appends your delta onto the running transcript — do not repeat any
prior answer.
