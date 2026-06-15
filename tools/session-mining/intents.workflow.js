// intents.workflow.js — the ONE LLM step (step B) of intent mining.
//
// Mirrors session-idea-mining/mine.workflow.js: one reader agent per batch,
// schema-validated structured output. Each reader SEGMENTS a distilled trace into
// intent spans and, per span, drafts a recipe — but it is treated as a strictly-
// validated ORACLE: every action MUST carry a citation (the 1-based trace line it
// came from), and ground.py later rejects any action/parameter that doesn't check
// out against the real trace (review §3). The oracle therefore proposes; the
// deterministic spine disposes.
//
// Invoke AFTER `prep.py --job <name>` (no --redact; intent mining is local):
//   Workflow({ scriptPath: "tools/session-mining/intents.workflow.js", args: {
//     batchDir:   ".artifacts/session-mining/<job>/batches",  // prep.py BATCHDIR=
//     batchCount: 7,                                           // prep.py BATCHES=
//     outDir:     ".artifacts/session-mining/<job>/oracle",    // raw per-batch JSON
//     actionTags:  [...core.yaml ids...],   // optional; mirror vocab/tags.yaml
//     surfaceTags: [...], scopeTags: [...]   // optional; mirror vocab/tags.yaml
//   }})
//
// Writes one oracle-batch-NN.json per batch under outDir (each {records:[...]}),
// then ground.py --oracle <outDir> grounds them. The workflow also RETURNS the
// merged records so a caller can pipe them directly.
//
// IMPORTANT: the oracle must NOT emit the verbatim user text — that is recovered
// deterministically from the raw jsonl in emit.py (step F). It emits the span's
// line range only.

export const meta = {
  name: 'session-intent-mining',
  description: 'Segment distilled traces into intent spans + drafted, cited recipes (the strictly-validated oracle pass)',
  phases: [
    { title: 'Oracle', detail: 'one reader agent per batch: segment into intent spans + cited recipes' },
  ],
}

// args may arrive as a JSON STRING when launched via Workflow({scriptPath, args})
// (the runtime doesn't always parse it), or as an object inline. Coerce to an object.
const A = (typeof args === 'string')
  ? (() => { try { return JSON.parse(args) } catch (_) { return {} } })()
  : (args || {})

const batchDir = A.batchDir
const batchCount = A.batchCount
const outDir = A.outDir || (batchDir ? batchDir.replace(/batches\/?$/, 'oracle') : null)
if (!batchDir || !batchCount) {
  throw new Error('intents.workflow.js requires args.batchDir and args.batchCount (run prep.py --job first)')
}

// Tag vocabularies — mirror vocab/tags.yaml. Defaults track core.yaml ids.
const actionTags = A.actionTags || [
  'explore-codebase', 'build-compile-fix-loop', 'fix-failing-tests', 'add-test-coverage',
  'debug-from-error-or-trace', 'refactor-rename-move', 'implement-from-spec', 'review-feedback',
  'verify-by-running', 'commit-or-pr', 'rebase-or-resolve-conflicts', 'branch-or-worktree-setup',
  'write-docs', 'update-config-or-deps', 'fan-out-agents-and-reconcile', 'visual-verification-loop',
]
const surfaceTags = A.surfaceTags || [
  'code', 'test', 'docs', 'proposal', 'config', 'ui', 'story', 'schema', 'ci', 'infra',
]
const scopeTags = A.scopeTags || ['single-file', 'cross-module', 'repo-wide']

const READER_BRIEF = `You are an intent-mining ORACLE over the user's REAL Claude Code session traces (distilled to compact action traces). For each trace you will SEGMENT it into INTENT SPANS and, per span, draft the concrete recipe that would reproduce what the user asked for.

## What an intent span is
A contiguous run of trace lines that serves ONE user request. A new \`USER:\` line that starts a genuinely new request begins a new span. Tool-result noise and AI narration belong to the span they serve.

## Trace format
Each line is one of:
- \`USER: ...\`        a human prompt (a span usually STARTS here)
- \`AI: ...\`          assistant narration
- \`  > Tool: arg\`    a tool call — THE reliable action signal

Trace lines are 1-BASED. When you cite a line, use its 1-based number.

## Per span, emit
- \`span\`: [firstLine, lastLine] (1-based, inclusive) — the line range in THIS trace.
- \`tags\`: multi-dimensional, drawn ONLY from the controlled vocab below. Use one or more per dimension; granularity comes from COMBINATIONS, not new labels. Never invent tags.
    - action  (what was asked): ${actionTags.join(', ')}
    - surface (what it touches): ${surfaceTags.join(', ')}
    - scope   (blast radius, optional): ${scopeTags.join(', ')}
- \`actions\`: the ORDERED recipe. For EVERY action:
    - \`tool\`: the EXACT tool name as it appears after \`>\` on the cited line (e.g. Bash, Edit, Read, Write).
    - \`signature\`: a GENERICIZED form of the call (e.g. "go test ./<pkg>/... -run <Test>"). Real shape, placeholders for specifics.
    - \`parameters\`: the concrete values, each of which MUST appear verbatim inside the cited line's argument (they will be checked).
    - \`cite\`: { "line": <1-based trace line this action came from> }. MANDATORY. Cite the EXACT line; a wrong citation drops the action.
- \`oracle_gates\` (ONLY if the recipe is not fully mechanical): the judgment forks, each { decision, validator } where validator describes the strict check (schema/assert/diff/review) that would confirm the choice.

## Hard discipline (you are a validated oracle, not a narrator)
- NEVER invent an action or a parameter. If you can't point to the exact trace line, DON'T emit it. Ungrounded actions are rejected downstream and only hurt the report.
- DO NOT emit the verbatim user text — only the span's line range. The verbatim text is recovered deterministically elsewhere.
- A trace with no real intents contributes zero spans. Don't manufacture.`

const ACTION_SCHEMA = {
  type: 'object', additionalProperties: false,
  properties: {
    tool: { type: 'string', description: 'exact tool name from the cited line' },
    signature: { type: 'string', description: 'genericized tool-call signature' },
    parameters: { type: 'object', additionalProperties: true, description: 'concrete values; each must be a substring of the cited arg' },
    cite: {
      type: 'object', additionalProperties: false,
      properties: { line: { type: 'integer', description: '1-based trace line' } },
      required: ['line'],
    },
  },
  required: ['tool', 'signature', 'parameters', 'cite'],
}

const SPAN_SCHEMA = {
  type: 'object', additionalProperties: false,
  properties: {
    session: { type: 'string', description: 'the source trace basename (.txt minus extension)' },
    span: { type: 'array', items: { type: 'integer' }, minItems: 2, maxItems: 2 },
    tags: {
      type: 'object', additionalProperties: false,
      properties: {
        action: { type: 'array', items: { type: 'string', enum: actionTags } },
        surface: { type: 'array', items: { type: 'string', enum: surfaceTags } },
        scope: { type: 'array', items: { type: 'string', enum: scopeTags } },
      },
      required: ['action'],
    },
    actions: { type: 'array', items: ACTION_SCHEMA },
    oracle_gates: {
      type: 'array',
      items: {
        type: 'object', additionalProperties: false,
        properties: { decision: { type: 'string' }, validator: { type: 'string' } },
        required: ['decision', 'validator'],
      },
    },
  },
  required: ['session', 'span', 'tags', 'actions'],
}

const READER_SCHEMA = {
  type: 'object', additionalProperties: false,
  properties: {
    traces_read: { type: 'integer' },
    records: {
      type: 'array',
      description: 'one entry per trace, each holding that trace\'s intent spans',
      items: {
        type: 'object', additionalProperties: false,
        properties: {
          session: { type: 'string' },
          spans: { type: 'array', items: SPAN_SCHEMA },
        },
        required: ['session', 'spans'],
      },
    },
  },
  required: ['traces_read', 'records'],
}

phase('Oracle')
const batchNums = Array.from({ length: batchCount }, (_, i) => i + 1)
const batchResults = await parallel(batchNums.map(n => () => {
  const manifest = `${batchDir}/batch-${String(n).padStart(2, '0')}.txt`
  return agent(
    `${READER_BRIEF}

Your batch manifest is at: ${manifest}
Read that file to get the list of distilled trace file paths. For EACH trace path, Read it IN FULL, segment it into intent spans, and draft each span's cited recipe per the discipline above. The \`session\` for a record is the trace's basename without the .txt extension. Return structured output.`,
    { label: `oracle:batch-${n}`, phase: 'Oracle', schema: READER_SCHEMA }
  )
}))

// Persist one raw oracle JSON per batch (ground.py reads the dir), and merge.
const allRecords = []
let tracesRead = 0
for (let i = 0; i < batchResults.length; i++) {
  const r = batchResults[i]
  if (!r) continue
  tracesRead += r.traces_read || 0
  const recs = r.records || []
  for (const rec of recs) allRecords.push(rec)
  // Workflow scripts have no filesystem access in most runtimes; only persist when
  // a writeFile hook is actually present. The records are ALWAYS returned below, so
  // a caller can persist the result itself and feed ground.py either way.
  if (outDir && typeof writeFile === 'function') {
    const path = `${outDir}/oracle-batch-${String(batchNums[i]).padStart(2, '0')}.json`
    try {
      await writeFile(path, JSON.stringify({ records: recs }, null, 2))
    } catch (e) {
      log(`writeFile unavailable (${e.message}); records returned instead`)
    }
  }
}

const spanCount = allRecords.reduce((a, r) => a + (r.spans || []).length, 0)
log(`Oracle: ${spanCount} intent spans across ${allRecords.length} traces (${tracesRead} read) -> ${outDir || '(not written)'}`)

return { tracesRead, traceCount: allRecords.length, spanCount, records: allRecords, outDir }
