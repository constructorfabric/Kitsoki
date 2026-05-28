# Oracle Event attrs Shape

This document defines the canonical `attrs` shape for `oracle.*` trace events.
It is the contract between the Go backend (which emits these events) and the
runstatus UI (which renders them).

## Event naming

```
oracle.<verb>.start     lightweight; emitted when the LLM call begins
oracle.<verb>.complete  full; emitted when the LLM call returns
```

Where `<verb>` is one of: `decide`, `extract`, `ask`, `task`, `converse`.

### oracle.\<verb\>.start attrs

| Key    | Type   | Notes                                      |
|--------|--------|--------------------------------------------|
| verb   | string | one of the five verbs above                |
| model  | string | e.g. `"claude-3-sonnet"`                   |
| agent  | string | agent name, e.g. `"reproducing_specialist"`|

Purpose: give the UI early visibility that an LLM call is in flight before the
(potentially long) response arrives.

---

## oracle.\<verb\>.complete attrs

All keys below are present on every `.complete` event unless marked `?`
(optional — omit the key entirely when not applicable, do not send `null`).

### Common fields (all verbs)

| Key             | Type   | Notes                                                         |
|-----------------|--------|---------------------------------------------------------------|
| verb            | string | "decide" \| "extract" \| "ask" \| "task" \| "converse"       |
| agent           | string | Agent name, e.g. `"reproducing_specialist"`                   |
| model           | string | LLM model identifier, e.g. `"claude-3-sonnet"`                |
| duration_ms     | number | Wall-clock ms from call start to response received            |
| prompt_tokens   | number | Tokens in the rendered prompt (input side)                    |
| response_tokens | number | Tokens in the model response (output side)                    |
| cost_usd?       | number | Estimated USD cost; omit if unavailable                       |
| system_prompt   | string | Full text of the system prompt sent to the model. Cap: 64KB. If truncated, append the marker ` [TRUNCATED]` at the end. |
| prompt          | string | Full text of the user prompt sent to the model. Cap: 64KB. If truncated, append ` [TRUNCATED]`. |
| error?          | string | If the call failed, the error message. Other response fields are absent when error is present. |

### input object

Verb-specific input provided to the oracle operator. Only the keys relevant
to the verb are present.

```jsonc
"input": {
  // extract only
  "schema": { ... },                // JSON Schema the operator will validate against

  // decide only
  "choices": [
    { "id": "string", "description": "string" }
  ],

  // task and ask
  "instructions": "string",         // the imperative task/question given to the agent

  // task only
  "files_in": ["path/to/file.go"],  // file paths loaded as initial context

  // converse only
  "messages": [
    { "role": "user" | "assistant", "content": "string" }
  ]
}
```

### response object

Holds the model's structured output. Only the keys relevant to the verb are
present.

| Key        | Verbs              | Notes                                                             |
|------------|--------------------|-------------------------------------------------------------------|
| text       | ask, converse      | Free-text reply from the model                                    |
| json       | decide, extract    | Raw structured response before post-processing                    |
| decision   | decide             | The chosen choice id from `input.choices`                         |
| extracted  | extract            | The extracted payload as a typed object matching `input.schema`   |
| intent     | ask                | Resolved intent name, e.g. `"start"`                             |
| slots      | ask                | Key-value map of extracted slot values                            |

### tool_calls (task only)

Array of tool invocations made by the task agent during execution. Ordered by
`seq` ascending.

```jsonc
"tool_calls": [
  {
    "seq":         1,          // 1-based call sequence number
    "tool":        "Read",     // tool name: Read | Edit | Write | Bash | Grep | ...
    "args":        { ... },    // tool-specific argument object
    "result":      "string",   // truncated stdout / return value; cap 2KB per call. Append " [TRUNCATED]" if cut.
    "duration_ms": 120,        // wall-clock ms; omit if unavailable
    "error":       "string"    // omit if call succeeded
  }
]
```

### files_changed (task only)

Array of files modified during the task. Each entry carries the full unified
diff so the UI can render an inline diff view.

```jsonc
"files_changed": [
  {
    "path":      "workerpool/dispatcher.go",
    "status":    "added" | "modified" | "deleted",
    "additions": 12,
    "deletions": 3,
    "diff":      "--- a/workerpool/dispatcher.go\n+++ ..."
                 // unified diff body; no a/b/ prefix on paths. Cap: 64KB per file.
                 // Append " [TRUNCATED]" to the diff string if cut.
  }
]
```

---

## Size caps and truncation

| Field                      | Cap    | Truncation marker |
|----------------------------|--------|-------------------|
| system_prompt              | 64 KB  | ` [TRUNCATED]`    |
| prompt                     | 64 KB  | ` [TRUNCATED]`    |
| response.text              | 64 KB  | ` [TRUNCATED]`    |
| tool_calls[].result        | 2 KB   | ` [TRUNCATED]`    |
| files_changed[].diff       | 64 KB  | ` [TRUNCATED]`    |

When a string is truncated, the last bytes are replaced by ` [TRUNCATED]` so
the field remains valid UTF-8 and the UI can display a truncation notice.

---

## Worked examples per verb

### oracle.decide.complete

```json
{
  "verb": "decide",
  "agent": "strategy_router",
  "model": "claude-3-sonnet",
  "duration_ms": 840,
  "prompt_tokens": 512,
  "response_tokens": 18,
  "cost_usd": 0.0009,
  "system_prompt": "You are a strategy router for the bugfix pipeline...",
  "prompt": "The user reported: 'race condition in worker pool on shutdown'. Choose the best next step.",
  "input": {
    "choices": [
      { "id": "reproduce_locally", "description": "Attempt to reproduce the bug in a local workspace." },
      { "id": "ask_user", "description": "Ask the user for more information before proceeding." }
    ]
  },
  "response": {
    "json": { "choice": "reproduce_locally", "confidence": 0.92 },
    "decision": "reproduce_locally"
  }
}
```

### oracle.extract.complete

```json
{
  "verb": "extract",
  "agent": "intake_extractor",
  "model": "claude-3-sonnet",
  "duration_ms": 620,
  "prompt_tokens": 380,
  "response_tokens": 45,
  "system_prompt": "Extract structured fields from the user message according to the schema.",
  "prompt": "User message: 'BUG-4711: Race condition in worker pool on shutdown. Severity: high.'",
  "input": {
    "schema": {
      "type": "object",
      "properties": {
        "ticket_id": { "type": "string" },
        "summary":   { "type": "string" },
        "severity":  { "type": "string", "enum": ["low", "medium", "high", "critical"] }
      },
      "required": ["ticket_id", "summary", "severity"]
    }
  },
  "response": {
    "json": { "ticket_id": "BUG-4711", "summary": "Race condition in worker pool on shutdown", "severity": "high" },
    "extracted": { "ticket_id": "BUG-4711", "summary": "Race condition in worker pool on shutdown", "severity": "high" }
  }
}
```

### oracle.ask.complete

```json
{
  "verb": "ask",
  "agent": "idle_router",
  "model": "claude-3-sonnet",
  "duration_ms": 510,
  "prompt_tokens": 420,
  "response_tokens": 24,
  "system_prompt": "You are the intent router for the bugfix pipeline idle state. Classify the user message into one of the registered intents and extract any slots.",
  "prompt": "User: start BUG-4711",
  "input": {
    "instructions": "Classify the user message into one of: start, quit, restart_from, quick."
  },
  "response": {
    "text": "Intent: start",
    "intent": "start",
    "slots": { "ticket_id": "BUG-4711" }
  }
}
```

### oracle.task.complete

```json
{
  "verb": "task",
  "agent": "reproducing_specialist",
  "model": "claude-3-sonnet",
  "duration_ms": 22400,
  "prompt_tokens": 2400,
  "response_tokens": 1820,
  "cost_usd": 0.018,
  "system_prompt": "You are the reproducing specialist agent...",
  "prompt": "Reproduce BUG-4711: race condition in worker pool on shutdown. Workspace: /workspace/BUG-4711.",
  "input": {
    "instructions": "Reproduce the reported bug. Write a failing test that demonstrates the race. Run the test to confirm it fails.",
    "files_in": ["workerpool/dispatcher.go", "workerpool/worker.go"]
  },
  "response": {
    "text": "Found the race: Dispatcher.shutdown() closes the done channel without holding the mu lock, while worker goroutines read it under mu. Added TestDispatcherShutdownRace — it fails reliably under -race."
  },
  "tool_calls": [
    {
      "seq": 1,
      "tool": "Read",
      "args": { "file_path": "workerpool/dispatcher.go" },
      "result": "package workerpool\n\nfunc (d *Dispatcher) shutdown() {\n\tclose(d.done)\n}\n...",
      "duration_ms": 12
    },
    {
      "seq": 2,
      "tool": "Bash",
      "args": { "command": "go test -race ./workerpool/... 2>&1 | tail -20" },
      "result": "--- FAIL: TestDispatcherShutdownRace (0.12s)\n    race detected during execution of test",
      "duration_ms": 3100
    }
  ],
  "files_changed": [
    {
      "path": "workerpool/dispatcher_test.go",
      "status": "modified",
      "additions": 18,
      "deletions": 0,
      "diff": "--- workerpool/dispatcher_test.go\n+++ workerpool/dispatcher_test.go\n@@ -42,0 +43,18 @@\n+func TestDispatcherShutdownRace(t *testing.T) {\n+\td := NewDispatcher(4)\n+\td.Start()\n+\tvar wg sync.WaitGroup\n+\tfor i := 0; i < 8; i++ {\n+\t\twg.Add(1)\n+\t\tgo func() {\n+\t\t\tdefer wg.Done()\n+\t\t\td.Dispatch(noopTask)\n+\t\t}()\n+\t}\n+\twg.Wait()\n+\td.Shutdown()\n+}"
    }
  ]
}
```

### oracle.converse.complete

```json
{
  "verb": "converse",
  "agent": "clarification_agent",
  "model": "claude-3-sonnet",
  "duration_ms": 3200,
  "prompt_tokens": 680,
  "response_tokens": 142,
  "system_prompt": "You are a clarification agent. Ask the user targeted questions to resolve ambiguity before the fix is proposed.",
  "prompt": "We need to clarify the expected shutdown behaviour before proposing a fix.",
  "input": {
    "messages": [
      { "role": "assistant", "content": "Should Dispatcher.Shutdown() wait for in-flight tasks to complete, or cancel them immediately?" },
      { "role": "user",      "content": "It should drain in-flight tasks with a timeout of 5 seconds, then force-cancel." },
      { "role": "assistant", "content": "Understood. Should the timeout be configurable per dispatcher instance or a global constant?" }
    ]
  },
  "response": {
    "text": "Configurable per instance, defaulting to 5 seconds."
  }
}
```

---

## Error response example

When `error` is set, the `response`, `tool_calls`, and `files_changed` keys
are omitted entirely:

```json
{
  "verb": "task",
  "agent": "reproducing_specialist",
  "model": "claude-3-sonnet",
  "duration_ms": 1200,
  "prompt_tokens": 2400,
  "response_tokens": 0,
  "system_prompt": "...",
  "prompt": "...",
  "input": { "instructions": "..." },
  "error": "context deadline exceeded after 1200ms"
}
```
