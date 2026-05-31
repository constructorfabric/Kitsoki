package slotparse

import (
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/lex"
)

// TokenRange records which input tokens were consumed by a parse.
// Indices refer to positions in the [lex.Tokenize] output that the
// caller passed in. The range is half-open: tokens[Start:End].
//
// A parser may report multiple disjoint ranges (e.g. money "$120"
// consumes the "$" token plus the digit token); callers should treat
// the union of ranges as "the substring this parser eats."
type TokenRange struct {
	Start, End int // half-open: tokens[Start:End] were consumed
}

// Result is returned by every parser.
//
// Hard contracts. When OK is true, Consumed is non-empty and covers
// the consumed token ranges as half-open intervals; a parser may
// report multiple disjoint ranges (money "$120" eats the "$" token
// plus the digit token). When OK is false, Value and Consumed are
// zero-valued and callers fall through to the next strategy.
//
// Reason is a stable diagnostic string ("digit", "spelled",
// "synonym:rich guy", "fuzzy:banker", "dollar-sign", …). It exists
// for trace events and tests; callers must not branch on its exact
// format beyond the documented prefixes.
type Result struct {
	Value    any
	Consumed []TokenRange
	OK       bool
	Reason   string
}

// Parser is the common shape of every typed slot parser. Implementations
// are pure: Parse depends only on its arguments and produces no side
// effects. The slot argument carries type-specific metadata
// ([app.Slot.Values], [app.Slot.Synonyms]) — parsers may ignore it
// when their behaviour is type-free (int, money, bool).
type Parser interface {
	Parse(tokens []lex.Token, slot app.Slot) Result
}

// For returns the right parser for slot. Returns nil for unsupported
// types so callers can decide what to do.
//
// Recognised slot.Type values:
//
//   - "int"               → [ParseInt]
//   - "money" or "$int"   → [ParseMoney]
//   - "enum"              → [ParseEnum] (consumes slot.Values + slot.Synonyms)
//   - "bool"              → [ParseBool]
//   - "date"              → [ParseDate]
//   - "list[<inner>]"     → [ParseList] dispatching to For(<inner>);
//     the inner type is parsed off the suffix between the brackets, so
//     "list[int]" / "list[money]" / "list[enum]" all work. The inner
//     slot inherits Values/Synonyms from the outer slot so enum lists
//     can read their per-value synonym table.
//
// Anything else (including the empty string) returns nil. The caller
// is responsible for the type-not-supported branch; in the matcher
// this means "the slot can only be filled by the LLM" — see the slot
// parser table in docs/architecture/semantic-routing.md.
func For(slot app.Slot) Parser {
	switch slot.Type {
	case "int":
		return intParser{}
	case "money", "$int":
		return moneyParser{}
	case "enum":
		return enumParser{}
	case "bool":
		return boolParser{}
	case "date":
		return dateParser{}
	default:
		if inner, ok := parseListType(slot.Type); ok {
			// Construct a synthetic slot for the inner parser. The
			// outer slot's value/synonym tables thread through so
			// "list[enum]" keeps access to slot.Values + Synonyms;
			// the inner Type swap lets For recurse into the right
			// per-type parser.
			innerSlot := slot
			innerSlot.Type = inner
			ip := For(innerSlot)
			if ip == nil {
				return nil
			}
			return listParser{inner: ip, innerSlot: innerSlot}
		}
		return nil
	}
}

// parseListType extracts the inner type from a "list[<inner>]" string.
// Returns the inner type and true on a clean parse; returns "" and
// false on a non-list type or a malformed bracket pair.
func parseListType(t string) (string, bool) {
	const prefix = "list["
	if !strings.HasPrefix(t, prefix) || !strings.HasSuffix(t, "]") {
		return "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(t, prefix), "]")
	if inner == "" {
		return "", false
	}
	return inner, true
}

// --- adapter types ---------------------------------------------------
//
// Each parser exposes both a function (the documented direct entry
// point) and a thin interface-shaped adapter. The function keeps
// godoc clean; the adapter lets [For] hand back a Parser without
// closures-with-state.

type intParser struct{}

func (intParser) Parse(tokens []lex.Token, _ app.Slot) Result { return ParseInt(tokens) }

type moneyParser struct{}

func (moneyParser) Parse(tokens []lex.Token, _ app.Slot) Result { return ParseMoney(tokens) }

type enumParser struct{}

func (enumParser) Parse(tokens []lex.Token, slot app.Slot) Result { return ParseEnum(tokens, slot) }

type boolParser struct{}

func (boolParser) Parse(tokens []lex.Token, _ app.Slot) Result { return ParseBool(tokens) }

type dateParser struct{}

func (dateParser) Parse(tokens []lex.Token, _ app.Slot) Result { return ParseDate(tokens) }
