Right now, we use LLM harnesses such as Claude Code, Codex and others as a "free for all" chat with a single context.

I want to create a deterministic orchestrator that uses the LLM APIs/harnesses (claude -p for the PoC) to allow free-text interaction with a structured application.

The goal is to replace the traditional CLI interactions, which requires strict formatting and memorization of parameters with a tool that works in predictable ways, within a deterministic framework.

The user is provided a CLI interface similar to Claude Code (I think OpenCode may be a better starting point for us though) that works as a hybrid between a traditional shell and chat agent.

The application is defined as possible paths (directed possibly-cyclic graph), which are allowed states and transitions to allow for wizard-like cases where a user progresses through a "journey".

The app provides a stateful orchestration, and the mcp uses this application state to understand the path.  The workflow or path traversal is persistent in memory (and can be snapshotted to a file on disk for investigation or replay).  The mcp uses the minimal trust model with the LLM - use it for as little as possible and only where needed.

This same fundamental system should work like old DOS-based text adventure games, and we can use one as a testing and development application.

The application becomes a state machine, and the LLM is explicitly called with the user input, and required to make an mcp tool call to convert the user's free-form expression into an allowed intent.  If the human input is somehow incomplete or invalid, the tool will respond with an LLM suggestion about what information is needed or other helpful feedback.

Some applications may choose to support a free-form interaction mode that would just use the underlying chat agent (e.g. claude -p) but clearly, visually indicate that you're "not on the path".

We also need a visual representation of the path.

This is a workflow tool, where human and LLM work together through a deterministic path, with deterministic gates and validation through a deterministic state machine or possible paths.

I want to implement the tool in Golang for various reasons, so we must focus on CLI/TUI or start from some simple Vue 3 web API instead if that is easier.

We need the chat mechanism, the method to specify states/paths/transitions/etc..., the gate schemas.

~/code/cyber-repo/tools/loopy has some of these concepts, but it is a deterministic script that does a bug-fix using many orchestrated calls to claude -p for new or resumed conversations.  It also uses the wiggum mcp to collect and validate feedback.  we should use a similar mechanism, that states with required inputs are transitioned via mcp calls with the appropriate parameters.  If the LLM produces invalid json/yaml response - the mcp error provides detail for the LLM to fix its next attempt.
