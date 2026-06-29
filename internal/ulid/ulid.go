package ulid

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"
)

// crockford is the Crockford Base32 alphabet: 32 symbols, no I/L/O/U so the
// encoding survives transcription and is case-insensitive on decode. Index i
// is the digit for the 5-bit value i, which is what makes [encodeCrockford] a
// plain table lookup.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// The ULID layout. A ULID is a 128-bit value split into a time half and a
// randomness half, each encoded with [encodeCrockford]. The constants below
// pin every width the encoder relies on; changing one without the others
// silently corrupts the layout, so they are stated once and reused.
const (
	// BitsPerChar is the information carried by one Crockford Base32
	// character (log2 of the 32-symbol alphabet).
	BitsPerChar = 5

	// TimestampBits is the millisecond Unix timestamp width. 48 bits
	// (≈8925 CE before wraparound) is the canonical ULID timestamp size.
	TimestampBits = 48
	// TimestampChars is how many Crockford characters the timestamp half
	// occupies. ceil(48/5) = 10.
	TimestampChars = 10

	// RandomnessBytes is the width of the random half in bytes (80 bits),
	// the canonical ULID randomness size.
	RandomnessBytes = 10
	// RandomnessChars is how many Crockford characters the random half
	// occupies. The 80 bits are encoded in two runs (see [New]): 64 bits
	// → 13 chars and 16 bits → 3 chars, ceil(80/5) = 16 total.
	RandomnessChars = 16

	// Length is the total character width of an encoded ULID: TimestampChars
	// + RandomnessChars. Every ULID this package emits is exactly this long,
	// and [IsValid] requires it.
	Length = TimestampChars + RandomnessChars
)

// charMask selects the low BitsPerChar bits of a value — the next Crockford
// digit during encoding.
const charMask = (1 << BitsPerChar) - 1

// Split of the 80-bit random half into the two integer-sized runs [New]
// encodes. crypto/rand fills RandomnessBytes; the first randHi64Bytes feed a
// uint64 (13 chars) and the rest feed a uint16 (3 chars).
const (
	randHi64Bytes = 8 // 64 bits → randHi64Chars
	randHi64Chars = 13
	randLo16Chars = 3
)

// readRandom is the randomness source [New] draws from. It is a package
// variable (defaulting to [crypto/rand.Read]) purely so tests can substitute a
// failing reader to exercise the panic path; production code never reassigns
// it.
var readRandom = rand.Read

// mu serialises [New] so that two goroutines never race on the encode buffer
// reasoning below; randomness alone already makes collisions improbable, so
// the lock exists purely to keep New a clean, allocation-light hot path that
// is safe to call from anywhere without the caller reasoning about it.
var mu sync.Mutex

// New returns a freshly generated ULID as a [Length]-character Crockford
// Base32 string. The leading [TimestampChars] encode the current wall-clock
// time in milliseconds, so IDs minted in timestamp order also sort in
// lexicographic order — the property callers rely on to page chats and jobs
// by creation time without a separate sort key.
//
// New is safe for concurrent use. It panics only if the operating system
// cannot supply cryptographic randomness ([crypto/rand] failure), which on a
// healthy host does not happen; a panic here signals a broken environment, not
// a recoverable condition, so callers are not asked to handle an error.
func New() string {
	mu.Lock()
	defer mu.Unlock()

	// Time half: millisecond Unix timestamp in the low TimestampBits.
	ms := uint64(time.Now().UnixMilli())

	// Random half: RandomnessBytes of cryptographic randomness.
	var random [RandomnessBytes]byte
	if _, err := readRandom(random[:]); err != nil {
		panic(fmt.Sprintf("ulid: rand.Read: %v", err))
	}

	var buf [Length]byte
	encodeCrockford(buf[:TimestampChars], ms, TimestampChars)
	// The 80 random bits do not fit one uint64, so encode them in two runs:
	// the high 64 bits then the low 16 bits.
	rnd64 := binary.BigEndian.Uint64(random[:randHi64Bytes])
	rnd16 := binary.BigEndian.Uint16(random[randHi64Bytes:])
	encodeCrockford(buf[TimestampChars:TimestampChars+randHi64Chars], rnd64, randHi64Chars)
	encodeCrockford(buf[TimestampChars+randHi64Chars:], uint64(rnd16), randLo16Chars)

	return string(buf[:])
}

// encodeCrockford writes the low BitsPerChar*n bits of v into dst as n
// Crockford Base32 characters, most-significant digit first. dst must be
// exactly n bytes; encoding fills it right-to-left so the natural string order
// is also the numeric order, which is what gives ULIDs their sortability.
func encodeCrockford(dst []byte, v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		dst[i] = crockford[v&charMask]
		v >>= BitsPerChar
	}
}

// IsValid reports whether s could have been produced by [New]: it must be
// exactly [Length] characters and every character must be in the Crockford
// alphabet. The check is case-insensitive (s is upper-cased first) because
// Crockford Base32 decodes case-insensitively, so a lower-cased ULID is still
// a valid ULID. IsValid does not verify that the timestamp half is a plausible
// time — it is a shape check, not a provenance check.
func IsValid(s string) bool {
	if len(s) != Length {
		return false
	}
	upper := strings.ToUpper(s)
	for _, c := range upper {
		if !strings.ContainsRune(crockford, c) {
			return false
		}
	}
	return true
}
