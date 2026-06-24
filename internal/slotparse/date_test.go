// Tests for [ParseDate] and [ParseDateAt]. Structure mirrors
// int_test.go: a banner per behavioural block, table-driven sub-cases,
// year-rollover heuristic pinned to its own test, then property tests
// at the bottom. Every sub-test runs in parallel.
package slotparse

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// fixedNow is the reference point used in every deterministic test
// below. 2026-04-01 is a Wednesday — pinning a known weekday makes
// the "next monday" arithmetic checkable by hand.
var fixedNow = time.Date(2026, time.April, 1, 12, 30, 0, 0, time.UTC)

// utc is a tiny helper that builds a midnight-UTC time so the test
// table reads cleanly. Keeps line length down on the table rows.
func utc(year int, mon time.Month, day int) time.Time {
	return time.Date(year, mon, day, 0, 0, 0, 0, time.UTC)
}

// ====================== ParseDate: bare relative ======================

func TestParseDate_BareRelative(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		wantVal  time.Time
		wantReas string
	}{
		{"today", "today", utc(2026, time.April, 1), "date:today"},
		{"tomorrow", "tomorrow", utc(2026, time.April, 2), "date:tomorrow"},
		{"yesterday", "yesterday", utc(2026, time.March, 31), "date:yesterday"},
		{"leading_filler", "please tomorrow", utc(2026, time.April, 2), "date:tomorrow"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseDateAt(tok(t, tc.input), fixedNow)
			assertDate(t, tc.input, got, tc.wantVal, tc.wantReas)
		})
	}
}

// ====================== ParseDate: "in N {days,weeks}" ======================

func TestParseDate_InNDays(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		wantVal  time.Time
		wantReas string
	}{
		{"in_3_days", "in 3 days", utc(2026, time.April, 4), "date:in_days"},
		{"in_1_day", "in 1 day", utc(2026, time.April, 2), "date:in_days"},
		{"in_2_weeks", "in 2 weeks", utc(2026, time.April, 15), "date:in_days"},
		{"in_three_days", "in three days", utc(2026, time.April, 4), "date:in_days"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseDateAt(tok(t, tc.input), fixedNow)
			assertDate(t, tc.input, got, tc.wantVal, tc.wantReas)
		})
	}
}

// ====================== ParseDate: "next <weekday>" / "next week" ======================

func TestParseDate_NextWeekday(t *testing.T) {
	t.Parallel()
	// fixedNow = 2026-04-01 (Wednesday). Weekday cheat sheet:
	//   Sun=2026-04-05, Mon=2026-04-06, Tue=2026-04-07,
	//   Wed=2026-04-08, Thu=2026-04-02, Fri=2026-04-03,
	//   Sat=2026-04-04.
	// "next monday" = days from Wed→Mon, wrapping (Mon is later in
	// the same week direction, so 5 days out).
	tests := []struct {
		name    string
		input   string
		wantVal time.Time
		reason  string
	}{
		{"next_monday", "next monday", utc(2026, time.April, 6), "date:next_weekday"},
		{"next_sunday", "next sunday", utc(2026, time.April, 5), "date:next_weekday"},
		{"next_friday", "next friday", utc(2026, time.April, 3), "date:next_weekday"},
		{"next_wednesday", "next wednesday", utc(2026, time.April, 8), "date:next_weekday"}, // 7 days out
		{"next_week", "next week", utc(2026, time.April, 8), "date:next_week"},
		{"last_week", "last week", utc(2026, time.March, 25), "date:last_week"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseDateAt(tok(t, tc.input), fixedNow)
			assertDate(t, tc.input, got, tc.wantVal, tc.reason)
		})
	}
}

// ====================== ParseDate: month-name forms ======================

func TestParseDate_MonthDay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		wantVal  time.Time
		wantReas string
	}{
		// Bare month+day with year inferred to the current year (the
		// resulting date is in the future from fixedNow).
		{"april_15_future", "april 15", utc(2026, time.April, 15), "date:month_day"},
		{"may_1_future", "may 1", utc(2026, time.May, 1), "date:month_day"},

		// Month+day+year explicit.
		{"march_15_2026", "march 15, 2026", utc(2026, time.March, 15), "date:month_day_year"},
		{"jan_1_2030", "jan 1 2030", utc(2030, time.January, 1), "date:month_day_year"},

		// Abbreviated month name.
		{"mar_3", "mar 3", utc(2027, time.March, 3), "date:month_day"}, // 2026-03-03 is past → rolls to 2027
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseDateAt(tok(t, tc.input), fixedNow)
			assertDate(t, tc.input, got, tc.wantVal, tc.wantReas)
		})
	}
}

// TestParseDate_YearRollover pins the month-day rollover promise: with now=2026-04-01
// and input "march 3", value=2027-03-03 (because march 2026 already
// passed).
func TestParseDate_YearRollover(t *testing.T) {
	t.Parallel()
	got := ParseDateAt(tok(t, "march 3"), fixedNow)
	want := utc(2027, time.March, 3)
	if !got.OK {
		t.Fatalf("want OK, got miss: %+v", got)
	}
	gv, _ := got.Value.(time.Time)
	if !gv.Equal(want) {
		t.Errorf("got %s, want %s (year-rollover heuristic)", gv.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

// ====================== ParseDate: ISO + slash forms ======================

func TestParseDate_ISOForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		wantVal time.Time
	}{
		{"iso_2026_03_15", "2026-03-15", utc(2026, time.March, 15)},
		{"slash_3_15_2026", "3/15/2026", utc(2026, time.March, 15)},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseDateAt(tok(t, tc.input), fixedNow)
			if !got.OK {
				t.Fatalf("want OK, got miss: %+v", got)
			}
			gv, ok := got.Value.(time.Time)
			if !ok {
				t.Fatalf("Value type %T, want time.Time", got.Value)
			}
			if !gv.Equal(tc.wantVal) {
				t.Errorf("got %s, want %s", gv.Format("2006-01-02"), tc.wantVal.Format("2006-01-02"))
			}
			if got.Reason == "" || !strings.HasPrefix(got.Reason, "date:") {
				t.Errorf("Reason=%q, want date:* prefix", got.Reason)
			}
		})
	}
}

// ====================== ParseDate: misses ======================

func TestParseDate_Misses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"only_words", "purple cats sing"},
		{"month_no_day", "march"},
		{"bare_number", "42"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseDateAt(tok(t, tc.input), fixedNow)
			if got.OK {
				t.Errorf("want miss for %q, got %+v", tc.input, got)
			}
		})
	}
}

// ====================== ParseDate: non-At wrapper smoke ======================

// TestParseDate_NonAtWrapper exercises the [ParseDate] (no -At suffix)
// entry point so we know it forwards to ParseDateAt with time.Now().
// Sub-day precision is irrelevant — we only check that "today"
// resolves to a date within ±1 day of the current calendar date.
func TestParseDate_NonAtWrapper(t *testing.T) {
	t.Parallel()
	got := ParseDate(tok(t, "today"))
	if !got.OK {
		t.Fatalf("ParseDate(today) miss: %+v", got)
	}
	gv, ok := got.Value.(time.Time)
	if !ok {
		t.Fatalf("Value type %T, want time.Time", got.Value)
	}
	now := time.Now().UTC()
	diff := gv.Sub(utc(now.Year(), now.Month(), now.Day()))
	if diff > 24*time.Hour || diff < -24*time.Hour {
		t.Errorf("ParseDate(today)=%s; want within ±1 day of %s", gv, now)
	}
}

// ====================== Property: every recognised pattern returns a stable Reason prefix ======================

func TestParseDate_ReasonPrefixes(t *testing.T) {
	t.Parallel()
	corpora := map[string]string{
		"today":          "date:today",
		"tomorrow":       "date:tomorrow",
		"yesterday":      "date:yesterday",
		"in 3 days":      "date:in_days",
		"next monday":    "date:next_weekday",
		"next week":      "date:next_week",
		"march 3":        "date:month_day",
		"march 15, 2026": "date:month_day_year",
		"2026-03-15":     "date:iso",
	}
	for in, wantReas := range corpora {
		in, wantReas := in, wantReas
		t.Run(fmt.Sprintf("input=%q", in), func(t *testing.T) {
			t.Parallel()
			got := ParseDateAt(tok(t, in), fixedNow)
			if !got.OK {
				t.Fatalf("%q: want OK, got miss", in)
			}
			if got.Reason != wantReas {
				t.Errorf("%q: Reason=%q, want %q", in, got.Reason, wantReas)
			}
		})
	}
}

// assertDate is the shared assertion used by every table above. Keeps
// the table rows compact and renders dates as ISO strings on failure
// for readability.
func assertDate(t *testing.T, input string, got Result, wantVal time.Time, wantReas string) {
	t.Helper()
	if !got.OK {
		t.Fatalf("ParseDate(%q): want OK, got miss: %+v", input, got)
	}
	gv, ok := got.Value.(time.Time)
	if !ok {
		t.Fatalf("ParseDate(%q): Value type %T, want time.Time", input, got.Value)
	}
	if !gv.Equal(wantVal) {
		t.Errorf("ParseDate(%q): got %s, want %s", input, gv.Format("2006-01-02"), wantVal.Format("2006-01-02"))
	}
	if wantReas != "" && got.Reason != wantReas {
		t.Errorf("ParseDate(%q): Reason=%q, want %q", input, got.Reason, wantReas)
	}
	if len(got.Consumed) == 0 {
		t.Errorf("ParseDate(%q): Consumed empty on OK=true", input)
	}
}
