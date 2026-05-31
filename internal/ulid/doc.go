// Package ulid is the leaf identifier generator: it mints lexicographically
// sortable, time-ordered string IDs for the stores that need them (notably
// [kitsoki/internal/chats] and [kitsoki/internal/jobs]), so that records sort
// by creation time without carrying a separate sort key. It depends only on
// the standard library.
//
// A ULID is a 128-bit value rendered as a 26-character Crockford Base32
// string: a 48-bit millisecond timestamp followed by 80 bits of cryptographic
// randomness. The timestamp prefix is what makes the string sortable — IDs
// minted later sort after IDs minted earlier — while the random suffix keeps
// IDs minted within the same millisecond distinct.
//
// # Algorithm
//
// [New] builds an ID in two halves and concatenates them:
//
//  1. Time half — the current Unix time in milliseconds (a 48-bit value)
//     is encoded into the leading [TimestampChars] characters.
//  2. Random half — [RandomnessBytes] of crypto/rand entropy fill the
//     trailing [RandomnessChars] characters. Because 80 bits do not fit one
//     uint64, the bytes are split into a high 64-bit run (13 chars) and a low
//     16-bit run (3 chars) before encoding.
//
// Both halves go through [encodeCrockford], which emits [BitsPerChar] bits per
// character, most-significant digit first. Writing the most significant digit
// first is the whole trick: the natural left-to-right string order equals the
// numeric order, so string comparison of two ULIDs orders them by timestamp.
//
// # Invariants
//
//   - Every emitted ID is exactly [Length] characters; [IsValid] enforces this.
//   - All characters come from the [Crockford Base32] alphabet, which omits
//     I, L, O, and U to resist transcription errors and decodes
//     case-insensitively.
//   - IDs minted in non-decreasing timestamp order sort in non-decreasing
//     lexicographic order. Within a single millisecond, ordering between two
//     IDs is random (this generator does not implement monotonic
//     within-millisecond counters).
//
// # Worked example
//
// Encoding the canonical ULID timestamp 2016-07-30T23:36:16.385Z
// (1469918176385 ms) gives the same 10-character prefix the ULID spec shows;
// the random half differs every call:
//
//	now:    2016-07-30T23:36:16.385Z
//	ms:     1469918176385
//	time half (10 chars):   01ARYZ6S41
//	random: 10 crypto/rand bytes -> 16 chars, e.g. TSV4RRFFQ69G5FAV
//	result: 01ARYZ6S41TSV4RRFFQ69G5FAV   (IsValid -> true)
//
// A runnable form of the validity round-trip lives in [ExampleNew]; the shape
// checks live in [ExampleIsValid].
//
// # Lifecycle
//
// There is no setup or teardown: [New] and [IsValid] are package-level
// functions with no constructor and no shared state a caller can see. New
// serialises internally and is safe to call from any goroutine; IsValid is a
// pure function. Callers typically call New once per record at insert time and
// store the result verbatim.
//
// # Non-goals
//
//   - No new module dependency. The generator is built on crypto/rand,
//     encoding/binary, and time so that adding sortable IDs never drags in a
//     third-party ULID/UUID library — the whole package is under 150 lines a
//     reviewer can audit.
//   - No configurable output length or alphabet. [Length] is fixed at 26 and
//     the alphabet is fixed Crockford Base32, because a sortable ID is only
//     interchangeable across the codebase if every producer emits the same
//     shape.
//   - No monotonic within-millisecond counter. Two IDs minted in the same
//     millisecond are ordered randomly relative to each other; callers that
//     need strict per-millisecond ordering must add their own tiebreak. The
//     80-bit random suffix already makes same-millisecond collisions
//     negligible, which is the property the stores actually rely on.
//   - No decode/parse API. Nothing in the codebase needs to read the timestamp
//     back out of an ID, so the package only generates and shape-validates.
//   - No learned or probabilistic components. The layout is fixed constants
//     (see [Length] and siblings), not tuned values.
package ulid
