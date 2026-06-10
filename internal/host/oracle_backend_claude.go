// Package host — the claude oracleBackend.
//
// claudeBackend is the identity implementation of oracleBackend: it preserves
// exactly the binary resolution, argv, prompt-on-stdin delivery, and Anthropic
// stream-json parsing that the oracle path used before the backend seam existed.
// Every method delegates to the pre-existing helpers (resolveClaudeBin,
// classifyStreamEvent, assistantToolUses, claudeTranscriptFormat,
// "mcp__<server>__submit"), so swapping the seam in is a behavior-preserving
// refactor and the flagship stories see no change.
package host

import "context"

// claudeBackend drives Anthropic's `claude` CLI. It is the default backend.
type claudeBackend struct{}

func (claudeBackend) Name() string { return "claude" }

func (claudeBackend) ResolveBin(ctx context.Context) (string, error) {
	return resolveClaudeBin(ctx)
}

// TranslateInvocation is the identity: the verb handlers already build claude
// argv with the prompt on stdin, so there is nothing to rewrite.
func (claudeBackend) TranslateInvocation(claudeArgs []string, stdin, workingDir string) Invocation {
	return Invocation{Args: claudeArgs, Stdin: stdin, WorkingDir: workingDir}
}

// Classify reproduces the field extraction that emitStreamEvent + the scan
// loops performed inline before the backend seam, reading the Anthropic
// stream-json event shapes.
func (claudeBackend) Classify(ev map[string]any) classifiedEvent {
	text, tool, toolArgs, isResult, resultText, sid := classifyStreamEvent(ev)
	evType, _ := ev["type"].(string)
	subtype, _ := ev["subtype"].(string)
	ce := classifiedEvent{
		Type:       evType,
		Subtype:    subtype,
		Text:       text,
		Tool:       tool,
		ToolArgs:   toolArgs,
		Tools:      assistantToolUses(ev),
		IsResult:   isResult,
		ResultText: resultText,
		SessionID:  sid,
	}
	if isResult {
		ce.Usage, ce.Cost = resultEventUsage(ev)
		ce.IsError, _ = ev["is_error"].(bool)
	}
	return ce
}

func (claudeBackend) TranscriptFormat() string { return claudeTranscriptFormat }

func (claudeBackend) ValidatorToolName(server string) string {
	return "mcp__" + server + "__submit"
}

func (claudeBackend) runnerFromContext(ctx context.Context) ClaudeRunner {
	return ClaudeRunnerFromContext(ctx)
}
