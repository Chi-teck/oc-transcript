package app

import (
	"bytes"
	"strings"
	"testing"
)

// The legend's `where` column is relative to --root when a session sits under
// it, and stays absolute when it does not.
func TestLegendWhereRelativeToRoot(t *testing.T) {
	d := zooDB(t)
	t.Setenv("COLUMNS", "")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--db", d.path, "--all", "--root", "/proj", "--list"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	out := stdout.String()
	// Read the `where` cell out of each row rather than matching the padding
	// around it, so the assertion is about the path and not the furniture. The
	// top rule, the heads and their rule come first; the foot rule and the
	// footer last.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines[3 : len(lines)-2] {
		cols := strings.Split(strings.Trim(line, "│"), "│")
		if len(cols) != 7 {
			t.Fatalf("row %q has %d columns, want 7", line, len(cols))
		}
		if got := strings.TrimSpace(cols[1]); got != "." && got != "sub" {
			t.Errorf("where column is %q, want it relative to the root, in:\n%s", got, out)
		}
	}
	if strings.Contains(out, "/proj/sub") {
		t.Errorf("path under the root stayed absolute:\n%s", out)
	}
}

// The --list table ends on a foot rule and a line counting what is above it,
// back to the earliest row's date. The transcript's key gets neither.
func TestLegendFooter(t *testing.T) {
	d := zooDB(t)
	t.Setenv("COLUMNS", "")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--db", d.path, "--everywhere", "--all", "--list"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if foot := lines[len(lines)-2]; !strings.HasPrefix(foot, "└") || !strings.HasSuffix(foot, "┘") {
		t.Errorf("foot rule = %q, want the box closed off", foot)
	}
	// Indented to where the first column's text starts, not to the margin.
	if got, want := lines[len(lines)-1], "  3 sessions since 2026-08-17"; got != want {
		t.Errorf("footer = %q, want %q", got, want)
	}

	stdout.Reset()
	if err := run([]string{"--db", d.path, "--everywhere", "--all"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "sessions since") {
		t.Error("the transcript's key grew a footer")
	}
}

// tokenText keeps the magnitude and drops the digits nobody reads, in four
// cells or fewer.
func TestTokenText(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{1020, "1.0k"},
		{8242, "8.2k"},
		{9949, "9.9k"},
		{9950, "10k"}, // 9.95 is the cut-off: "10.0k" would be five cells
		{23499, "23k"},
		{104600, "105k"},
		{589674, "590k"},
		{999499, "999k"},
		{999600, "1.0M"},
		{1765712, "1.8M"},
		{3360202, "3.4M"},
		{9949999, "9.9M"},
		{9950000, "10M"},
	} {
		got := tokenText(c.n)
		if got != c.want {
			t.Errorf("tokenText(%d) = %q, want %q", c.n, got, c.want)
		}
		if cells(got) > 4 {
			t.Errorf("tokenText(%d) = %q, %d cells", c.n, got, cells(got))
		}
	}
}

// A session the store never accounted for gets a blank cell, not a fabricated
// zero: the two are different facts, and both are in the fixture.
func TestLegendBlankAndZero(t *testing.T) {
	unknown := &session{}
	if got := spanOf(unknown); got != "" {
		t.Errorf("span of an untouched session = %q, want blank", got)
	}
	if got := tokensOf(unknown); got != "" {
		t.Errorf("tokens of an unaccounted session = %q, want blank", got)
	}
	spent := &session{}
	spent.timeCreated, spent.timeUpdated, spent.hasTokens = 1000, 1000, true
	if got := spanOf(spent); got != "0s" {
		t.Errorf("span of a session touched at once = %q, want %q", got, "0s")
	}
	if got := tokensOf(spent); got != "0" {
		t.Errorf("tokens of a session that spent nothing = %q, want %q", got, "0")
	}
}
