package inbound

import "strings"

// PrefixClassifier is the deterministic, no-LLM default Classifier. It reads the
// first non-empty line of a reply and maps a leading command word to a checkpoint
// intent — the same shape the external loop.py driver uses to read bot commands.
// It recognises:
//
//	continue | approve | lgtm | ok        → intent "continue"   (no slots)
//	refine: <feedback> | refine <feedback> → intent "refine"     {refine_feedback}
//	restart_from <state>                   → intent "restart_from" {target}
//	jump_to <state>                        → intent "jump_to"     {target}
//
// Matching is case-insensitive on the command word. Anything else returns
// ok=false so the bridge skips it (idle ticket chatter is not a turn). The
// intent/slot vocabulary mirrors stories/bugfix's checkpoint intents; a story
// with a different vocabulary supplies its own Classifier.
type PrefixClassifier struct{}

// continueWords are the bare approvals that map to the "continue" checkpoint
// intent.
var continueWords = map[string]struct{}{
	"continue": {}, "approve": {}, "approved": {}, "lgtm": {}, "ok": {}, "okay": {},
}

func (PrefixClassifier) Classify(body string) (Classification, bool) {
	line := firstNonEmptyLine(body)
	if line == "" {
		return Classification{}, false
	}
	word, rest := splitWord(line)
	lower := strings.ToLower(strings.TrimRight(word, ":"))

	switch {
	case isContinueWord(lower, word):
		return Classification{Intent: "continue"}, true
	case lower == "refine":
		feedback := strings.TrimSpace(rest)
		return Classification{Intent: "refine", Slots: map[string]any{"refine_feedback": feedback}}, true
	case lower == "restart_from":
		target := strings.TrimSpace(rest)
		if target == "" {
			return Classification{}, false
		}
		return Classification{Intent: "restart_from", Slots: map[string]any{"target": target}}, true
	case lower == "jump_to":
		target := strings.TrimSpace(rest)
		if target == "" {
			return Classification{}, false
		}
		return Classification{Intent: "jump_to", Slots: map[string]any{"target": target}}, true
	default:
		return Classification{}, false
	}
}

// isContinueWord matches the bare approval words, but only when the whole first
// word is the approval (so "continue please" still approves, but "continueish"
// does not — splitWord already isolated the first token).
func isContinueWord(lower, word string) bool {
	if strings.HasSuffix(word, ":") {
		return false
	}
	_, ok := continueWords[lower]
	return ok
}

func firstNonEmptyLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// splitWord splits the first whitespace-delimited token from the rest.
func splitWord(s string) (word, rest string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}
