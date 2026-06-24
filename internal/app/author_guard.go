package app

import "strings"

// ReadsWorldKeyInGuard reports whether any guard expression in the app — a
// transition `when:`, an effect `when:`, or a slot `validator:` — references the
// named world key as a bare identifier (e.g. `slots.last_reply_author in
// world.allowed_authors`). It is the deterministic signal the web surface uses
// to decide whether a story enforces an author ACL: declaring the world key is
// not enough (stories/bugfix declares `allowed_authors` but never reads it) —
// the key must actually appear in a guard for it to gate a turn.
//
// Matching is identifier-aware: the key must appear bounded by non-identifier
// characters, so `allowed_authors` does not match `allowed_authors_extra`. It is
// intentionally conservative — a guard that reads the key in any branch counts.
func (d *AppDef) ReadsWorldKeyInGuard(key string) bool {
	if d == nil || key == "" {
		return false
	}
	for _, s := range d.States {
		if stateReadsKeyInGuard(s, key) {
			return true
		}
	}
	return false
}

func stateReadsKeyInGuard(s *State, key string) bool {
	if s == nil {
		return false
	}
	for _, transitions := range s.On {
		for _, t := range transitions {
			if guardRefsKey([]string{t.When}, key) {
				return true
			}
			if effectsReadKey(t.Effects, key) {
				return true
			}
		}
	}
	if effectsReadKey(s.OnEnter, key) {
		return true
	}
	for _, in := range s.Intents {
		for _, sl := range in.Slots {
			if guardRefsKey([]string{sl.Validator}, key) {
				return true
			}
		}
	}
	for _, child := range s.States {
		if stateReadsKeyInGuard(child, key) {
			return true
		}
	}
	return false
}

func effectsReadKey(effects []Effect, key string) bool {
	for _, e := range effects {
		if guardRefsKey([]string{e.When}, key) {
			return true
		}
	}
	return false
}

// guardRefsKey reports whether any expression in exprs references key as a
// whole identifier token.
func guardRefsKey(exprs []string, key string) bool {
	for _, expr := range exprs {
		if expr == "" {
			continue
		}
		idx := 0
		for {
			at := strings.Index(expr[idx:], key)
			if at < 0 {
				break
			}
			at += idx
			before := at == 0 || !isIdentRune(rune(expr[at-1]))
			endPos := at + len(key)
			after := endPos >= len(expr) || !isIdentRune(rune(expr[endPos]))
			if before && after {
				return true
			}
			idx = at + len(key)
		}
	}
	return false
}

func isIdentRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
