// Benchmarks for the slotparse public surface. Pinned so contributors
// can measure the routing-tier latency claim ("~3 µs" per synonym
// hit; see docs/architecture/semantic-routing.md) without re-deriving
// the numbers from a clean tree.
//
// Run:
//
//	go test -bench=. -benchmem ./internal/slotparse/...
//
// Typical numbers on modern x86_64 hardware: ParseInt_Digit and
// ParseEnum_Direct sit at sub-microsecond after Tokenize is
// subtracted; ParseInt_Spelled adds a SpelledNumber pass and ends up
// at ~1-3 µs. None of the parsers should allocate per call beyond
// the Consumed slice header.
package slotparse

import (
	"testing"

	"kitsoki/internal/lex"
)

// Pre-tokenise the bench inputs once, outside the timed loop. This
// isolates the parser's cost from lex's; the orchestrator already
// caches token lists per turn anyway (the turn cache in
// docs/architecture/semantic-routing.md).
var (
	benchIntDigit   = lex.Tokenize("six", nil)
	benchIntSpelled = lex.Tokenize("two hundred fifty", nil)
	benchMoney      = lex.Tokenize("120 dollars", nil)
	benchEnumDirect = lex.Tokenize("banker", nil)
	benchEnumFuzzy  = lex.Tokenize("bankr", nil)
	benchEnumSyn    = lex.Tokenize("rich guy", nil)
	benchBool       = lex.Tokenize("yes", nil)
	benchEnumSlot   = fixtureProfessionSlot()
)

func BenchmarkParseInt_Digit(b *testing.B) {
	bench := lex.Tokenize("6", nil)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseInt(bench)
	}
}

func BenchmarkParseInt_Spelled(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseInt(benchIntSpelled)
	}
}

func BenchmarkParseInt_SpelledSingleWord(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseInt(benchIntDigit) // "six" — single-token spelled
	}
}

func BenchmarkParseMoney_Unit(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseMoney(benchMoney)
	}
}

func BenchmarkParseEnum_Direct(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseEnum(benchEnumDirect, benchEnumSlot)
	}
}

func BenchmarkParseEnum_Synonym(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseEnum(benchEnumSyn, benchEnumSlot)
	}
}

func BenchmarkParseEnum_Fuzzy(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseEnum(benchEnumFuzzy, benchEnumSlot)
	}
}

func BenchmarkParseBool(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseBool(benchBool)
	}
}

var (
	benchListInts = lex.Tokenize("6, 12, and 3", nil)
	benchListLong = lex.Tokenize("1 2 3 4 5 6 7 8 9 10", nil)
	benchDateRel  = lex.Tokenize("tomorrow", nil)
	benchDateMD   = lex.Tokenize("march 3", nil)
	benchDateISO  = lex.Tokenize("2026-03-15", nil)
)

func BenchmarkParseList_Ints(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseList(benchListInts, intParser{})
	}
}

func BenchmarkParseList_LongInts(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseList(benchListLong, intParser{})
	}
}

func BenchmarkParseDate_Relative(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseDate(benchDateRel)
	}
}

func BenchmarkParseDate_MonthDay(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseDate(benchDateMD)
	}
}

func BenchmarkParseDate_ISO(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParseDate(benchDateISO)
	}
}
