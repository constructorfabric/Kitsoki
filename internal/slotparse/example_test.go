// Runnable godoc examples. Each Example function's // Output: block
// is verified by `go test -run "^Example" ./internal/slotparse/...`,
// so the documentation cannot drift out of sync with the parsers.
package slotparse_test

import (
	"fmt"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/lex"
	"kitsoki/internal/slotparse"
)

// parseIntAdapter wraps the package-level [slotparse.ParseInt]
// function in a [slotparse.Parser] so the [ExampleParseList] example
// has a clean inner-parser handle to pass in. The adapter exists in
// _test.go (not in the public surface) because public callers should
// reach for For(Slot{Type:"list[int]"}) instead — the example
// deliberately shows the explicit-inner path so authors reading the
// godoc see how the building blocks fit together.
type parseIntAdapter struct{}

func (parseIntAdapter) Parse(tokens []lex.Token, _ app.Slot) slotparse.Result {
	return slotparse.ParseInt(tokens)
}

// ExampleParseInt shows ParseInt picking up a spelled multi-word
// cardinal embedded in a sentence with leading filler. Returns
// Reason="spelled" and a Consumed range pointing at "two hundred
// fifty".
func ExampleParseInt() {
	toks := lex.Tokenize("please buy two hundred fifty oxen", nil)
	r := slotparse.ParseInt(toks)
	fmt.Printf("value=%v reason=%s ok=%v\n", r.Value, r.Reason, r.OK)
	// Output:
	// value=250 reason=spelled ok=true
}

// ExampleParseEnum demonstrates the synonym-tier worked example: a
// slot with per-value synonyms maps "rich guy" → "banker".
func ExampleParseEnum() {
	slot := app.Slot{
		Type:   "enum",
		Values: []string{"banker", "carpenter", "farmer"},
		Synonyms: map[string][]string{
			"banker":    {"banker", "rich guy", "money man"},
			"carpenter": {"carpenter", "builder"},
		},
	}
	toks := lex.Tokenize("i want to be the rich guy", nil)
	r := slotparse.ParseEnum(toks, slot)
	fmt.Printf("value=%v reason=%s ok=%v\n", r.Value, r.Reason, r.OK)
	// Output:
	// value=banker reason=synonym:rich guy ok=true
}

// ExampleParseMoney shows the parser folding "$120.50" into 121
// whole dollars via math.Round's round-half-away-from-zero rule.
// Because the live lex layer drops "$" as pure-punctuation the
// Reason ends up "bare-int" — see the package doc.
func ExampleParseMoney() {
	toks := lex.Tokenize("$120.50", nil)
	r := slotparse.ParseMoney(toks)
	fmt.Printf("value=%v reason=%s ok=%v\n", r.Value, r.Reason, r.OK)
	// Output:
	// value=121 reason=bare-int ok=true
}

// ExampleParseList demonstrates the list worked example: a comma-
// and "and"-separated run of ints round-trips into a []any. The
// inner parser is shared with the bare [slotparse.ParseInt] entry
// point through a small adapter (see parseIntAdapter in this file).
func ExampleParseList() {
	toks := lex.Tokenize("6, 12, and 3", nil)
	r := slotparse.ParseList(toks, parseIntAdapter{})
	fmt.Printf("value=%v reason=%s ok=%v\n", r.Value, r.Reason, r.OK)
	// Output:
	// value=[6 12 3] reason=list:digit ok=true
}

// ExampleParseDate shows the year-rollover heuristic: "march 3"
// without a year resolves to NEXT march if the current calendar
// year's march 3 is already in the past. We pin a reference time so
// the doc example is deterministic year-round.
func ExampleParseDate() {
	now := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	toks := lex.Tokenize("march 3", nil)
	r := slotparse.ParseDateAt(toks, now)
	v, _ := r.Value.(time.Time)
	fmt.Printf("value=%s reason=%s ok=%v\n", v.Format("2006-01-02"), r.Reason, r.OK)
	// Output:
	// value=2027-03-03 reason=date:month_day ok=true
}
