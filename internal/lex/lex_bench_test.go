// Benchmarks for the lex public surface. Pinned here so contributors can
// confirm the "sub-millisecond signature" claim from
// docs/architecture/semantic-routing.md without digging into the codebase.
//
// Run:
//
//	go test -bench=. -benchmem ./internal/lex/...
//
// Typical numbers on modern hardware (laptop x86_64): Tokenize ~3-10 µs
// for a short utterance, Signature ~5-15 µs. The budget is "<1 ms" so we
// have ~100x headroom.
package lex

import "testing"

const (
	benchShort = "buy 6 oxen and 200 lbs of food"
	benchLong  = "let's please just go ahead and buy six oxen and two hundred lbs of food for two hundred forty dollars including some bullets a wagon and the spare wheel from the general store in independence"
)

func BenchmarkTokenize_Short(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Tokenize(benchShort, nil)
	}
}

func BenchmarkTokenize_Long(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Tokenize(benchLong, nil)
	}
}

func BenchmarkSignature_Short(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Signature(benchShort, nil)
	}
}

func BenchmarkSignature_Long(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Signature(benchLong, nil)
	}
}
