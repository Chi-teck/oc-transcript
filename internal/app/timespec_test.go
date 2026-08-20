package app

import (
	"strconv"
	"testing"
	"time"
)

func TestParseWhen(t *testing.T) {
	loc := time.FixedZone("+05", 5*3600)
	now := time.Date(2026, 8, 18, 15, 4, 5, 0, loc)
	ms := func(t time.Time) int64 { return t.UnixMilli() }

	cases := []struct {
		spec string
		end  bool
		want int64
	}{
		{"2h", false, ms(now.Add(-2 * time.Hour))},
		{"30m", false, ms(now.Add(-30 * time.Minute))},
		{"1.5h", false, ms(now.Add(-90 * time.Minute))},
		{"3d", false, ms(now.Add(-72 * time.Hour))},
		{"1w", false, ms(now.Add(-7 * 24 * time.Hour))},
		{"45s", false, ms(now.Add(-45 * time.Second))},
		{"2H", false, ms(now.Add(-2 * time.Hour))},
		{" 2h ", false, ms(now.Add(-2 * time.Hour))},
		{"today", false, ms(time.Date(2026, 8, 18, 0, 0, 0, 0, loc))},
		{"today", true, ms(time.Date(2026, 8, 19, 0, 0, 0, 0, loc))},
		{"yesterday", false, ms(time.Date(2026, 8, 17, 0, 0, 0, 0, loc))},
		{"yesterday", true, ms(time.Date(2026, 8, 18, 0, 0, 0, 0, loc))},
		{"2026-08-18", false, ms(time.Date(2026, 8, 18, 0, 0, 0, 0, loc))},
		// A bare date as --until means the end of that day — but only the
		// 10-char form.
		{"2026-08-18", true, ms(time.Date(2026, 8, 19, 0, 0, 0, 0, loc))},
		{"20260818", true, ms(time.Date(2026, 8, 18, 0, 0, 0, 0, loc))},
		{"2026-08-18T12:30", false, ms(time.Date(2026, 8, 18, 12, 30, 0, 0, loc))},
		{"2026-08-18 12:30:05", false, ms(time.Date(2026, 8, 18, 12, 30, 5, 0, loc))},
		{"2026-08-18T12:30:05.25", false, ms(time.Date(2026, 8, 18, 12, 30, 5, 250e6, loc))},
		{"2026-08-18T12:30:05.123456789", false, ms(time.Date(2026, 8, 18, 12, 30, 5, 123456000, loc))},
		{"2026-08-18T12:30:05+02:00", false, ms(time.Date(2026, 8, 18, 12, 30, 5, 0, time.FixedZone("", 2*3600)))},
		{"2026-08-18T12:30:05Z", false, ms(time.Date(2026, 8, 18, 12, 30, 5, 0, time.UTC))},
		{"2026-08-18T12:30:05-0330", false, ms(time.Date(2026, 8, 18, 12, 30, 5, 0, time.FixedZone("", -(3*3600+30*60))))},
		{"2026-08-18T1230", false, ms(time.Date(2026, 8, 18, 12, 30, 0, 0, loc))},
	}
	for _, c := range cases {
		got, err := parseWhen(c.spec, c.end, now)
		if err != nil {
			t.Errorf("parseWhen(%q, end=%v): %v", c.spec, c.end, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseWhen(%q, end=%v) = %d (%s), want %d (%s)",
				c.spec, c.end, got, time.UnixMilli(got).In(loc), c.want, time.UnixMilli(c.want).In(loc))
		}
	}
}

func TestParseWhenErrors(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 4, 5, 0, time.UTC)
	for _, spec := range []string{
		"nope", "2026-13-01", "2026-02-30", "2026-0818", "5x", "2026-08-18T25:00",
		"2026-08-18T12:61", "", "h", "2026-08-18X12:30",
	} {
		_, err := parseWhen(spec, false, now)
		if err == nil {
			t.Errorf("parseWhen(%q) unexpectedly parsed", spec)
			continue
		}
		want := "cannot parse time " + strconv.Quote(spec) + " — use 2h, 30m, 3d, today, yesterday or an ISO timestamp"
		if err.Error() != want {
			t.Errorf("parseWhen(%q) error = %q, want %q", spec, err.Error(), want)
		}
	}
}

// spanText picks one unit and never spends more than four cells on it — the
// column is budgeted for a magnitude, not for a duration.
func TestSpanText(t *testing.T) {
	sec := func(f float64) int64 { return int64(f * 1000) }
	for _, c := range []struct {
		ms   int64
		want string
	}{
		{-1000, "0s"},
		{0, "0s"},
		{sec(3), "3s"},
		{sec(59.9), "59s"},
		{sec(60), "1m"},
		{sec(110), "2m"},
		{sec(445), "7m"},
		{sec(59.4 * 60), "59m"},
		{sec(59.9 * 60), "1.0h"}, // never "60m": the unit hands over at half a unit
		{sec(3600), "1.0h"},
		{sec(6707), "1.9h"},
		{sec(9.96 * 3600), "10h"}, // 9.95 is the cut-off: "10.0h" would be five cells
		{sec(23.4 * 3600), "23h"},
		{sec(23.99 * 3600), "1.0d"}, // never "24h", for the same reason
		{sec(86400), "1.0d"},
		{sec(160037), "1.9d"},
		{sec(9358645), "108d"},
	} {
		got := spanText(c.ms)
		if got != c.want {
			t.Errorf("spanText(%d) = %q, want %q", c.ms, got, c.want)
		}
		if cells(got) > 4 {
			t.Errorf("spanText(%d) = %q, %d cells", c.ms, got, cells(got))
		}
	}
}
