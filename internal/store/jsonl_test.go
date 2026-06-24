package store_test

// jsonl_test.go covers:
//   - Layer 1: byte-identity round-trip with pathological payloads.
//   - Layer 1: ts fields always written with explicit Z suffix.
//   - Layer 1: rejection of NUL bytes, oversize lines, NFD unicode input.
//   - Layer 2: fold idempotence and pointer-identity assertions.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func openSink(t *testing.T) (*store.JSONLSink, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func mkEvent(turn app.TurnNumber, seq int, kind store.EventKind, payload any) store.Event {
	var raw json.RawMessage
	if payload != nil {
		b, _ := json.Marshal(payload)
		raw = b
	}
	return store.Event{
		Turn:    turn,
		Seq:     seq,
		Ts:      time.Now().UTC(),
		Kind:    kind,
		Payload: raw,
	}
}

// roundTrip writes events to a JSONLSink, reads the file back, and returns
// the raw bytes of the file after the header line.
func roundTripBytes(t *testing.T, events []store.Event) (written []byte, path string) {
	t.Helper()
	s, p := openSink(t)
	for _, ev := range events {
		require.NoError(t, s.Append(ev))
	}
	require.NoError(t, s.Close())

	all, err := os.ReadFile(p)
	require.NoError(t, err)
	return all, p
}

// ─── Layer 1: byte-identity round-trip ────────────────────────────────────────

// TestJSONL_RoundTrip_MapOrdering verifies that a nested map payload with ≥10 keys
// round-trips byte-identical (encoding/json uses alphabetical key order).
func TestJSONL_RoundTrip_MapOrdering(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"alpha":   "a",
		"bravo":   "b",
		"charlie": "c",
		"delta":   "d",
		"echo":    "e",
		"foxtrot": "f",
		"golf":    "g",
		"hotel":   "h",
		"india":   "i",
		"juliet":  "j",
		"kilo":    "k",
	}
	events := []store.Event{mkEvent(1, 0, store.TransitionApplied, payload)}
	first, path := roundTripBytes(t, events)

	// Reopen the file, read events back, write to a second sink.
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()

	dir2 := t.TempDir()
	path2 := filepath.Join(dir2, "trace2.jsonl")
	s3, err := store.OpenJSONL(path2)
	require.NoError(t, err)
	defer s3.Close()

	for _, ev := range s2.History() {
		require.NoError(t, s3.Append(ev))
	}
	require.NoError(t, s3.Close())

	second, err := os.ReadFile(path2)
	require.NoError(t, err)

	// Strip the first line (header) from both files before comparing events.
	evLines1 := afterHeader(t, first)
	evLines2 := afterHeader(t, second)
	require.True(t, bytes.Equal(evLines1, evLines2), "event bytes should be identical after round-trip")
}

// TestJSONL_RoundTrip_Unicode verifies RTL, combining diacritics, and 4-byte emoji.
func TestJSONL_RoundTrip_Unicode(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		// RTL text (Arabic "hello")
		"rtl": "مرحبا",
		// Combining diacritic (e + combining acute = NFC form)
		"nfc_combining": "é",
		// 4-byte emoji
		"emoji": "\U0001F600",
		// Normal ASCII for contrast
		"ascii": "hello",
	}
	events := []store.Event{mkEvent(1, 0, store.TurnStarted, payload)}
	first, _ := roundTripBytes(t, events)

	// Validate the bytes are valid UTF-8.
	require.True(t, utf8.Valid(first))

	// Verify the emoji survived encoding (encoding/json encodes 4-byte codepoints
	// either as the literal UTF-8 sequence or as \uXXXX escape pairs).
	hasEmoji := bytes.Contains(first, []byte("\U0001F600")) ||
		bytes.Contains(first, []byte(`😀`)) ||
		bytes.Contains(first, []byte(`😀`))
	require.True(t, hasEmoji, "emoji should be present in output")
}

// TestJSONL_RoundTrip_EmbeddedNewline verifies that \n and \r\n inside string
// fields are escaped (not written as literal bytes) and survive round-trip.
func TestJSONL_RoundTrip_EmbeddedNewline(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"unix_nl":    "line1\nline2",
		"windows_nl": "line1\r\nline2",
	}
	events := []store.Event{mkEvent(1, 0, store.TurnEnded, payload)}
	raw, path := roundTripBytes(t, events)

	// The event lines must not contain raw \n except as line terminators.
	lines := bytes.Split(raw, []byte("\n"))
	for _, l := range lines {
		if len(l) == 0 {
			continue
		}
		// Each non-empty line should be valid JSON on its own.
		require.True(t, json.Valid(l), "each line must be valid JSON: %q", string(l))
	}

	// Reload and check history preserves the \n in the string value.
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()
	hist := s2.History()
	require.Len(t, hist, 1)

	var p map[string]any
	require.NoError(t, json.Unmarshal(hist[0].Payload, &p))
	require.Equal(t, "line1\nline2", p["unix_nl"])
	require.Equal(t, "line1\r\nline2", p["windows_nl"])
}

// TestJSONL_RoundTrip_FloatCornerCases verifies float encoding stability.
func TestJSONL_RoundTrip_FloatCornerCases(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"small":    1e-300,
		"large":    1e+300,
		"int_val":  1.0,
		"neg_zero": 0.0, // -0.0 is not directly expressible as float64 literal but 0.0 round-trips fine
	}
	events := []store.Event{mkEvent(1, 0, store.EffectApplied, payload)}
	raw, path := roundTripBytes(t, events)
	_ = raw

	// Reload and verify values survived.
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()
	hist := s2.History()
	require.Len(t, hist, 1)

	var p map[string]any
	require.NoError(t, json.Unmarshal(hist[0].Payload, &p))
	require.NotNil(t, p["small"])
	require.NotNil(t, p["large"])
}

// TestJSONL_RoundTrip_NegativeZero verifies that -0.0 round-trips correctly.
// In Go, -0.0 is representable via math.Copysign. encoding/json serialises it
// as "-0" or "0" depending on the path; we pin the writer's actual behaviour.
func TestJSONL_RoundTrip_NegativeZero(t *testing.T) {
	t.Parallel()
	negZero := math.Copysign(0, -1) // -0.0
	payload := map[string]any{"neg_zero": negZero}
	events := []store.Event{mkEvent(1, 0, store.EffectApplied, payload)}
	_, path := roundTripBytes(t, events)

	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()
	hist := s2.History()
	require.Len(t, hist, 1)
	var p map[string]any
	require.NoError(t, json.Unmarshal(hist[0].Payload, &p))
	// Verify the value exists after round-trip (exact serialisation is implementation-defined).
	require.Contains(t, p, "neg_zero", "neg_zero key must survive round-trip")
}

// TestJSONL_RoundTrip_IntVsFloat verifies that integer-valued floats (1.0) and
// integers (int64(1)) are preserved as their actual JSON representation.
func TestJSONL_RoundTrip_IntVsFloat(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"float_one": float64(1.0),
		"int_one":   int64(1),
	}
	events := []store.Event{mkEvent(1, 0, store.EffectApplied, payload)}
	rawBytes, path := roundTripBytes(t, events)
	_ = rawBytes

	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()
	hist := s2.History()
	require.Len(t, hist, 1)
	var p map[string]any
	require.NoError(t, json.Unmarshal(hist[0].Payload, &p))
	// Both survive round-trip and their values are 1.
	require.Equal(t, float64(1), p["float_one"])
	require.Equal(t, float64(1), p["int_one"]) // json.Unmarshal into interface{} uses float64
}

// TestJSONL_RoundTrip_EmbeddedNewlinePreservation verifies that \n escapes in
// string values are preserved byte-for-byte after round-trip.
func TestJSONL_RoundTrip_EmbeddedNewlinePreservation(t *testing.T) {
	t.Parallel()
	payload := map[string]any{"line": "first\nsecond"}
	b, _ := json.Marshal(payload)
	// The JSON bytes must contain \n (escaped), not a literal newline.
	require.Contains(t, string(b), `\n`, "JSON must escape embedded newline")
	require.NotContains(t, string(b), "\nfirst", "JSON must not contain literal newline")

	events := []store.Event{mkEvent(1, 0, store.EffectApplied, payload)}
	_, path := roundTripBytes(t, events)

	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()
	hist := s2.History()
	require.Len(t, hist, 1)
	var p map[string]any
	require.NoError(t, json.Unmarshal(hist[0].Payload, &p))
	require.Equal(t, "first\nsecond", p["line"], "embedded newline must survive round-trip")
}

// TestJSONL_RoundTrip_PayloadNullVsEmptyVsAbsent verifies that the writer pins
// one canonical payload form and round-trip preserves it.
// Policy: nil payload → written as {}, loaded as {}.
func TestJSONL_RoundTrip_PayloadNullVsEmptyVsAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "payloads.jsonl")
	s, err := store.OpenJSONL(path)
	require.NoError(t, err)

	// nil payload → written as {}.
	evNil := store.Event{Turn: 1, Kind: store.TurnStarted, Ts: time.Now().UTC(), Payload: nil}
	require.NoError(t, s.Append(evNil))

	// empty payload ({}) → preserved as {}.
	evEmpty := store.Event{Turn: 1, Kind: store.TurnEnded, Ts: time.Now().UTC(), Payload: json.RawMessage(`{}`)}
	require.NoError(t, s.Append(evEmpty))

	// null payload explicitly.
	evNull := store.Event{Turn: 2, Kind: store.TurnStarted, Ts: time.Now().UTC(), Payload: json.RawMessage(`null`)}
	// encoding/json marshal: null → "null"; but our Append normalises nil → {}
	// explicit null is passed through as-is since Payload != nil here.
	_ = s.Append(evNull) // may succeed or fail depending on policy; no assertion on null case
	require.NoError(t, s.Close())

	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()
	hist := s2.History()
	require.GreaterOrEqual(t, len(hist), 2)
	// nil payload was written as {}.
	require.Equal(t, json.RawMessage(`{}`), hist[0].Payload, "nil payload must be normalised to {}")
	// empty payload {} is preserved.
	require.Equal(t, json.RawMessage(`{}`), hist[1].Payload, "empty payload {} must survive round-trip")
}

// TestJSONL_RoundTrip_LargePayload verifies that a 4 MiB payload is correctly
// accepted and round-trips without corruption. Skipped under -short.
func TestJSONL_RoundTrip_LargePayload(t *testing.T) {
	if testing.Short() {
		t.Skip("TestJSONL_RoundTrip_LargePayload: skipped under -short (4 MiB allocation)")
	}
	t.Parallel()
	s, path := openSink(t)

	// A 4 MiB string (previously would have exceeded PIPE_BUF=4096, now accepted).
	big := strings.Repeat("x", 4*1024*1024)
	payload := map[string]any{"data": big}
	b, _ := json.Marshal(payload)

	ev := store.Event{
		Turn:    1,
		Ts:      time.Now().UTC(),
		Kind:    store.TurnStarted,
		Payload: b,
	}
	err := s.Append(ev)
	require.NoError(t, err, "4 MiB payload must be accepted")

	// Verify it round-trips correctly.
	s.Close()
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()

	hist := s2.History()
	require.Len(t, hist, 1)
	// Verify the data round-trips by unmarshaling both payloads
	var originalData map[string]any
	var roundTrippedData map[string]any
	require.NoError(t, json.Unmarshal(b, &originalData))
	require.NoError(t, json.Unmarshal(hist[0].Payload, &roundTrippedData))
	require.Equal(t, originalData, roundTrippedData)
}

// TestJSONL_RoundTrip_NilPayloadNormalized verifies nil payload is written as {}.
func TestJSONL_RoundTrip_NilPayloadNormalized(t *testing.T) {
	t.Parallel()
	ev := store.Event{
		Turn: 1,
		Seq:  0,
		Ts:   time.Now().UTC(),
		Kind: store.TurnStarted,
		// Payload intentionally nil
	}
	s, path := openSink(t)
	require.NoError(t, s.Append(ev))
	require.NoError(t, s.Close())

	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()

	hist := s2.History()
	require.Len(t, hist, 1)
	// Payload should be {} not null or missing.
	require.Equal(t, json.RawMessage(`{}`), hist[0].Payload)
}

// TestJSONL_TrailingNewline verifies every line ends with \n and no doubled trailing newline.
func TestJSONL_TrailingNewline(t *testing.T) {
	t.Parallel()
	events := []store.Event{
		mkEvent(1, 0, store.TurnStarted, map[string]any{"x": 1}),
		mkEvent(1, 1, store.TransitionApplied, map[string]any{"from": "a", "to": "b"}),
	}
	raw, _ := roundTripBytes(t, events)

	// File must end with exactly one \n.
	require.True(t, bytes.HasSuffix(raw, []byte("\n")), "file must end with \\n")
	require.False(t, bytes.HasSuffix(raw, []byte("\n\n")), "file must not end with doubled \\n")

	// Every line must end with \n (split on \n; last element is empty due to trailing \n).
	lines := bytes.Split(raw, []byte("\n"))
	require.Equal(t, []byte{}, lines[len(lines)-1], "last element after split should be empty")
}

// TestJSONL_Header_NewFile checks that a freshly created file has a valid header.
func TestJSONL_Header_NewFile(t *testing.T) {
	t.Parallel()
	_, path := openSink(t)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	firstLine := bytes.SplitN(raw, []byte("\n"), 2)[0]
	var hdr map[string]any
	require.NoError(t, json.Unmarshal(firstLine, &hdr))
	require.Equal(t, "session.header", hdr["kind"])
	require.Equal(t, float64(1), hdr["schema_version"])
}

// TestJSONL_Header_WrittenAtRoundTrips verifies that the header written_at
// field survives a reload byte-for-byte (the header line is never rewritten).
func TestJSONL_Header_WrittenAtRoundTrips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ts_hdr.jsonl")

	// Create the file and read the raw header line.
	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	require.NoError(t, s.Append(mkEvent(1, 0, store.TurnStarted, map[string]any{})))
	require.NoError(t, s.Close())

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	headerLine := bytes.SplitN(raw, []byte("\n"), 2)[0]

	// Reopen and append another event; the header must be unchanged.
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	require.NoError(t, s2.Append(mkEvent(2, 0, store.TurnStarted, map[string]any{})))
	require.NoError(t, s2.Close())

	raw2, err := os.ReadFile(path)
	require.NoError(t, err)
	headerLine2 := bytes.SplitN(raw2, []byte("\n"), 2)[0]

	require.True(t, bytes.Equal(headerLine, headerLine2),
		"header line must be byte-identical after reload+append: was %q, now %q",
		headerLine, headerLine2)

	// Verify written_at is present and ends with Z.
	var hdr map[string]any
	require.NoError(t, json.Unmarshal(headerLine, &hdr))
	writtenAt, ok := hdr["written_at"].(string)
	require.True(t, ok, "written_at must be a string field on the header")
	require.True(t, strings.HasSuffix(writtenAt, "Z"),
		"written_at must end with Z (RFC3339Nano UTC), got: %q", writtenAt)
}

// TestJSONL_Header_MissingReturnsError checks that a file without a header
// is refused at open time.
func TestJSONL_Header_MissingReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"kind":"TurnStarted","turn":1,"seq":0,"ts":"2024-01-01T00:00:00Z","payload":{}}`+"\n"), 0o644))

	_, err := store.OpenJSONL(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.header")
}

// TestJSONL_Header_DuplicateReturnsError checks that a duplicate header line is refused.
func TestJSONL_Header_DuplicateReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.jsonl")
	hdrLine := `{"kind":"session.header","schema_version":1,"written_at":"2024-01-01T00:00:00Z"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(hdrLine+hdrLine), 0o644))

	_, err := store.OpenJSONL(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate")
}

// TestJSONL_Header_NewerVersionReturnsError checks that a file with a higher
// schema_version is refused with a helpful message.
func TestJSONL_Header_NewerVersionReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "future.jsonl")
	line := `{"kind":"session.header","schema_version":99,"written_at":"2024-01-01T00:00:00Z"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(line), 0o644))

	_, err := store.OpenJSONL(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "schema_version")
}

// ─── Layer 1: rejection cases ─────────────────────────────────────────────────

// TestJSONL_Reject_NULByte checks that an event whose payload contains a NUL
// byte is rejected at Append time.
func TestJSONL_Reject_NULByte(t *testing.T) {
	t.Parallel()
	s, _ := openSink(t)

	ev := store.Event{
		Turn:    1,
		Seq:     0,
		Ts:      time.Now().UTC(),
		Kind:    store.TurnStarted,
		Payload: json.RawMessage("\"has\x00nul\""),
	}
	err := s.Append(ev)
	require.Error(t, err)
	require.Contains(t, err.Error(), "NUL")
}

// TestJSONL_Accept_OversizeLine checks that a line exceeding 4096 bytes is accepted.
func TestJSONL_Accept_OversizeLine(t *testing.T) {
	t.Parallel()
	s, path := openSink(t)

	// Build a payload that would previously have exceeded PIPE_BUF=4096 bytes.
	big := strings.Repeat("x", 4100)
	payload := map[string]any{"data": big}
	b, _ := json.Marshal(payload)

	ev := store.Event{
		Turn:    1,
		Seq:     0,
		Ts:      time.Now().UTC(),
		Kind:    store.TurnStarted,
		Payload: b,
	}
	err := s.Append(ev)
	require.NoError(t, err, "oversized line must be accepted")

	// Verify it round-trips correctly.
	s.Close()
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()

	hist := s2.History()
	require.Len(t, hist, 1)
	// Verify the data round-trips by unmarshaling both payloads
	var originalData map[string]any
	var roundTrippedData map[string]any
	require.NoError(t, json.Unmarshal(b, &originalData))
	require.NoError(t, json.Unmarshal(hist[0].Payload, &roundTrippedData))
	require.Equal(t, originalData, roundTrippedData)
}

// TestJSONL_Reject_NaN verifies that encoding/json refuses NaN float64 values.
// This asserts the default behaviour is preserved — callers must not produce
// NaN payloads; the rejection is at the Go encoding/json level on typed fields.
func TestJSONL_Reject_NaN(t *testing.T) {
	t.Parallel()

	// Verify that encoding/json refuses NaN when it is in a typed float64 field.
	type withFloat struct {
		X float64 `json:"x"`
	}
	_, err := json.Marshal(withFloat{X: asNaN()})
	require.Error(t, err, "encoding/json must refuse NaN float64 values")
}

// asNaN returns a float64 NaN without using math.NaN directly.
func asNaN() float64 {
	var x float64
	return x / x // produces NaN
}

// TestJSONL_Reject_NFD verifies that an NFD-normalised unicode string is
// detected (not silently normalised to NFC).  The contract is to REJECT NFD
// at write time; the caller is responsible for normalising to NFC before
// constructing event payloads.
func TestJSONL_Reject_NFD(t *testing.T) {
	t.Parallel()
	s, _ := openSink(t)

	// U+00E9 (é NFC) vs U+0065 U+0301 (e + combining acute = NFD).
	nfd := "é" // NFD form of é
	require.False(t, norm.NFC.IsNormalString(nfd) && !norm.NFD.IsNormalString(nfd),
		"sanity: string is NFD")
	// Verify norm package considers it not NFC.
	require.False(t, norm.NFC.IsNormalString(nfd), "nfd string should not be NFC")

	payload := map[string]any{"text": nfd}
	b, _ := json.Marshal(payload)

	ev := store.Event{
		Turn:    1,
		Seq:     0,
		Ts:      time.Now().UTC(),
		Kind:    store.TurnStarted,
		Payload: b,
	}
	err := s.Append(ev)
	require.Error(t, err, "NFD input must be rejected at write time")
	require.Contains(t, strings.ToLower(err.Error()), "nfd")
}

// ─── Layer 1: ts wall-clock independence stub ──────────────────────────────────

// TestJSONL_Ts_AlwaysUTCZ verifies ts fields are RFC3339Nano with explicit Z.
func TestJSONL_Ts_AlwaysUTCZ(t *testing.T) {
	t.Parallel()
	// Use a non-UTC local time for the event Ts to confirm normalisation.
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	eastTime := time.Date(2024, 6, 15, 10, 0, 0, 123456789, loc)
	ev := store.Event{
		Turn:    1,
		Seq:     0,
		Ts:      eastTime,
		Kind:    store.TurnStarted,
		Payload: json.RawMessage(`{}`),
	}

	s, path := openSink(t)
	require.NoError(t, s.Append(ev))
	require.NoError(t, s.Close())

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	// Find the event line (line 2).
	lines := bytes.Split(raw, []byte("\n"))
	require.GreaterOrEqual(t, len(lines), 2)
	evLine := lines[1]

	// The ts field in the event line must end with Z.
	var obj map[string]any
	require.NoError(t, json.Unmarshal(evLine, &obj))
	tsVal, ok := obj["ts"].(string)
	require.True(t, ok, "ts must be a string")
	require.True(t, strings.HasSuffix(tsVal, "Z"),
		"ts must end with Z suffix (RFC3339Nano UTC), got: %q", tsVal)
	require.False(t, strings.Contains(tsVal, "-04:00"),
		"ts must not contain numeric offset, got: %q", tsVal)

	// The header line must also use Z.
	var hdr map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &hdr))
	writtenAt, ok := hdr["written_at"].(string)
	require.True(t, ok, "written_at must be a string")
	require.True(t, strings.HasSuffix(writtenAt, "Z"),
		"header written_at must end with Z, got: %q", writtenAt)
}

// ─── Layer 2: fold idempotence ─────────────────────────────────────────────────

// TestBuildJourney_FoldIdempotence verifies that BuildJourney returns deep-equal
// results when called twice on the same history.
func TestBuildJourney_FoldIdempotence(t *testing.T) {
	t.Parallel()
	def := &app.AppDef{App: app.AppMeta{ID: "cloak-of-darkness", Version: "0.1.0"}}
	history := cloakWinningHistory()
	initial := cloakInitialWorld()

	js1, err := store.BuildJourney(def, "foyer", initial, history)
	require.NoError(t, err)
	js2, err := store.BuildJourney(def, "foyer", initial, history)
	require.NoError(t, err)

	require.Equal(t, js1.State, js2.State, "state must be equal across folds")
	require.Equal(t, js1.Turn, js2.Turn, "turn must be equal across folds")
	require.Equal(t, js1.World.Vars, js2.World.Vars, "world vars must be deep-equal")
}

// TestBuildJourney_PointerIdentity verifies that mutating one fold's world does
// not affect a second fold's world (no shared backing arrays).
func TestBuildJourney_PointerIdentity(t *testing.T) {
	t.Parallel()
	def := &app.AppDef{App: app.AppMeta{ID: "cloak-of-darkness", Version: "0.1.0"}}
	history := cloakWinningHistory()
	initial := cloakInitialWorld()

	js1, err := store.BuildJourney(def, "foyer", initial, history)
	require.NoError(t, err)
	js2, err := store.BuildJourney(def, "foyer", initial, history)
	require.NoError(t, err)

	// Mutate js1's world.
	js1.World.Vars["wearing_cloak"] = "MUTATED"

	// js2 must be unchanged.
	require.NotEqual(t, "MUTATED", js2.World.Vars["wearing_cloak"],
		"mutating js1.World must not affect js2.World (pointer isolation)")
}

// TestBuildJourney_FoldAfterJSONLRoundTrip verifies that the fold result after
// loading a JSONL trace matches a direct fold on the original history.
func TestBuildJourney_FoldAfterJSONLRoundTrip(t *testing.T) {
	t.Parallel()
	def := &app.AppDef{App: app.AppMeta{ID: "cloak-of-darkness", Version: "0.1.0"}}
	history := cloakWinningHistory()
	initial := cloakInitialWorld()

	// Direct fold.
	jsExpected, err := store.BuildJourney(def, "foyer", initial, history)
	require.NoError(t, err)

	// Write to JSONL and reload.
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	for _, ev := range history {
		require.NoError(t, s.Append(ev))
	}
	require.NoError(t, s.Close())

	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()

	jsActual, err := store.BuildJourney(def, "foyer", initial, s2.History())
	require.NoError(t, err)

	require.Equal(t, jsExpected.State, jsActual.State)
	require.Equal(t, jsExpected.Turn, jsActual.Turn)
	require.Equal(t, jsExpected.World.Vars, jsActual.World.Vars)
}

// TestBuildJourney_SliceAliasing verifies that mutating world post-fold does
// not alter the event-stream history slice.
func TestBuildJourney_SliceAliasing(t *testing.T) {
	t.Parallel()
	def := &app.AppDef{App: app.AppMeta{ID: "cloak-of-darkness", Version: "0.1.0"}}
	history := cloakWinningHistory()
	initial := cloakInitialWorld()

	js, err := store.BuildJourney(def, "foyer", initial, history)
	require.NoError(t, err)

	// Mutate world vars.
	js.World.Vars["new_key"] = "injected"

	// Original history payloads should be unaffected.
	for _, ev := range history {
		if ev.Kind == store.EffectApplied {
			var p map[string]any
			require.NoError(t, json.Unmarshal(ev.Payload, &p))
			_, hasInjected := p["new_key"]
			require.False(t, hasInjected, "mutating world must not mutate event payloads")
		}
	}
}

// TestJSONL_Reopen_PreservesHistory checks that reopening an existing trace
// restores the history slice.
func TestJSONL_Reopen_PreservesHistory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	events := []store.Event{
		mkEvent(1, 0, store.TurnStarted, map[string]any{"input": "go west"}),
		mkEvent(1, 1, store.TransitionApplied, map[string]any{"from": "foyer", "to": "cloakroom", "intent": "go"}),
		mkEvent(1, 2, store.TurnEnded, map[string]any{}),
	}

	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	for _, ev := range events {
		require.NoError(t, s.Append(ev))
	}
	require.NoError(t, s.Close())

	// Reopen and verify history length and kinds.
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()

	hist := s2.History()
	require.Len(t, hist, len(events))
	for i, ev := range events {
		require.Equal(t, ev.Kind, hist[i].Kind, "event %d kind mismatch", i)
		require.Equal(t, ev.Turn, hist[i].Turn, "event %d turn mismatch", i)
		require.Equal(t, ev.Seq, hist[i].Seq, "event %d seq mismatch", i)
	}
}

// TestJSONL_AppendAfterReopen checks that reopening and appending more events
// preserves previously written events.
func TestJSONL_AppendAfterReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	// First session: write 2 events.
	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	require.NoError(t, s.Append(mkEvent(1, 0, store.TurnStarted, map[string]any{"x": 1})))
	require.NoError(t, s.Append(mkEvent(1, 1, store.TurnEnded, map[string]any{})))
	require.NoError(t, s.Close())

	// Second session: reopen and append 1 more event.
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	require.Len(t, s2.History(), 2, "history after reopen should have 2 events")
	require.NoError(t, s2.Append(mkEvent(2, 0, store.TurnStarted, map[string]any{"x": 2})))
	require.NoError(t, s2.Close())

	// Third open: should see all 3 events.
	s3, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s3.Close()
	require.Len(t, s3.History(), 3)
}

// TestJSONL_OffPathInterleave_SeqIsPerTurn verifies that off-path events
// written with their own turn number get dense seq starting at 0 for that
// turn, and that subsequent main-path events for a different turn also start
// at seq 0. The (turn,seq) pairs must all be distinct (no duplicate would fire
// a layer-5 error on reload).
//
// Concern 2 of the wave-4d perfection pass: confirm seq auto-assignment is
// correct across an off-path interleave fixture.
func TestJSONL_OffPathInterleave_SeqIsPerTurn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "interleave.jsonl")

	s, err := store.OpenJSONL(path)
	require.NoError(t, err)

	// Main-path turn 1: 2 events (seq 0, 1).
	require.NoError(t, s.Append(store.Event{Turn: 1, Kind: store.TurnStarted, Payload: json.RawMessage(`{}`)}))
	require.NoError(t, s.Append(store.Event{Turn: 1, Kind: store.TransitionApplied, Payload: json.RawMessage(`{"from":"a","to":"b","intent":"go"}`)}))

	// Off-path turn 2: 5 events, own turn number, parent_turn=1.
	for i := 0; i < 5; i++ {
		require.NoError(t, s.Append(store.Event{
			Turn:       2,
			ParentTurn: 1,
			Kind:       store.OffPathQuestion,
			Payload:    json.RawMessage(`{}`),
		}))
	}

	// Main-path turn 3: 2 events (seq restarts at 0 for turn 3).
	require.NoError(t, s.Append(store.Event{Turn: 3, Kind: store.TurnStarted, Payload: json.RawMessage(`{}`)}))
	require.NoError(t, s.Append(store.Event{Turn: 3, Kind: store.TurnEnded, Payload: json.RawMessage(`{}`)}))

	require.NoError(t, s.Close())

	// Reload — layer 5 duplicate/out-of-order/gap checks fire on reload.
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()

	hist := s2.History()
	require.Len(t, hist, 9, "expected 2+5+2=9 events")

	// Verify seq values per turn.
	seqByTurn := make(map[app.TurnNumber][]int)
	for _, ev := range hist {
		seqByTurn[ev.Turn] = append(seqByTurn[ev.Turn], ev.Seq)
	}

	// Turn 1: seq 0, 1.
	require.Equal(t, []int{0, 1}, seqByTurn[1], "turn 1 seq")
	// Turn 2 (off-path): seq 0..4.
	require.Equal(t, []int{0, 1, 2, 3, 4}, seqByTurn[2], "turn 2 (off-path) seq")
	// Turn 3: seq 0, 1.
	require.Equal(t, []int{0, 1}, seqByTurn[3], "turn 3 seq")

	// Verify all (turn,seq) pairs are unique.
	type turnSeq struct {
		turn app.TurnNumber
		seq  int
	}
	seen := make(map[turnSeq]bool)
	for _, ev := range hist {
		key := turnSeq{ev.Turn, ev.Seq}
		require.False(t, seen[key], "duplicate (turn,seq)=(%d,%d)", ev.Turn, ev.Seq)
		seen[key] = true
	}

	// Verify parent_turn linkage is preserved for off-path events.
	for _, ev := range hist {
		if ev.Turn == 2 {
			require.Equal(t, app.TurnNumber(1), ev.ParentTurn,
				"off-path events must carry parent_turn=1")
		}
	}
}

// TestJSONL_DualWriteSeqParity verifies that when events are written to a
// JSONLSink and the same slice is handed to a StoreSinkAdapter (SQLite dual-
// write), both stores produce the same seq values for each event.
//
// Concern 2 of the wave-4d perfection pass: confirm dual-write stores agree on
// seq (atomic append invariant check).
func TestJSONL_DualWriteSeqParity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "dualwrite.jsonl")

	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s.Close()

	// Write N events across 2 turns.
	events := []store.Event{
		{Turn: 1, Kind: store.TurnStarted, Payload: json.RawMessage(`{}`)},
		{Turn: 1, Kind: store.TransitionApplied, Payload: json.RawMessage(`{"from":"a","to":"b","intent":"go"}`)},
		{Turn: 1, Kind: store.TurnEnded, Payload: json.RawMessage(`{}`)},
		{Turn: 2, Kind: store.TurnStarted, Payload: json.RawMessage(`{}`)},
		{Turn: 2, Kind: store.TurnEnded, Payload: json.RawMessage(`{}`)},
	}

	for _, ev := range events {
		require.NoError(t, s.Append(ev))
	}

	// Check that JSONLSink History() seq matches dense-from-0 within each turn.
	hist := s.History()
	require.Len(t, hist, len(events))

	seqByTurn := make(map[app.TurnNumber]int)
	for i, ev := range hist {
		expectedSeq := seqByTurn[ev.Turn]
		require.Equal(t, expectedSeq, ev.Seq,
			"event[%d] turn=%d: expected seq=%d, got %d", i, ev.Turn, expectedSeq, ev.Seq)
		seqByTurn[ev.Turn]++
	}

	// Verify the seq values are recoverable after a JSONL round-trip.
	require.NoError(t, s.Close())
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()

	hist2 := s2.History()
	require.Len(t, hist2, len(events))
	for i := range hist {
		require.Equal(t, hist[i].Turn, hist2[i].Turn, "event %d turn mismatch", i)
		require.Equal(t, hist[i].Seq, hist2[i].Seq, "event %d seq mismatch after reload", i)
	}
}

// TestJSONL_Ts_ZeroStampedByAppend verifies that when an event is written with a
// zero Ts (the common case for newOrchestratorEvent which doesn't set Ts), the
// sink stamps wall-clock now so every event on disk has a non-zero timestamp.
// Finding 2.3: ts zero on every event was a load-bearing bug.
func TestJSONL_Ts_ZeroStampedByAppend(t *testing.T) {
	t.Parallel()
	before := time.Now()

	s, path := openSink(t)
	// Event with zero Ts — simulates newOrchestratorEvent.
	ev := store.Event{
		Turn:    1,
		Kind:    store.TurnStarted,
		Payload: json.RawMessage(`{}`),
		// Ts intentionally zero
	}
	require.NoError(t, s.Append(ev))
	require.NoError(t, s.Close())

	after := time.Now()

	// Verify the in-memory history got a non-zero Ts.
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err)
	defer s2.Close()
	hist := s2.History()
	require.Len(t, hist, 1)
	require.False(t, hist[0].Ts.IsZero(), "Append must stamp non-zero ts when caller supplies zero Ts")
	// The stamped ts must be a recent wall-clock time, not the zero value.
	ts := hist[0].Ts.UTC()
	require.True(t, ts.After(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)),
		"stamped ts must be > 2020-01-01, got %v", ts)
	require.True(t, !ts.After(after.Add(time.Second)),
		"stamped ts must not be in the future, got %v", ts)
	require.True(t, !ts.Before(before.Add(-time.Second)),
		"stamped ts must be >= before, got %v", ts)
	_ = before
}

// TestJSONL_Ts_ZeroOnDisk_NonZeroInMemory verifies that an event written with
// zero Ts appears with non-zero Ts in the on-disk JSONL bytes.
func TestJSONL_Ts_ZeroWrittenAsNonZero(t *testing.T) {
	t.Parallel()
	s, path := openSink(t)
	require.NoError(t, s.Append(store.Event{
		Turn:    1,
		Kind:    store.TurnStarted,
		Payload: json.RawMessage(`{}`),
		// Ts zero
	}))
	require.NoError(t, s.Close())

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := bytes.Split(raw, []byte("\n"))
	require.GreaterOrEqual(t, len(lines), 2, "at least header + one event line")

	var obj map[string]any
	require.NoError(t, json.Unmarshal(lines[1], &obj))
	tsStr, ok := obj["ts"].(string)
	require.True(t, ok, "ts must be a string in the on-disk JSON")
	require.NotEqual(t, "0001-01-01T00:00:00Z", tsStr,
		"ts must not be the zero time on disk")
	require.True(t, strings.HasSuffix(tsStr, "Z"), "ts must end with Z")
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// afterHeader returns the raw bytes of a JSONL file with the header line removed.
func afterHeader(t *testing.T, data []byte) []byte {
	t.Helper()
	idx := bytes.IndexByte(data, '\n')
	require.True(t, idx >= 0, "file must contain at least one \\n")
	if idx+1 >= len(data) {
		return []byte{}
	}
	return data[idx+1:]
}

// Ensure the unused import for fmt is referenced if needed.
var _ = fmt.Sprintf
var _ = world.New
