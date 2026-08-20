package app

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// ---------------------------------------------------------------- measuring

// cells is the number of terminal columns a string occupies. A code-point
// count is wrong for CJK and emoji, so every width decision goes through this.
func cells(s string) int {
	return runewidth.StringWidth(s)
}

// ljust pads on the right to w cells, like str.ljust.
func ljust(s string, w int) string {
	return runewidth.FillRight(s, w)
}

// rjust pads on the left to w cells, for the columns holding numbers: a figure
// is read from its right-hand end, so a column of them has to line up there.
func rjust(s string, w int) string {
	return runewidth.FillLeft(s, w)
}

// truncateCells cuts s to at most limit cells, ending in an ellipsis when
// anything was dropped — value[:limit-1] + "…", measured in cells.
func truncateCells(s string, limit int) string {
	if cells(s) <= limit {
		return s
	}
	return runewidth.Truncate(s, limit, "…")
}

// tailCells keeps the last limit cells of s behind an ellipsis.
func tailCells(s string, limit int) string {
	if cells(s) <= limit {
		return s
	}
	return runewidth.TruncateLeft(s, cells(s)-(limit-1), "…")
}

// Two questions about an escape sequence, and they are not the same one.
// sgrSeq asks which sequences are colour spans this program wrote — the ones
// wrapLine can close at a break and reopen after the prefix, and the ones a
// --color always run has to be able to strip back to what --color never
// printed. escSeq asks which byte runs the terminal will advance zero columns
// for, which is all of them, whoever wrote them: message bodies and tool
// output are printed exactly as they arrived, so a transcript routinely
// carries escapes it did not write — a hyperlink from a webfetch, a colour or
// a cursor move from a bash call — and the layout has to measure those at
// nothing or it will budget a line it cannot fit.
var (
	sgrSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")
	escSeq = regexp.MustCompile(
		"\x1b\\[[0-?]*[ -/]*[@-~]" + // CSI: colour, cursor moves, erases
			"|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" + // OSC: titles, hyperlinks
			"|\x1b[\\x30-\\x5a\\x5c-\\x7e]") // two-character escapes
)

// stripSGR drops the colour sequences this program writes, and only those.
// Anything the transcript is quoting is left where it was: what this answers
// is what the same output would have looked like with colour off.
func stripSGR(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return sgrSeq.ReplaceAllString(s, "")
}

// stripEscapes drops every escape sequence, leaving what the terminal actually
// shows.
func stripEscapes(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return escSeq.ReplaceAllString(s, "")
}

// visibleCells is cells() for a string that may carry escapes: none of them
// take a column.
//
// Every escape and not just our own, because the two ways one gets into a line
// fail the same way. A line this program painted is measured after the fact by
// wrap() and padTo(), and a line quoting a tool's output can arrive carrying
// escapes of its own, since bodies are printed exactly as they came. Counting
// either as text budgets a line against a width it does not have and pushes it
// past the margin — which is the one thing TestWidthInvariant is here to catch.
//
// Reach for it only where painted text is unavoidable — wrap() and padTo() take
// what they are given, so they have to. Everywhere else the rule is to measure
// before painting and never after: renderTool budgets its margin against the
// bare name, summary and outcome and paints them once the widths are settled,
// and the session table measures its columns before a hue is anywhere near
// them. Kept that way, cells() is always the right question and the two never
// have to be told apart at a call site.
func visibleCells(s string) int {
	return cells(stripEscapes(s))
}

// padTo sets left at the margin and right against the far one, in a field
// width cells across, never letting the two touch: two cells between them is
// the floor, and a field too narrow to hold both grows rather than overlapping
// them. Widths are visible cells, so either side may already be painted.
//
// This is the only way anything reaches the right margin. A tool call's
// outcome does it, and so does anything else that wants a value out there —
// which is the point of having it: the alternative is each caller subtracting
// its own indent and its own gap from the width and getting a different answer.
func padTo(left, right string, width int) string {
	return left + strings.Repeat(" ", max(2, width-visibleCells(left)-visibleCells(right))) + right
}

// ---------------------------------------------------------------- tables

// The session table budgets its width rather than measuring it: every column
// but the flexible pair keeps its natural width, and `where` and `title` split
// what is left. `where` yields before `title` — a truncated path is still
// recognisable from its tail, while a truncated title loses its subject.
const (
	tableGutter = 2    // cells between columns
	whereFloor  = 12   // `where` holds this while `title` still fits
	titleFloor  = 14   // `title` holds this while any room remains
	whereShare  = 0.45 // `where`'s bid on the flexible room
)

// budget returns per-column widths for a table with two flexible columns, named
// by index rather than by position: the two are not neighbours in every table
// that has them. Fixed columns take their natural width; the flexible pair
// splits the remainder and truncates.
func budget(width int, naturals []int, whereIdx, titleIdx int) []int {
	widths := append([]int(nil), naturals...)
	fixed := 0
	for i, w := range widths {
		if i != whereIdx && i != titleIdx {
			fixed += w + tableGutter
		}
	}
	room := width - fixed - tableGutter // the gutter between the flexible pair
	wNat, tNat := widths[whereIdx], widths[titleIdx]
	if wNat+tNat <= room {
		return widths
	}
	// A floor is what a column may not be squeezed below, not what it is owed:
	// both are clamped back to the natural width, so a `where` column holding
	// nothing but "." stays one cell wide instead of taking twelve and a
	// `title` never rules past its longest title.
	w := min(wNat, max(whereFloor, int(float64(room)*whereShare)))
	t := min(tNat, max(titleFloor, room-w))
	// Room the titles turned out not to need goes back to `where` rather than
	// off the end of the row, so a run of long paths beside short titles is
	// cut at the margin and not at 45% of it.
	w = min(wNat, max(w, room-t))
	if w+t > room { // the floors collide: where yields first, down to one cell
		w = max(1, room-t)
	}
	if w+t > room { // still over: title yields too
		t = max(1, room-w)
	}
	widths[whereIdx], widths[titleIdx] = w, t
	return widths
}

// table lays heads and rows out on the given widths: cells padded right,
// two-space gutters, no rule row, nothing trailing. Cells must already be
// cut to their column's width; the clamp against `width` only guards the
// invariant, for the budgets too narrow to strike one that fits.
func table(heads []string, rows [][]string, widths []int, width int, paint Paint) []string {
	gutter := strings.Repeat(" ", tableGutter)
	return layTable(heads, rows, widths, width, furniture{gutter: gutter, paint: paint})
}

// What a table draws around and between its cells: the two edges of a row and
// the gutter between two columns, all of them bare, since a row is measured
// before anything is painted. The zero value is a table with no edges and no
// rules — the transcript's key.
type furniture struct {
	left   string
	gutter string
	right  string
	framed bool
	paint  Paint
}

const (
	// A framed row: an edge either side and a vertical between every pair of
	// columns, each with a space between it and the cell it touches, so nothing
	// is ever hard against a rule.
	frameLeft   = "│ "
	frameGutter = " │ "
	frameRight  = " │"
)

// frameCost is what a frame takes out of the width before the columns are
// budgeted. budget() strikes its widths for a bare table — two-space gutters,
// nothing at either end — and a frame adds a cell to each of the cols-1
// gutters and two cells at each end.
func frameCost(cols int) int { return (cols - 1) + 2*cells(frameLeft) }

// framedTable is table() in a box: the columns divided by verticals rather
// than by space, and three horizontals — over the heads, under them, and under
// the last row — meeting the verticals at corners and junctions. Furniture end
// to end, so it all goes in the quietest grey there is when colour is on.
//
// It is what --list prints, where the table is the whole output: the rules
// carry the eye across a row, and the box says where the table stops without
// the reader having to work it out from the alignment. The transcript's key
// stays plain, since it sits above rules of its own and a second grid there
// would compete with them.
//
// The frame is not free — see frameCost, which the caller has to have taken out
// of the budget before striking the widths.
func framedTable(heads []string, rows [][]string, widths []int, width int, paint Paint) []string {
	return layTable(heads, rows, widths, width, furniture{
		left: frameLeft, gutter: frameGutter, right: frameRight, framed: true, paint: paint,
	})
}

// layTable is the body of both: cells cut and padded to their column, joined
// on a gutter, the row clamped to the width.
//
// The clamp is why a row is assembled twice. A row is measured before it is
// painted — the rule the whole file keeps — so it is put together bare, and
// only once the width is known to hold is it put together again with the paint
// on. A row that overflows is cut instead, and gives up its colour with the
// cells it loses; nothing else here can overflow, so that is a pathologically
// narrow terminal or nothing.
func layTable(heads []string, rows [][]string, widths []int, width int, f furniture) []string {
	// lit paints the verticals in a piece of furniture and leaves the spaces
	// flanking them alone: a span around a blank cell shows nothing and says
	// nothing. Cells never go through here, so a title that happens to contain
	// a box-drawing character is left as the session named it.
	lit := func(s string) string {
		return strings.ReplaceAll(s, "│", f.paint.paint("│", dim))
	}
	line := func(cs []string, color int, bold bool) string {
		parts := make([]string, len(cs))
		for i, c := range cs {
			parts[i] = ljust(truncateCells(c, widths[i]), widths[i])
		}
		if !f.framed {
			// Trailing empty columns are dropped rather than padded and then
			// trimmed off, since an unframed row ends wherever its last cell
			// does. A framed one cannot: the right edge has to be out there
			// whether the last cell has anything in it or not.
			for len(parts) > 1 && strings.TrimSpace(parts[len(parts)-1]) == "" {
				parts = parts[:len(parts)-1]
			}
		}
		if bare := f.left + strings.Join(parts, f.gutter) + f.right; cells(bare) > width {
			return truncateCells(strings.TrimRight(bare, " "), width)
		}
		if color != noColor {
			for i, p := range parts {
				// The word is painted and the padding after it is not: a span
				// closed past the last letter would carry blank cells the trim
				// at the end can no longer see, and the row would keep them.
				word := strings.TrimRight(p, " ")
				parts[i] = f.paint.paint(word, color, bold) + p[len(word):]
			}
		}
		row := lit(f.left) + strings.Join(parts, lit(f.gutter)) + lit(f.right)
		if f.framed {
			return row
		}
		return strings.TrimRight(row, " ")
	}
	rule := func(left, mid, right string) string {
		return tableRule(widths, width, left, mid, right, f.paint)
	}
	out := make([]string, 0, len(rows)+4)
	if f.framed {
		out = append(out, rule("┌", "┬", "┐"))
	}
	out = append(out, line(heads, headColor, headBold))
	if f.framed {
		out = append(out, rule("├", "┼", "┤"))
	}
	for _, r := range rows {
		// Cells stay in the terminal's own foreground, the way message text
		// does: a hue on a tag or a path would be making a claim about that
		// column that is not true.
		out = append(out, line(r, noColor, false))
	}
	if f.framed {
		out = append(out, rule("└", "┴", "┘"))
	}
	return out
}

// tableRule is one horizontal of a frame. Each segment spans its column and
// the space either side of it, so the junctions land exactly on the verticals
// of the rows above and below and the rule comes out the same width as one.
// Which junctions those are is the whole difference between the three: corners
// at the top and bottom, tees where a vertical starts or stops, a cross where
// it carries on through. Being furniture end to end it is painted whole, after
// the same measure-then-paint as a row.
func tableRule(widths []int, width int, left, mid, right string, paint Paint) string {
	segs := make([]string, len(widths))
	for i, w := range widths {
		segs[i] = strings.Repeat("─", w+2)
	}
	rule := left + strings.Join(segs, mid) + right
	if cells(rule) > width {
		return truncateCells(rule, width)
	}
	return paint.paint(rule, dim)
}

// ---------------------------------------------------------------- wrapping

// wrap breaks text into lines of at most width cells, prefix included, and
// re-emits prefix on every line — the indent survives wrapping. Newlines in
// text are hard breaks. SGR sequences take no room and are never split, and
// a colour span open where a line breaks is closed there and reopened after
// the prefix, so colour survives too.
func wrap(prefix, text string, width int) []string {
	room := max(1, width-visibleCells(prefix))
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		out = append(out, wrapLine(prefix, line, room)...)
	}
	return out
}

// wrapLine wraps one newline-free line into pieces of at most room cells,
// breaking at spaces where it can and mid-word where it must.
func wrapLine(prefix, line string, room int) []string {
	if visibleCells(line) <= room {
		return []string{strings.TrimRight(prefix+line, " ")}
	}
	var out []string
	emit := func(open, content, closing string) {
		content = strings.TrimRight(content, " ")
		if content != "" {
			content = open + content + closing
		}
		out = append(out, strings.TrimRight(prefix+content, " "))
	}
	var cur []byte
	curW := 0
	active := "" // the SGR span in force at the write position
	open := ""   // the span to reopen at the start of the current piece
	// The cut is the last gap seen: emitting keeps cur[:cutB] on this line
	// and resumes at cur[contB:], where the span state is contActive.
	cutB, cutW, cutActive := -1, 0, ""
	contB, contW, contActive := -1, 0, ""
	inGap := false

	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			if loc := escSeq.FindStringIndex(line[i:]); loc != nil && loc[0] == 0 {
				seq := line[i : i+loc[1]]
				// Every escape passes through at no width, but only a colour
				// span is remembered as the one to close at a break and reopen
				// on the next line. Reopening a cursor move or a screen clear
				// would be re-running it, not restoring it — so a sequence the
				// transcript is merely quoting rides along where it was written
				// and says nothing about the lines after it.
				switch {
				case seq == reset:
					active = ""
				case sgrSeq.MatchString(seq):
					active = seq
				case inGap:
					// Breaking at a gap throws the gap away, which is fine for
					// spaces and not for this: a span would come back from
					// `open` and this has nothing to come back from, so it
					// would be bytes the transcript was given and did not
					// print. The gap stops being a place to break instead.
					cutB = -1
				}
				cur = append(cur, seq...)
				i += loc[1]
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		w := runewidth.RuneWidth(r)
		if r == ' ' {
			if !inGap {
				inGap, cutB, cutW, cutActive = true, len(cur), curW, active
				contB = -1
			}
		} else {
			if inGap {
				inGap, contB, contW, contActive = false, len(cur), curW, active
			}
			if curW+w > room && curW > 0 {
				if cutB > 0 && cutW > 0 && contB >= cutB {
					closing := ""
					if cutActive != "" {
						closing = reset
					}
					emit(open, string(cur[:cutB]), closing)
					cur = append([]byte(nil), cur[contB:]...)
					curW -= contW
					open = contActive
				} else {
					closing := ""
					if active != "" {
						closing = reset
					}
					emit(open, string(cur), closing)
					cur, curW, open = nil, 0, active
				}
				cutB, contB = -1, -1
			}
		}
		cur = append(cur, line[i:i+size]...)
		curW += w
		i += size
	}
	emit(open, string(cur), "")
	return out
}
