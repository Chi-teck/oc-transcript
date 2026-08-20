package app

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestBudget(t *testing.T) {
	// Three fixed columns: 13+2 + 12+2 + 11+2 = 42 cells spoken for, plus the
	// gutter between the two flexible ones.
	fixed := []int{13, 12, 11}
	nat := func(w, tt int) []int { return append(slices.Clone(fixed), w, tt) }
	total := func(ws []int) int {
		sum := tableGutter * (len(ws) - 1)
		for _, w := range ws {
			sum += w
		}
		return sum
	}

	// Natural fit: nothing is squeezed.
	if got := budget(100, nat(20, 30), 3, 4); !slices.Equal(got, nat(20, 30)) {
		t.Errorf("natural fit: %v", got)
	}
	// `where` squeezed: room is 80-42-2 = 36, so `where` gets 45%% of it and
	// `title` the rest.
	if got := budget(80, nat(120, 60), 3, 4); got[3] != 16 || got[4] != 20 {
		t.Errorf("squeezed: %v", got)
	}
	// Both at their floors: room 16 cannot hold 12+14, `where` yields first.
	if got := budget(60, nat(120, 60), 3, 4); got[3] != 2 || got[4] != 14 || total(got) > 60 {
		t.Errorf("floors: %v (total %d)", got, total(got))
	}
	// Zero room: no panic, both flexible columns keep at least one cell.
	if got := budget(30, nat(120, 60), 3, 4); got[3] < 1 || got[4] < 1 {
		t.Errorf("zero room: %v", got)
	}

	// The flexible pair is named, not positional. This is the --list table's own
	// shape: seven columns with `where` second and `title` last, five fixed ones
	// between them. Room is 90-56-2 = 32, `where` bids 45%% of it and the room
	// `title` does not need comes back — and the row lands on the width exactly.
	wide := []int{12, 40, 16, 4, 8, 6, 60}
	if got := budget(90, wide, 1, 6); got[1] != 14 || got[6] != 18 || total(got) != 90 {
		t.Errorf("named pair: %v (total %d)", got, total(got))
	}
}

func TestTable(t *testing.T) {
	heads, rows, widths := []string{"A", "B"}, [][]string{{"x", "yy"}, {"zzz", ""}}, []int{3, 2}
	got := table(heads, rows, widths, 40, Paint{})
	want := []string{"A    B", "x    yy", "zzz"}
	if !slices.Equal(got, want) {
		t.Errorf("table = %q, want %q", got, want)
	}
	// Painted, only the heads take a hue, and only the word in each — never
	// the padding after it, which the trim at the end still has to see.
	lit := table(heads, rows, widths, 40, Paint{enabled: true})
	if lit[0] != fg(headColor, "1")+"A"+reset+"    "+fg(headColor, "1")+"B"+reset {
		t.Errorf("painted heads = %q", lit[0])
	}
	if !slices.Equal(lit[1:], got[1:]) {
		t.Errorf("painted rows = %q, want %q", lit[1:], got[1:])
	}
}

func TestFramedTable(t *testing.T) {
	heads, rows, widths := []string{"A", "B"}, [][]string{{"x", "yy"}, {"zzz", ""}}, []int{3, 2}
	got := framedTable(heads, rows, widths, 40, Paint{})
	// A closed box: corners at the top and bottom, tees where a vertical
	// starts or stops, a cross where it carries on through. An empty last cell
	// is padded out rather than dropped — the right edge has to be out there.
	want := []string{
		"┌─────┬────┐",
		"│ A   │ B  │",
		"├─────┼────┤",
		"│ x   │ yy │",
		"│ zzz │    │",
		"└─────┴────┘",
	}
	if !slices.Equal(got, want) {
		t.Errorf("framedTable = %q, want %q", got, want)
	}
	// Every line is exactly as wide as every other — that is what makes the
	// junctions land on the verticals.
	for i, line := range got {
		if cells(line) != cells(got[0]) {
			t.Errorf("line %d is %d cells, want %d: %q", i, cells(line), cells(got[0]), line)
		}
	}

	// Painted, the furniture wears a hue and the cells do not, and nothing the
	// paint added is measurable.
	lit := framedTable(heads, rows, widths, 40, Paint{enabled: true})
	pipe, rules, heading := fg(dim)+"│"+reset, fg(dim), fg(headColor, "1")
	if lit[0] != rules+"┌─────┬────┐"+reset {
		t.Errorf("painted top rule = %q", lit[0])
	}
	if lit[1] != pipe+" "+heading+"A"+reset+"   "+pipe+" "+heading+"B"+reset+"  "+pipe {
		t.Errorf("painted head = %q", lit[1])
	}
	if lit[2] != rules+"├─────┼────┤"+reset {
		t.Errorf("painted head rule = %q", lit[2])
	}
	if last := lit[len(lit)-1]; last != rules+"└─────┴────┘"+reset {
		t.Errorf("painted foot rule = %q", last)
	}
	for i := range lit {
		if visibleCells(lit[i]) != cells(got[i]) {
			t.Errorf("paint moved line %d: %d cells, want %d", i, visibleCells(lit[i]), cells(got[i]))
		}
	}

	// Too narrow to hold the budget: the rows are cut to the width, and give
	// up their colour rather than being measured with it.
	cut := framedTable(heads, rows, widths, 5, Paint{enabled: true})
	for i, line := range cut {
		if strings.Contains(line, "\x1b") {
			t.Errorf("clamped line %d kept colour: %q", i, line)
		}
		if cells(line) > 5 {
			t.Errorf("clamped line %d is %d cells: %q", i, cells(line), line)
		}
	}
}

func TestPadTo(t *testing.T) {
	if got := padTo("ab", "cd", 10); got != "ab      cd" {
		t.Errorf("padTo = %q", got)
	}
	// Both sides are measured by what they show, not by what they carry.
	red := "\x1b[38;5;203m"
	if got := padTo(red+"ab"+reset, "cd", 10); got != red+"ab"+reset+"      cd" {
		t.Errorf("painted padTo = %q", got)
	}
	// Too narrow to hold both: the field grows rather than overlapping them.
	if got := padTo("aaaa", "bbbb", 6); got != "aaaa  bbbb" {
		t.Errorf("floor = %q", got)
	}
}

func TestWrapPrefix(t *testing.T) {
	// The indent prefix is re-emitted on every continuation line.
	got := wrap("    ", "aaa bbb ccc ddd", 12)
	want := []string{"    aaa bbb", "    ccc ddd"}
	if !slices.Equal(got, want) {
		t.Errorf("wrap = %q, want %q", got, want)
	}
	// A word too long for the room is broken mid-word rather than overflowing.
	got = wrap("  ", "abcdefghij", 7)
	want = []string{"  abcde", "  fghij"}
	if !slices.Equal(got, want) {
		t.Errorf("hard break = %q, want %q", got, want)
	}
	// A line that fits comes back byte-for-byte — internal spacing preserved.
	got = wrap("  ", "a  b   c", 40)
	if !slices.Equal(got, []string{"  a  b   c"}) {
		t.Errorf("fitting line = %q", got)
	}
}

func TestWrapCJK(t *testing.T) {
	// Double-width runes: two fill four cells, the third would cross five.
	got := wrap("", "日本語", 5)
	want := []string{"日本", "語"}
	if !slices.Equal(got, want) {
		t.Errorf("cjk = %q, want %q", got, want)
	}
	for _, line := range got {
		if cells(line) > 5 {
			t.Errorf("cjk line %q is %d cells", line, cells(line))
		}
	}
}

func TestWrapANSI(t *testing.T) {
	red := "\x1b[38;5;203m"
	got := wrap("| ", red+"aaa bbb"+reset, 7)
	// The span open at the break is closed there and reopened after the prefix.
	want := []string{"| " + red + "aaa" + reset, "| " + red + "bbb" + reset}
	if !slices.Equal(got, want) {
		t.Errorf("ansi = %q, want %q", got, want)
	}
	// Sequences are never split and never counted.
	for _, line := range got {
		if strings.Count(line, "\x1b") != strings.Count(stripSGR(line), "\x1b")+2 {
			t.Errorf("ansi line %q lost or split a sequence", line)
		}
		if visibleCells(line) > 7 {
			t.Errorf("ansi line %q is %d visible cells", line, visibleCells(line))
		}
	}
}

// A transcript prints message bodies and tool output exactly as they arrived,
// so it carries escapes it did not write: a hyperlink from a webfetch, a
// colour or a screen clear from a bash call. The layout has to measure those
// at the nothing the terminal advances for them, or it budgets a line against
// a width it does not have. sgrSeq answers a narrower question and is left
// answering it — what a --color always run added, and nothing else.
func TestVisibleCellsForeignEscapes(t *testing.T) {
	for _, c := range []struct {
		name string
		line string
		want int
	}{
		{"osc 8 hyperlink", "\x1b]8;;http://example.com/x\x07link\x1b]8;;\x07", 4},
		{"osc terminated by st", "\x1b]0;a title\x1b\\abc", 3},
		{"csi erase", "\x1b[2Jabc", 3},
		{"csi cursor move", "\x1b[10;20Habc", 3},
		{"csi private mode", "\x1b[?25labc", 3},
		{"two-character escape", "\x1bMabc", 3},
		{"our own colour", fg(grey) + "abc" + reset, 3},
		{"nothing to strip", "abc", 3},
	} {
		if got := visibleCells(c.line); got != c.want {
			t.Errorf("%s: visibleCells(%q) = %d, want %d", c.name, c.line, got, c.want)
		}
	}
	// stripSGR still answers only for the colour this program writes: an
	// escape the transcript is quoting is not ours to take back.
	quoted := "\x1b[2Jabc"
	if got := stripSGR(quoted); got != quoted {
		t.Errorf("stripSGR(%q) = %q, want it left alone", quoted, got)
	}
	if got := stripSGR(fg(grey) + "abc" + reset); got != "abc" {
		t.Errorf("stripSGR dropped the wrong thing: %q", got)
	}
}

// Wrapping folds on what is visible, and a sequence it did not write is
// carried where it stood rather than reopened on the next line — reopening a
// cursor move would be re-running it.
func TestWrapForeignEscapes(t *testing.T) {
	// Five cells of room for seven cells of text, so it has to fold — the
	// escape in front of it buys no room and must not be counted as any.
	clear := "\x1b[2J"
	got := wrap("", clear+"aaa bbb", 5)
	want := []string{clear + "aaa", "bbb"}
	if !slices.Equal(got, want) {
		t.Errorf("wrap = %q, want %q", got, want)
	}
	for _, line := range got {
		if visibleCells(line) > 5 {
			t.Errorf("line %q is %d visible cells", line, visibleCells(line))
		}
		if strings.Contains(strings.TrimPrefix(line, clear), "\x1b") {
			t.Errorf("line %q reopened a sequence that is not a colour span", line)
		}
	}
	// Nothing the transcript was given may be dropped on its way through the
	// wrapper. A colour span discarded with the spaces it sat among comes back
	// out of `open` on the next line; a sequence that cannot be reopened has
	// nothing to come back from, so the gap it sits in must stop being a place
	// to break rather than take it down with the spaces.
	for _, line := range []string{
		"aaa " + clear + " bbb",
		clear + "aaa " + clear + "bbb",
		"aaa " + clear + clear + " bbb",
	} {
		got := wrap("", line, 5)
		folded := strings.Join(got, "\n")
		if in, out := strings.Count(line, "\x1b"), strings.Count(folded, "\x1b"); out != in {
			t.Errorf("wrap(%q) kept %d of %d escapes: %q", line, out, in, folded)
		}
		// The cost of keeping them is a break the gap would have taken more
		// neatly, so the fold is allowed to differ from the bare text's — but
		// never to overrun, and never to lose a letter.
		letters := func(s string) string {
			return strings.Join(strings.FieldsFunc(stripEscapes(s), unicode.IsSpace), "")
		}
		if letters(folded) != letters(line) {
			t.Errorf("wrap(%q) changed the text: %q", line, folded)
		}
		for _, l := range got {
			if visibleCells(l) > 5 {
				t.Errorf("wrap(%q) line %q is %d cells", line, l, visibleCells(l))
			}
		}
	}
}

// TestWidthInvariant is what keeps the layout honest: whatever the terminal
// width, no emitted line may exceed it.
func TestWidthInvariant(t *testing.T) {
	d := zooDB(t)
	cases := [][]string{
		nil,
		{"--tools", "full", "--reasoning", "--stats"},
		{"--tools", "none"},
		{"--color", "always", "--tools", "full", "--reasoning", "--stats"},
		{"--list"},
		{"--list", "--color", "always"},
		{"--arg-width", "20", "--max-chars", "40", "--tools", "full"},
	}
	for _, width := range []int{60, 80, 100, 120, 200} {
		for _, args := range cases {
			t.Setenv("COLUMNS", strconv.Itoa(width))
			var stdout, stderr bytes.Buffer
			full := append([]string{"--db", d.path, "--everywhere", "--all"}, args...)
			if err := run(full, &stdout, &stderr); err != nil {
				t.Fatalf("run(%v): %v", full, err)
			}
			for line := range strings.SplitSeq(stdout.String(), "\n") {
				if got := visibleCells(line); got > width {
					t.Errorf("COLUMNS=%d %v: %d-cell line %q", width, args, got, stripSGR(line))
				}
			}
		}
	}
}
