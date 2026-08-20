package app

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// legend is the session table --list prints, and the transcript's key. `ruled`
// picks between them, and they differ in two ways.
//
// In furniture: `ruled` boxes the table in and adds a foot line under it, which
// is worth the width and the four extra rows when the table is the whole
// output, and not when it is a key over a transcript that rules its own blocks
// and is already headed by a summary saying what the foot line would.
//
// And in columns. The key names a session and says where and when — the tag is
// the only handle a reader needs, and --session matches on it. The table is
// where a reader picks a session out of a list of them, so it carries the three
// figures that tell one apart: how long it ran, how many messages it holds and
// what it spent. Those describe the whole session, never the window; the window
// only decides which rows are here at all, the way it already does for the
// start time beside them. `counts` carries the message tallies and is read only
// when `ruled` is set.
//
// Columns are budgeted, not measured: the narrow fixed ones take their natural
// width, `where` and `title` split what is left — `where` cut from the left,
// since the tail of a path is the informative half, `title` from the right.
// Seven columns want about a hundred cells to sit in; below that the pair
// squeezes, and below about seventy the row is cut at the margin.
//
// The paint reaches the heads and the rules and nothing else. Cells are left
// in the terminal's own foreground the way message text is: a session table is
// read for what is in it, and hues on a tag or a path would be saying
// something about the columns that is not true.
func legend(used []*session, root string, width int, ruled bool, counts map[string]int, paint Paint) []string {
	if len(used) == 0 {
		return nil
	}
	ordered := slices.Clone(used)
	slices.SortStableFunc(ordered, func(a, b *session) int { return cmp.Compare(a.timeCreated, b.timeCreated) })

	heads := []string{"Session", "Started", "Where", "Title"}
	whereIdx, titleIdx := 2, 3
	// The columns holding a figure, which is read from its right-hand end.
	var numeric []int
	if ruled {
		heads = []string{"Session", "Where", "Started", "Span", "Messages", "Tokens", "Title"}
		whereIdx, titleIdx = 1, 6
		numeric = []int{3, 4, 5}
	}
	rows := make([][]string, 0, len(ordered))
	for _, s := range ordered {
		// Without a root there is nothing to be relative to, and the absolute
		// path is the point — it is what tells two projects apart.
		where := filepath.Clean(s.directory)
		if root != "" {
			if rel, err := filepath.Rel(root, where); err == nil && !strings.HasPrefix(rel, "..") {
				where = rel
			}
		}
		where = homeTilde(where)
		if !ruled {
			rows = append(rows, []string{s.tag, shortStamp(s.timeCreated), where, titleOf(s)})
			continue
		}
		rows = append(rows, []string{s.tag, where, shortStamp(s.timeCreated),
			spanOf(s), fmt.Sprint(counts[s.id]), tokensOf(s), titleOf(s)})
	}

	naturals := make([]int, len(heads))
	for i, head := range heads {
		naturals[i] = cells(head)
		for _, r := range rows {
			naturals[i] = max(naturals[i], cells(r[i]))
		}
	}
	// budget() strikes its widths for a bare table; a frame costs more than
	// that, and the widths have to be struck knowing it.
	avail := width
	if ruled {
		avail -= frameCost(len(heads))
	}
	widths := budget(avail, naturals, whereIdx, titleIdx)
	for _, r := range rows {
		r[whereIdx] = tailCells(r[whereIdx], widths[whereIdx])
		r[titleIdx] = truncateCells(r[titleIdx], widths[titleIdx])
		// The figures are padded on their left before the table pads everything
		// on its right. budget() never shrinks a fixed column, so the width
		// used here is the width the column ends up with; the heads are the
		// widest thing in these three and already fill them, which is as well —
		// a padded head would carry its blanks inside the span colouring it.
		for _, i := range numeric {
			r[i] = rjust(r[i], widths[i])
		}
	}
	if ruled {
		out := framedTable(heads, rows, widths, width, paint)
		return append(out, legendFooter(len(ordered), ordered[0].timeCreated, width, paint))
	}
	return table(heads, rows, widths, width, paint)
}

// spanOf is how long a session ran, blank where the store never said when it
// was last touched — a session with no end is not a session that ended at once.
func spanOf(s *session) string {
	if s.timeUpdated == 0 {
		return ""
	}
	return spanText(s.timeUpdated - s.timeCreated)
}

// tokensOf is what a session spent, blank where the store accounted for
// nothing. A session that ran and spent nothing is a real thing — a turn that
// never completed leaves zeroes behind — and prints 0 rather than a blank.
func tokensOf(s *session) string {
	if !s.hasTokens {
		return ""
	}
	return tokenText(s.tokens)
}

// tokenText is a token total at a glance: the figure itself below a thousand,
// then k and M. The decimal is kept only in the bands where the leading digit
// alone would throw the magnitude away — 8.2k says something 8k does not — and
// the cut-offs are 9.95 rather than 10 because "%.1f" of 9.96 is "10.0", five
// cells where every other reading is four.
func tokenText(n int64) string {
	f := float64(n)
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case f < 9_950:
		return fmt.Sprintf("%.1fk", f/1000)
	case f < 999_500:
		return fmt.Sprintf("%.0fk", f/1000)
	case f < 9_950_000:
		return fmt.Sprintf("%.1fM", f/1e6)
	default:
		return fmt.Sprintf("%.0fM", f/1e6)
	}
}

// legendFooter is the line under the --list table: how many sessions are in it
// and how far back they reach. It is only worth drawing where the table is the
// whole output; over a transcript the summary line already says both.
//
// The count is of the sessions that answered the query and not of every session
// in the store — a root, a time window and --session all narrow it — so it is
// not called a total. The date is the earliest row's, since that is the reach
// of what is actually on the screen, and a date is all of it: the Started
// column carries the times, and what a foot line is for is the span.
//
// It sits outside the box and is indented to where the first column's text
// starts, so the count reads as a continuation of that column rather than as a
// stray line at the margin, and takes footColor — see style.go for why the
// line under a table is not painted like the box around it.
func legendFooter(n int, earliest int64, width int, paint Paint) string {
	noun := "sessions"
	if n == 1 {
		noun = "session"
	}
	text := fmt.Sprintf("%d %s since %s", n, noun, dayStamp(earliest))
	lead := strings.Repeat(" ", cells(frameLeft))
	// Measured before it is painted, like every other line here.
	return lead + paint.paint(truncateCells(text, max(1, width-cells(lead))), footColor)
}

// homeTilde spells the home directory as ~, the way a prompt would.
func homeTilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
