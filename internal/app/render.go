package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// First input key that says what a tool call was actually about. Covers
// opencode's built-ins and lets an MCP tool name itself through whichever of
// these it happens to take, without a per-tool table.
var argKeys = []string{
	"command", "filePath", "pattern", "url", "query", "channel", "username",
	"post_id", "prompt", "path", "description", "name", "file", "code",
	"entity_type", "message",
}

// oneLine collapses a value to a single line of at most limit cells. A
// non-string value is its compact JSON. The cap is measured in display cells,
// not code points, so CJK and emoji count for the room they take.
func oneLine(raw json.RawMessage, limit int) string {
	s, ok := unquoteFast(raw)
	if !ok {
		if err := json.Unmarshal(raw, &s); err != nil {
			if string(raw) == "null" || len(raw) == 0 {
				return ""
			}
			s = reformatJSON(raw, -1)
		}
	}
	return oneLineText(s, limit)
}

func oneLineText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit < 1 {
		if value != "" {
			return "…"
		}
		return ""
	}
	return truncateCells(value, limit)
}

// toolSummary is the one-line gist of a tool call: the first known input key
// that has a value, else the state's title, else the whole input.
func toolSummary(state *toolState, limit int) string {
	var args map[string]json.RawMessage
	if truthy(state.Input) {
		if err := json.Unmarshal(state.Input, &args); err != nil {
			args = nil
		}
		for _, key := range argKeys {
			if v, ok := args[key]; ok && truthy(v) {
				return oneLine(v, limit)
			}
		}
	}
	if state.Title != "" {
		return oneLineText(string(state.Title), limit)
	}
	if truthy(state.Input) {
		return oneLine(state.Input, limit)
	}
	return ""
}

// The envelope opencode injects as a synthetic text part when a background
// subagent finishes: an opening tag carrying that subagent's session id and the
// state it ended in, a one-line summary, and whatever it returned.
//
//	<task id="ses_…" state="completed">
//	<summary>Background task completed: …</summary>
//	<task_result>
//	…
//	</task_result>
//	</task>
//
// It is the one synthetic shape that is a record rather than prose. The others
// — the nudge after a compaction, the note that the user ran a tool — collapse
// to a line cleanly; this one spends most of the width on markup and on a
// session id the banner overhead already prints, and loses the summary
// mid-word.
const taskOpen = "<task "

// syntheticTask reads that envelope: the id and state off the opening tag, the
// summary, and the result if one is there.
//
// Scanned rather than parsed. The <task_result> body is whatever a subagent
// returned and may hold a bare <, which would fail encoding/xml on a part that
// renders perfectly well. Anything that does not match comes back ok=false and
// is drawn the way every other synthetic part is — no error, no warning.
func syntheticTask(text string) (id, state, summary, result string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, taskOpen) {
		return "", "", "", "", false
	}
	open, _, closed := strings.Cut(text, ">")
	if !closed {
		return "", "", "", "", false
	}
	id, okID := xmlAttr(open, "id")
	state, okState := xmlAttr(open, "state")
	summary, okSummary := xmlElement(text, "summary")
	if !okID || !okState || !okSummary {
		return "", "", "", "", false
	}
	// The summary opens by naming the state the attribute has already given,
	// and the line prints that state in front of it: left alone it reads
	// "completed: Background task completed: …". A summary that does not carry
	// the prefix is printed whole.
	summary = strings.TrimPrefix(summary, "Background task "+state+": ")
	// A task that failed may carry no result at all, which is not a reason to
	// fall back to the markup.
	result, _ = xmlElement(text, "task_result")
	return id, state, summary, result, true
}

// xmlAttr reads a double-quoted attribute out of an opening tag.
func xmlAttr(tag, name string) (string, bool) {
	_, rest, ok := strings.Cut(tag, " "+name+`="`)
	if !ok {
		return "", false
	}
	value, _, ok := strings.Cut(rest, `"`)
	return value, ok
}

// xmlElement is the text between a tag and its closing twin.
func xmlElement(body, name string) (string, bool) {
	_, rest, ok := strings.Cut(body, "<"+name+">")
	if !ok {
		return "", false
	}
	text, _, ok := strings.Cut(rest, "</"+name+">")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(text), true
}

// renderSynthetic is what follows the [synthetic] label: the task envelope
// drawn as the record it is, and for every other shape the one-lined text they
// have always been.
//
// The tag is the session banner's own — same six characters, same brackets,
// same hue — so the line can be matched by eye against the banner of the
// session it names. No ↓ hangs off it: that suffix is tagSessions' answer to
// whether the parent session is in scope, and all this line has is an id. A tag
// that is right six characters out of six is worth more than one that guesses
// an arrow.
//
// The state comes off the attribute rather than out of the summary's wording,
// since the attribute is the authoritative one, and the summary itself stays in
// the tier prose takes: it is the only part of the line the subagent wrote.
func renderSynthetic(text string, opts *options) string {
	id, state, summary, result, ok := syntheticTask(text)
	if !ok {
		return oneLineText(text, opts.argWidth)
	}
	paint := opts.paint
	tag := tagOpen + tagFor(id) + tagClose
	head := paint.paint("task", grey) + " " + paint.paint(tag, sessionColor) +
		" " + paint.paint(state+":", argColor)
	// The line answers to --arg-width the way the one-lined form it replaces
	// did, and the header is charged to that budget before the summary: at
	// --arg-width 20 the header alone overruns it and the summary comes back an
	// ellipsis, which is what a cap that bites looks like.
	if s := oneLineText(summary, opts.argWidth-cells("task "+tag+" "+state+": ")); s != "" {
		head += " " + s
	}
	// The result is dropped unless --tools full asked for that much. The
	// subagent's own session prints it verbatim under its own banner a few
	// lines above — but only while that session is in scope, and under
	// --session filtering this envelope is the one place it survives.
	if result != "" && opts.tools == "full" {
		return head + "\n" + indentBlock("result", result, opts.maxChars, paint, grey)
	}
	return head
}

// capRunes caps text at limit code points, saying how much was dropped. The
// cap is on bulk, not width — the terminal width is the wrapper's business.
func capRunes(body string, limit int) string {
	limit = max(limit, 0)
	// Byte length bounds rune count, so most bodies skip the counting.
	if len(body) > limit {
		if n := utf8.RuneCountInString(body); n > limit {
			runes := []rune(body)
			body = string(runes[:limit]) + fmt.Sprintf("\n… [%d more chars]", n-limit)
		}
	}
	return body
}

// indentBlock renders a labelled, indented quotation capped at limit
// characters.
func indentBlock(label, body string, limit int, paint Paint, color int) string {
	body = capRunes(strings.TrimRightFunc(body, unicode.IsSpace), limit)
	tag := paint.paint(label, color)
	if strings.Contains(body, "\n") {
		lines := strings.Split(body, "\n")
		for i, line := range lines {
			// The line's own indentation stays outside the span, so the wrapper
			// can still fold it into the prefix and keep continuations aligned.
			trimmed := strings.TrimLeft(line, " ")
			lead := line[:len(line)-len(trimmed)]
			lines[i] = "    " + lead + paint.paint(trimmed, argColor)
		}
		return "  " + tag + "\n" + strings.Join(lines, "\n")
	}
	return "  " + tag + " " + paint.paint(strings.TrimSpace(body), argColor)
}

// outcomeWidth is the right-hand block of a tool line: the mark and the
// duration together, one column whatever either of them turns out to be.
const outcomeWidth = 12

// outcomeCell lays that block out. Fixed fields, so outcome and duration each
// line up as their own column: ok/ERR sit right-aligned in the first three
// cells with the duration after them, and a longer status such as `running` or
// `pending 2.0s` is right-aligned over the whole block instead of spilling left
// into the summary's room.
func outcomeCell(mark, took string) (markCell, tookCell string) {
	if cells(mark) > 3 {
		if took == "" {
			return fmt.Sprintf("%*s", outcomeWidth, mark), ""
		}
		// One space between them here rather than the usual two, so a wide
		// status and a duration still fit the block together.
		return fmt.Sprintf("%*s", max(cells(mark), outcomeWidth-1-cells(took)), mark), " " + took
	}
	return fmt.Sprintf("%3s", mark), fmt.Sprintf("  %7s", took)
}

// toolDuration is how long a call took, to a tenth of a second, or "" when the
// state does not say.
func toolDuration(state *toolState) string {
	if state.Time == nil || !truthy(state.Time.Start) || !truthy(state.Time.End) {
		return ""
	}
	start, okS := numberValue(state.Time.Start)
	end, okE := numberValue(state.Time.End)
	if !okS || !okE {
		return ""
	}
	return fmt.Sprintf("%.1fs", (end-start)/1000)
}

// renderTool is one tool call: a head line with the outcome at the right
// margin, then, with --tools full, the input and the output or error.
func renderTool(p *partData, opts *options) []string {
	state := p.State
	if state == nil {
		state = &toolState{}
	}
	status := string(state.Status)
	if status == "" {
		status = "?"
	}
	mark := status
	switch status {
	case "completed":
		mark = "ok"
	case "error":
		mark = "ERR"
	}
	markCell, tookCell := outcomeCell(mark, toolDuration(state))
	right := markCell + tookCell

	paint := opts.paint
	name := string(p.Tool)
	if name == "" {
		name = "?"
	}
	truncated := outputTruncated(state)
	note := ""
	if truncated {
		note = "[output truncated]"
	}

	// The outcome sits at the right margin so a run of calls scans as a column
	// of ok/ERR; the summary takes what is left between the name and it, and
	// yields entirely — the note first — sooner than push past the margin.
	avail := opts.width - bodyIndent - cells(right) - 2
	if cells(name) > avail {
		name = truncateCells(name, max(1, avail))
	}
	rem := avail - cells(name)
	if note != "" && rem < 1+cells(note) {
		note = ""
	}
	if note != "" {
		rem -= 1 + cells(note)
	}
	summary := ""
	if rem >= 10 {
		summary = toolSummary(state, min(opts.argWidth, rem-1))
	}
	head := paint.paint(name, grey)
	if summary != "" {
		head += " " + paint.paint(summary, argColor)
	}
	if note != "" {
		head += " " + paint.paint(note, dim)
	}
	markColor := okColor
	if status == "error" {
		markColor = errColor
	}
	outcome := paint.paint(markCell, markColor, status == "error") + paint.paint(tookCell, dim)
	lines := []string{padTo(head, outcome, opts.width-bodyIndent)}

	switch {
	case opts.tools == "full":
		if truthy(state.Input) {
			lines = append(lines, indentBlock("in", reformatJSON(state.Input, 2), opts.maxChars, paint, grey))
		}
		if status == "error" {
			lines = append(lines, indentBlock("err", string(state.Error), opts.maxChars, paint, errColor))
		} else if state.Output != "" {
			lines = append(lines, indentBlock("out", string(state.Output), opts.maxChars, paint, grey))
		}
		if truncated {
			lines = append(lines, "  "+paint.paint("truncated", grey)+" "+paint.paint(truncatedNote(state), argColor))
		}
	case status == "error":
		lines = append(lines, indentBlock("err", string(state.Error), opts.maxChars, paint, errColor))
	}
	return lines
}

// outputTruncated reports metadata.truncated — the inline output is not the
// whole story.
func outputTruncated(state *toolState) bool {
	var md toolMetadata
	if err := json.Unmarshal(state.Metadata, &md); err != nil {
		return false
	}
	return truthy(md.Truncated)
}

// truncatedNote says how much was kept inline and where the rest is, when the
// tool wrote it somewhere (bash and webfetch do; read/glob/grep cut their own
// output and keep nothing).
func truncatedNote(state *toolState) string {
	var md toolMetadata
	_ = json.Unmarshal(state.Metadata, &md)
	note := fmt.Sprintf("%d chars shown", len([]rune(string(state.Output))))
	if md.OutputPath == "" {
		return note + ", the rest was not kept"
	}
	note += ", full output at " + string(md.OutputPath)
	if info, err := os.Stat(string(md.OutputPath)); err == nil {
		note += fmt.Sprintf(" (%d bytes)", info.Size())
	}
	return note
}

// headSep joins the fields of a header — stamp, role, model. A gap alone reads
// as alignment that failed to line up; a bullet says the fields are separate
// and deliberate. A full bullet rather than the middle dot the tags use: the
// head is read across, at a glance, and the smaller glyph vanishes at that
// distance. One cell wide, like every other glyph the layout budgets for.
const headSep = " • "

// bodyRail runs down the left of a turn, from its head to its last line. The
// head's underline closes the envelope along the top and the rail closes it
// along the side, which is what an indent alone could not do: an indent says
// how deep a line sits, not where the thing it belongs to ends. Light
// box-drawing, to match the rule under a session banner, and one cell so the
// body keeps its indent of four.
const bodyRail = "│"

// turnColor is the hue a whole turn is drawn in — its head and the rail down
// its body, one colour because they are one envelope — and is how a prompt is
// told from a reply now that the head carries no direction glyph. Colour is
// doing work here that no character does, so with --color never the role
// named in the head is the only thing left saying which way a turn went.
func turnColor(role string) int {
	switch role {
	case "user":
		return userColor
	case "assistant":
		return modelColor
	}
	// A role that is neither takes no side. The label grey rather than the
	// quiet one: this colours a header, and a header has to be readable even
	// when it has nothing to declare.
	return grey
}

// A chunk is one part's lines; tool chunks pack together, everything else gets
// a blank line around it. A chunk colour is applied line by line, after the
// lines are split and before they are wrapped, so a span never has to survive
// being cut in two.
//
// Chunks are built through newChunk and never as a literal: the zero value of
// color is 0, which is a real colour — black — and would paint anything that
// forgot to say otherwise.
type chunk struct {
	text  string
	tool  bool
	color int
}

func newChunk(text string) chunk { return chunk{text: text, color: noColor} }

// decodeParts decodes a message's parts, warning about and dropping any that
// will not parse rather than losing the whole message to one bad row.
func decodeParts(parts []partRow, opts *options) []*partData {
	payloads := make([]*partData, 0, len(parts))
	for _, part := range parts {
		var p partData
		if err := json.Unmarshal(part.data, &p); err != nil {
			opts.warnf("skipping part %s: %v", part.id, err)
			continue
		}
		payloads = append(payloads, &p)
	}
	return payloads
}

// renderAttachment names a file the turn carried. Only the name and type: the
// url is routinely a data: URI carrying the whole file.
//
// One space in front of the type, like everywhere else: the parentheses already
// say it is an aside and the tier already says it is the quiet one, so a wider
// gap was a third way of saying the same thing and the only one that cost a
// hole in the line.
func renderAttachment(p *partData, paint Paint) string {
	return paint.paint("attachment", grey) + " " + paint.paint(stringOr(p.Filename, "?"), argColor) +
		" " + paint.paint("("+stringOr(p.Mime, "?")+")", dim)
}

// renderPatch is how many files a turn changed and the hash it changed them to.
//
// The hash is a value, not a footnote: it is the one thing on the line that can
// be taken somewhere else and used, so it takes the same light tier the file
// count does rather than the quiet grey stamps and totals take. One space
// between them, not the two a value and its trailing metadata get elsewhere:
// they are two values now, one label covers both, and a wider gap made the line
// read as a field with something else stranded to the right of it.
func renderPatch(p *partData, paint Paint) string {
	var files []json.RawMessage
	_ = json.Unmarshal(p.Files, &files)
	hash := []rune(string(p.Hash))
	hash = hash[:min(8, len(hash))]
	return paint.paint("patch", grey) + " " +
		paint.paint(fmt.Sprintf("%d file(s)", len(files)), argColor) +
		" " + paint.paint(string(hash), argColor)
}

// renderStats is the --stats line: what one step of a turn spent.
//
// Drawn the way a tool call is drawn: grey on the label, the light tier on the
// value beside it. A count is the value here and the word after it says which
// count, so the figures come up a tier and the nouns stay put. The whole row
// was one dim grey before, which gave the only part of it worth reading the
// same weight as the words labelling it, and left the line reading as a total
// rather than as five of them.
func renderStats(p *partData, paint Paint) string {
	tokens := p.Tokens
	if tokens == nil {
		tokens = &stepTokens{}
	}
	cost, _ := numberValue(p.Cost)
	field := func(value, label string) string {
		return paint.paint(value, argColor) + " " + paint.paint(label, grey)
	}
	stats := paint.paint("tokens", grey) + " " +
		field(numberText(tokens.Input, "0"), "in") + " " +
		field(numberText(tokens.Output, "0"), "out") + " " +
		field(numberText(tokens.Cache.Read, "0"), "cached") + " " +
		field(numberText(tokens.Cache.Write, "0"), "written") + " " +
		paint.paint(fmt.Sprintf("$%.4f", cost), argColor)
	// The finish reason is the one thing on the line nobody asked for, so it
	// keeps the quiet tier — and the tier is what sets it apart now that the gap
	// in front of it is one space like every other.
	if reason := string(p.Reason); reason != "" {
		stats += " " + paint.paint(reason, dim)
	}
	return stats
}

// renderMessage renders one message as a block of lines, or nothing when it
// has nothing to show. The header names the turn, the rail beside it says how
// far the turn runs, and blank lines do the separating within.
func renderMessage(msg messageRow, parts []partRow, opts *options) []string {
	var data messageData
	if err := json.Unmarshal(msg.data, &data); err != nil {
		opts.warnf("skipping message %s: %v", msg.id, err)
		return nil
	}
	role := string(data.Role)
	if role == "" {
		role = "?"
	}
	paint := opts.paint
	var body []chunk
	add := func(text string) { body = append(body, newChunk(text)) }
	for _, p := range decodeParts(parts, opts) {
		switch {
		case p.Type == "text":
			text := string(p.Text)
			if truthy(p.Synthetic) {
				add(paint.paint("[synthetic]", dim) + " " + renderSynthetic(text, opts))
			} else if strings.TrimSpace(text) != "" {
				add(strings.TrimRightFunc(text, unicode.IsSpace))
			}

		case p.Type == "tool" && opts.tools != "none":
			c := newChunk(strings.Join(renderTool(p, opts), "\n"))
			c.tool = true
			body = append(body, c)

		case p.Type == "reasoning" && opts.reasoning && strings.TrimSpace(string(p.Text)) != "":
			// No label: the colour is the label. Reasoning is the one part whose
			// whole body is a single kind of thing, so a hue over all of it says
			// what a word in front of the first line said, without competing with
			// the prose for the eye.
			c := newChunk(capRunes(strings.TrimRightFunc(string(p.Text), unicode.IsSpace), opts.maxChars))
			c.color = reasonColor
			body = append(body, c)

		case p.Type == "file":
			add(renderAttachment(p, paint))

		case p.Type == "patch":
			add(renderPatch(p, paint))

		case p.Type == "compaction":
			add(paint.paint("context compacted", dim))

		case p.Type == "step-finish" && opts.stats:
			add(renderStats(p, paint))
		}
	}

	if truthy(data.Error) {
		add(indentBlock("error", reformatJSON(data.Error, -1), opts.maxChars, paint, errColor))
	}

	if len(body) == 0 {
		return nil
	}

	// The banner carries the session, so the header only says what changed within
	// it. It is drawn in the speaker's own hue, the same one the rail beside it
	// takes: head and rail are two sides of one envelope, and painting them
	// apart made the head look like it belonged to the frame rather than to the
	// turn under it. The role is still named in words, which is what is left of
	// the distinction under --color never.
	//
	// The stamp is dated in full rather than a bare clock. A transcript is read in
	// pieces — scrolled into, grepped, piped through less — and a piece that only
	// says 10:27:51 is a piece that has to be traced back to whatever drew the last
	// date. Every line answering for itself costs eleven cells and no thought.
	head := stamp(msg.timeCreated) + headSep + role
	if suffix := headSuffix(&data); len(suffix) > 0 {
		head += headSep + strings.Join(suffix, " ")
	}
	return turnEnvelope(head, body, turnColor(role), opts)
}

// headSuffix is what a head says after the role: which model answered, under
// which agent, and whether the turn ended in an error. A prompt has none of it,
// and the default agent is not worth naming — it is what @ is measured against.
func headSuffix(data *messageData) []string {
	if data.Role != "assistant" {
		return nil
	}
	var suffix []string
	if data.ModelID != "" {
		if data.Variant != "" {
			suffix = append(suffix, string(data.ModelID)+"/"+string(data.Variant))
		} else {
			suffix = append(suffix, string(data.ModelID))
		}
	}
	if data.Agent != "" && data.Agent != "build" {
		suffix = append(suffix, "@"+string(data.Agent))
	}
	if truthy(data.Error) {
		suffix = append(suffix, "ERROR")
	}
	return suffix
}

// turnEnvelope draws one turn around its body: the head across the top, ruled
// underneath, and the rail down the left of the whole thing.
//
// The rail runs down the whole turn, head included: it is the left side of the
// envelope and the head is inside the envelope, so a rail that started under
// the head left the line naming the turn standing outside the thing it names.
// It takes the rest of the body's indent as its gap, so the body sits where a
// plain indent put it, and it is drawn on every blank line within: broken at
// any gap it would read as a column of ticks rather than as one line, and the
// whole point of it is to say where the turn ends without a line saying so.
func turnEnvelope(head string, body []chunk, color int, opts *options) []string {
	paint := opts.paint
	pad := strings.Repeat(" ", railIndent) + paint.paint(bodyRail, color) +
		strings.Repeat(" ", bodyIndent-railIndent-cells(bodyRail))
	blank := strings.TrimRight(pad, " ")
	out := wrap(pad, paint.underline(head, color), opts.width)
	// One row between the head and the body. The head is the envelope and the
	// body is what came in it; hard against each other, a message's first line
	// read as the last field of its own header. The row carries the rail like
	// every other blank inside the turn — the head sits inside the envelope
	// now, so a bare row here would cut the rail in two and leave the head with
	// a tick of its own above the body's.
	out = append(out, blank)
	for i, c := range body {
		// One blank line between parts keeps prose, blocks and labels apart; a
		// run of tool calls stays packed so ok/ERR scan as a column.
		if i > 0 && (!c.tool || !body[i-1].tool) {
			out = append(out, blank)
		}
		for line := range strings.SplitSeq(c.text, "\n") {
			if line == "" {
				out = append(out, blank)
				continue
			}
			// A line's own indentation folds into the wrap prefix, so
			// continuations line up under the content, not the margin — but a
			// pathological indent must not eat the width the content needs.
			trimmed := strings.TrimLeft(line, " ")
			lead := line[:len(line)-len(trimmed)]
			if maxLead := opts.width - bodyIndent - 20; len(lead) > maxLead {
				lead = lead[:max(0, maxLead)]
			}
			out = append(out, wrap(pad+lead, paint.paint(trimmed, c.color), opts.width)...)
		}
	}
	return out
}

// The flag on a banner says what that banner is doing with the session named
// on it: planting it, joining one already under way, or coming back to one the
// transcript has shown before. Text-presentation code points, not emoji: one
// cell each, and they take the session's colour rather than the font's.
const (
	newFlag  = "⚑"
	joinFlag = "⚐"
	backFlag = "↻"
)

// The brackets around a banner's tag. Angle bracket ornaments rather than the
// ASCII pair: a transcript is full of ASCII brackets it did not write — a
// bridge's prefix, a title, a line of code quoted back — and a glyph no body
// text is going to contain says the tag is the transcript's own furniture
// before it is read. They point at what they hold, which suits a tag that is
// the one thing on the banner naming the session. One cell each, like every
// other glyph the layout budgets for.
const (
	tagOpen  = "❬"
	tagClose = "❭"
)

// ongoing reports whether a session was already running when the window
// opened, so its banner announces a conversation joined part-way rather than
// one starting here. It is a fact about the session rather than about the
// output — with --all nothing predates the window and every session begins in
// view — and it is asked only of a session's first banner, since a later one
// has a nearer answer to give.
func ongoing(s *session, since *int64) bool {
	return since != nil && s.timeCreated < *since
}

// bannerFlag picks the flag a banner opens with. Two different things put a
// session's beginning out of view and they are not answered the same way: a
// session already running when the window opened has the rest of itself
// outside the window, and widening --since is what fetches it; a session the
// stream left and came back to has the rest of itself further up this same
// output, and scrolling is what fetches it. A flag each, and the plain one for
// a session that starts where it says it does, leaves nothing to be inferred
// from a tag the reader would first have to remember having seen.
func bannerFlag(s *session, since *int64, seen bool) string {
	switch {
	case seen:
		return backFlag
	case ongoing(s, since):
		return joinFlag
	}
	return newFlag
}

// sessionBanner names the session a run of blocks belongs to.
//
// Drawn on every change of session rather than on every message, and it is the
// only thing that says which session a block came from — neither indentation
// nor colour carries that.
func sessionBanner(s *session, flag string, paint Paint, width int) []string {
	// The id is bracketed. It is an identifier rather than a word — six random
	// characters, sometimes with an arrow hung off them — and the brackets say
	// so before it is read, the way they do around a token in prose. They also close the tag on its right, which is what lets a title
	// sit one space away without the two reading as one phrase.
	tag := flag + " " + tagOpen + s.tag + tagClose
	// The banner line is inset one cell — the rule runs the full width, and a
	// flag hard against column zero reads as that rule's end-cap rather than as
	// the first thing on the line. One cell for that inset, one for the gap
	// after the tag.
	room := max(1, width-cells(tag)-2)
	// The directory the session ran in, out at the right margin. A title says
	// what the work was and the path says what it was done to, which is the
	// half a title routinely leaves out — two sessions can carry the same
	// generated title, and under --everywhere a transcript runs across
	// projects, where the path is the only thing on the line telling them
	// apart.
	//
	// Whole, and not cut down to --root the way the session table's column is.
	// The table is read in the terminal that ran the command, with a summary
	// line above it naming the root; a transcript is routinely redirected to a
	// file and read somewhere else entirely, where `sub/` names nothing and
	// `.` names less. The one shortening it keeps is ~ for home, which is
	// unambiguous wherever the file is read.
	//
	// The title is served first and the path takes what is left: a path cut
	// from the left still names the directory it ends in, while a title cut
	// anywhere loses its subject. So the title keeps up to titleFloor cells —
	// the same floor the session table holds for it — before the path gets
	// any, and the pair are held two cells apart, which is padTo's own floor.
	//
	// What is left then has to be worth spending: a path squeezed to a couple
	// of cells is an ellipsis and one letter, noise at the margin rather than
	// an answer, so below whereFloor the path is dropped and the title takes
	// the width back. Like the table's floors this one is not owed — a path
	// short enough to fit whole is drawn whole, however little room there is.
	title, where := titleOf(s), homeTilde(s.directory)
	w := max(0, room-min(cells(title), titleFloor)-2)
	if w < min(cells(where), whereFloor) {
		w = 0
	}
	w = min(w, cells(where))
	if w == 0 {
		title, where = truncateCells(title, room), ""
	} else {
		title, where = truncateCells(title, room-w-2), tailCells(where, w)
	}
	// The rule under the tag is the envelope the rail used to draw: one line
	// clear across the width, saying everything below it belongs to this
	// session until the next rule. Unlike the rail it costs no column, and it
	// is drawn, not coloured, so --color never keeps it whole.
	//
	// Its weight says depth. A subagent's run is work some other session asked
	// for, so its rule is dashed where a top-level session's is solid — the
	// subordination reads off the line itself, at the width of the screen,
	// rather than off the ↓ hiding at the end of a tag.
	glyph := "─"
	if s.sub {
		glyph = "┈"
	}
	// Tag, title and rule wear the session's own hue rather than the frame blue
	// a turn head takes, so the banner reads as the thing the heads under it
	// sit inside rather than as another one of them — the tag and the rule
	// solid, the title a lighter step, one object across the line. One hue for
	// every banner: it says a line is a banner, and the tag beside it says
	// which.
	rule := paint.paint(strings.Repeat(glyph, max(1, width)), sessionColor)
	body := paint.paint(title, sessionTitle)
	if where != "" {
		body = padTo(body, paint.paint(where, sessionWhere), room)
	}
	// Two lines and no blanks. The air a banner needs on both sides to read as
	// one object is sessionGap above and bannerGap below, and both belong to
	// whoever is laying banners down rather than to the banner.
	return []string{
		" " + paint.paint(tag, sessionColor, true) + " " + body,
		rule,
	}
}

func titleOf(s *session) string {
	if s.title == "" {
		return "(untitled)"
	}
	return s.title
}
