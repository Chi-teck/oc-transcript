package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// Blocks are stepped: the session banner at the margin, a turn's rail one step
// in, and everything that turn holds — its head as much as its body — a step
// further. Which session a block belongs to is answered by the banner and its
// rule, not by hue.
const (
	railIndent = 2
	bodyIndent = 4
)

// How many blank rows separate two things in the stream. Nothing that gets
// assembled carries its own: a block that laid down the gap after itself would
// have it trimmed off again at the next boundary, and the height of a break
// would belong to whichever side happened to be written first rather than to
// the break. Producers hand over bare content and the assembler decides the
// distance, once, from what is on either side.
//
// Inside a turn a paragraph gap still carries the rail, so the gap between
// turns has to be the wider one to outrank it — one blank row is the same
// height as a gap that is not a break at all. A session change outranks that in
// turn, and keeps its two rows above the banner rather than trading them for
// the rule: the rule says a session ended and the gap says how much of one. One
// row under the rule is enough, and is needed — a banner is an object rather
// than a line and wants air on both sides to read as one, and with the first
// head hard against the rule the rule read as that head's underline.
//
// The transcript ends on a single row rather than the two a turn leaves behind.
// Those two are a gap between turns and there is no turn after the last one: at
// the end of a run they are a hole at the bottom of the page, and under --follow
// they are already written by the time the next round knows whether it wanted
// one row or two. Ending on one and opening each round with whatever more it
// needs puts the same distance between two blocks whether a poll fell across
// them or not.
const (
	turnGap    = 2 // between one turn and the next
	sessionGap = 2 // above a session banner
	bannerGap  = 1 // between a banner's rule and the first turn under it
	endGap     = 1 // after the last line of the transcript
)

// Colour marks what a line is, never which session it came from. The frame is
// drawn on two levels: the session banner and its rule outside, in one hue,
// and inside it a turn's head and the rail down its side, in the hue of
// whoever took the turn — so the eye can find where a turn starts, whose it
// is, and where the run of turns it sits in starts, without reading any of
// them; the rest is the three states a body line can be in. Handing
// sessions their own colours was tried and dropped: a merged stream routinely
// runs to dozens of them, past any number of hues a reader can tell apart, let
// alone remember, and the tag on the banner answers the question exactly. The
// session hue says a line is a banner, not which banner it is.
//
// Everything that is not what a person or the model actually said is painted,
// in three tiers: 250 for a value, 244 for the label in front of it, 240 for
// the metadata neither of those needs. Message text alone is left in the
// terminal's own foreground, and is the only thing that can be. The quiet tier
// used SGR 2 and no colour, which is not the same thing: faint default-white
// is still white, so a stamp beside a grey label read as the loudest cell on
// the line.
// 256-colour SGR, since every terminal that does colour at all does these.
const (
	grey     = 244
	argColor = 250 // the value beside a grey label, lighter than the label and quieter than prose
	errColor = 203
	okColor  = 108
	// Reasoning is the light tier too, and for the same reason: it is text,
	// and text wants the lightest paint there is. It wore a violet before,
	// which read as a highlight over a block that is the opposite of one —
	// thinking out loud is the least of what a turn says, and a hue no other
	// line had made it the first thing the eye landed on. Grey puts it a step
	// under the prose beside it, which is the whole claim being made about it.
	reasonColor = 250
	dim         = 240 // the quietest tier: stamps, mime types, finish reasons
	noColor     = -1  // sentinel: paint nothing

	// A turn is drawn in the hue of whoever took it — the head across the top
	// and the rail down the side, one colour, because they are one envelope.
	// Painting the head with the frame instead made it look like part of the
	// transcript's furniture rather than the lid of the turn under it. It is a
	// speaker a hue names here and not a session: there are two of them,
	// always the same two, so a reader has nothing to memorise. The user keeps
	// the blue the whole frame used to be. The model's turns are most of a
	// transcript, so they take a warm hue a step quieter: an envelope as
	// bright as the user's, drawn round every reply, would be the loudest
	// thing on the screen and would be saying the least.
	userColor  = 117
	modelColor = 180

	// The session envelope is the outer frame and a turn's is the inner one,
	// and while both wore the same blue a banner read as one more head that
	// happened to have a rule under it — same hue, same weight, a step further
	// left. Its own hue puts the two levels a step apart before either is
	// read, and it takes two steps of that hue rather than one: solid on the
	// tag and its rule, lighter on the title beside them. The title was in the
	// quiet grey the timings and hashes take, which made a banner a coloured
	// tag with a caption after it belonging to something else; a lighter step
	// of the tag's own hue makes the whole line one object.
	//
	// The hue is violet because nothing else here is. Measured in OKLCH, the
	// structural colours sit in a narrow band of chroma — the two turn hues,
	// the ok mark and the banner's own title all near 0.07 — and the green
	// that used to be here sat at 0.157, twice that: the loudest thing in the
	// transcript after an error, spent on the widest element there is and
	// saying the least. It was also within seven degrees of the green an ok
	// mark wears, so the outer frame and a tool call that worked were one
	// colour told apart by weight alone. This violet is 0.117 — a step above
	// the turn hues, which is what the frame outside them should be, and
	// nowhere near the error — and it is further in hue from every other
	// colour in the palette than anything else the terminal can name at this
	// lightness. Violet was tried on reasoning blocks and dropped for making
	// the quietest text on the screen the first thing the eye found: the wrong
	// job for a paragraph of thinking, and the right one for the lid over
	// every turn beneath it.
	sessionColor = 183 // the banner's tag and rule
	sessionTitle = 225 // the title beside the tag: the same violet, near white

	// The directory out at the banner's right margin takes the tag's own
	// violet, not the metadata grey: the banner is one object across the line,
	// and a grey path hanging off its right end read as a stray note the
	// banner happened to be carrying rather than as the far end of the same
	// line. It goes on unbolded, so the tag is still the loudest thing there
	// and the path is the quiet answer to it.
	sessionWhere = sessionColor

	// The session table's column heads are white and bold — the only place
	// anything here is either. A head row is neither a label beside a value nor
	// furniture: it is the row that says what every row beneath it means, read
	// once at the top and then not again, and weight says that better than any
	// hue can. Two hues were tried and dropped on the way. The label grey sank
	// the heads into the rules under them, leaving the table nothing above its
	// own furniture. The banner's violet had the pleasing argument that a
	// session table and a session banner name the same thing — but it spent
	// the outer frame's colour on something that is not a frame, and made the
	// head row a fifth kind of coloured line in a transcript that already runs
	// four. White is what is left when neither argument holds: the heads stand
	// off the table on weight alone, and every hue in the palette goes on
	// meaning exactly what it meant.
	headColor = 231
	headBold  = true

	// The table's foot line takes the same white and not the weight. It says
	// what the table adds up to, which is content and not furniture, so the
	// greys the rules wear would have sunk it into the rule it sits under —
	// but it is a caption on the table rather than a heading over it, and
	// leaving the bold to the heads is what keeps them the top of the thing.
	footColor = headColor
)

const reset = "\033[0m"

// fg opens a span: the colour, plus any attribute codes to carry with it. The
// attributes ride in the same escape rather than nesting a second span —
// wrap() carries exactly one open span across a line break, so a nested one
// would come back without its attribute.
func fg(color int, attrs ...string) string {
	attrs = append(attrs, fmt.Sprintf("38;5;%d", color))
	return "\033[" + strings.Join(attrs, ";") + "m"
}

// Paint wraps text in SGR codes, or hands it back untouched when colour is off.
type Paint struct {
	enabled bool
}

// paint returns text wrapped in SGR codes for color (bold when asked), or
// untouched when colour is off.
func (p Paint) paint(text string, color int, bold ...bool) string {
	if !p.enabled || color == noColor || text == "" {
		return text
	}
	if len(bold) > 0 && bold[0] {
		return fg(color, "1") + text + reset
	}
	return fg(color) + text + reset
}

// underline paints and rules under in one span. It is what closes the head of
// a turn: the head names the turn and the line under it says where the naming
// stops and the turn's own content starts, without spending a row on a rule
// the way the session banner does.
func (p Paint) underline(text string, color int) string {
	if !p.enabled || text == "" {
		return text
	}
	return fg(color, "4") + text + reset
}

// wantColor decides colour for a stream: an explicit mode wins, then https://no-color.org (any non-empty value disables colour),
// then whether the stream is a terminal.
func wantColor(mode string, stream any) bool {
	switch mode {
	case "never":
		return false
	case "always":
		return true
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTerminal(stream)
}

func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// terminalWidth is shutil.get_terminal_size: COLUMNS first, then the terminal
// the stream is attached to, then the fallback.
func terminalWidth(stream any, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && n > 0 {
		return n
	}
	if f, ok := stream.(*os.File); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w
		}
	}
	return fallback
}
