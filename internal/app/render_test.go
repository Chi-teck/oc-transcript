package app

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// zooDB builds a fixture covering every message type and content item, all
// four tool statuses, truncated output, a tool that returned a file, a tool cut
// off before it finished, error turns in and out of the {type, message} shape,
// synthetic prose and a <task> envelope, a compaction, an attachment with a
// huge base64 payload, an assistant turn with no content, an idle row, and a
// CJK/emoji summary, spread over two days and three sessions (one a subagent).
func zooDB(t *testing.T) *testDB {
	t.Helper()
	d := newTestDB(t)
	day1 := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC).UnixMilli()
	day2 := time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC).UnixMilli()

	d.session("ses_zoo001", "", "Zoo main", "/proj", day1)
	d.session("ses_zoo002", "", "", "/proj/sub", day1+1000)
	d.session("ses_zoo003", "ses_zoo001", "Subtask", "/proj", day1+2000)

	// What the --list table's three figures are read from. zoo001 ran nine
	// minutes; zoo002 was started on day1 and last touched on day2, so its span
	// comes from the session row and not from its messages; zoo003 says nothing
	// about either, which is the blank cell the table has to draw.
	d.spent("ses_zoo001", day1+9*60_000, 12_000, 3_400, 88_000, 1_200)
	d.spent("ses_zoo002", day2+60_000, 900, 120, 0, 0)

	ts := day1
	next := func() int64 { ts += 60_000; return ts }
	// Each row takes the next seq in its session, as the store assigns it.
	add := func(id, sid, typ string, at int64, data string) {
		t.Helper()
		d.message(id, sid, typ, d.nextSeq(sid), at, data)
	}
	// A base64 image of ~200 KB, the size a pasted screenshot is. Neither the
	// attachment nor the tool's file item may put any of it on the screen.
	image := strings.Repeat("QUJD", 50_000)

	d.userText("msg_z01", "ses_zoo001", next(), "[chat | user=x]\nhello world")

	m2 := next()
	add("msg_z02", "ses_zoo001", "assistant", m2, `{"agent":"plan",
	  "model":{"id":"glm-5","providerID":"p","variant":"default"},
	  "tokens":{"input":10,"output":5,"reasoning":0,"cache":{"read":1,"write":2}},
	  "cost":0.0123,"finish":"tool-calls","snapshot":"cafe",
	  "time":{"created":`+fmt.Sprint(m2)+`,"completed":`+fmt.Sprint(m2+5000)+`},
	  "content":[`+strings.Join([]string{
		`{"type":"reasoning","text":"weighing the options\nacross two lines","time":{"created":1000}}`,
		`{"type":"text","text":"I will run tools.\n\n"}`,
		`{"type":"tool","id":"c1","name":"bash","state":{"status":"completed",
		  "input":{"command":"echo hi"},"content":[{"type":"text","text":"hi\n"}],
		  "metadata":{"output":"hi\n","exit":0}},
		  "time":{"created":1000,"completed":2200}}`,
		`{"type":"tool","id":"c2","name":"webfetch","state":{"status":"error",
		  "input":{"url":"http://x"},
		  "error":{"type":"tool.execution","message":"boom: connection refused"}},
		  "time":{"created":1000,"completed":1400}}`,
		`{"type":"tool","id":"c3","name":"bash","state":{"status":"running",
		  "input":{"command":"sleep 999"}},"time":{"created":1000}}`,
		`{"type":"tool","id":"c4","name":"guess","state":{"status":"pending",
		  "input":{"query":"q"}},"time":{"created":1000,"completed":3000}}`,
		`{"type":"tool","id":"c5","name":"bash","state":{"status":"completed",
		  "input":{"command":"ps aux"},"content":[{"type":"text","text":"USER PID\nroot 1\n"}],
		  "metadata":{"exit":0,"truncated":true,"outputPath":"/nonexistent/tool-output/tool_x"}},
		  "time":{"created":1000,"completed":1100}}`,
		`{"type":"tool","id":"c6","name":"glob","state":{"status":"completed",
		  "input":{"pattern":"*"},"content":[{"type":"text","text":"a\nb\n"}],
		  "metadata":{"count":100,"truncated":true}},
		  "time":{"created":1000,"completed":1050}}`,
		// A tool that returned a file: named by its type, the uri never shown.
		`{"type":"tool","id":"c8","name":"read","state":{"status":"completed",
		  "input":{"filePath":"/proj/shot.png"},
		  "content":[{"type":"text","text":"Image read successfully"},
		             {"type":"file","uri":"data:image/png;base64,` + image + `","mime":"image/png"}],
		  "metadata":{"truncated":false}},
		  "time":{"created":1000,"completed":1030}}`,
	}, ",")+`]}`)

	m3 := next()
	add("msg_z03", "ses_zoo001", "user", m3, `{"time":{"created":`+fmt.Sprint(m3)+`},
	  "text":"see attached",
	  "files":[{"name":"shot.png","mime":"image/png","data":"`+image+`",
	            "source":{"type":"inline"},"mention":{"text":"[Image 1]","start":0,"end":9}}],
	  "agents":[]}`)

	// Escape mid-turn: the turn carries the error, and the call it cut off has
	// an error of its own, a start and no end, and no output at all.
	m4 := next()
	add("msg_z04", "ses_zoo001", "assistant", m4, `{"model":{"id":"glm-5"},
	  "time":{"created":`+fmt.Sprint(m4)+`},
	  "error":{"type":"aborted","message":"Aborted"},
	  "content":[{"type":"text","text":"partial answer"},
	    {"type":"tool","id":"c9","name":"bash","state":{"status":"error",
	     "input":{"command":"make test"},
	     "error":{"type":"tool.interrupted","message":"Tool execution was interrupted"}},
	     "time":{"created":1000}}]}`)
	// An error that is not {type, message}: shown as the JSON it is. Off the
	// minute, so the turns after it keep their stamps.
	add("msg_z04b", "ses_zoo001", "assistant", m4+30_000, `{"model":{"id":"glm-5"},
	  "time":{"created":`+fmt.Sprint(m4+30_000)+`},
	  "error":{"name":"MessageAbortedError","data":{"message":"Aborted"}},
	  "content":[]}`)

	// Empty content and no error: must render nothing, not a stray header.
	add("msg_z05", "ses_zoo001", "assistant", next(), `{"model":{"id":"glm-5"},"content":[]}`)

	m6 := next()
	add("msg_z06", "ses_zoo001", "synthetic", m6, `{"time":{"created":`+fmt.Sprint(m6)+`},
	  "text":"continue where you left off"}`)
	// The envelope opencode injects when a background subagent finishes, right
	// after the synthetic prose above it: one golden shows both, so the record
	// being read as a record and the prose still being one-lined are the same
	// diff. It names ses_zoo003, whose banner is in this transcript, so the tag
	// on the line can be checked against the tag on the banner.
	add("msg_z06b", "ses_zoo001", "synthetic", m6+1, `{"time":{"created":`+fmt.Sprint(m6+1)+`},
	  "text":`+jsonStr(strings.Join([]string{
		`<task id="ses_zoo003" state="completed">`,
		`<summary>Background task completed: Sleep 15 seconds in background</summary>`,
		`<task_result>`,
		`15 seconds have passed.`,
		`</task_result>`,
		`</task>`,
	}, "\n"))+`}`)

	// The summary is tens of KB on a real store and is not drawn.
	m7 := next()
	add("msg_z07", "ses_zoo001", "compaction", m7, `{"status":"completed","reason":"auto",
	  "summary":"## Objective\nnot for the transcript","recent":"[Assistant reasoning]: neither",
	  "time":{"created":`+fmt.Sprint(m7)+`}}`)

	d.userText("msg_z08", "ses_zoo003", next(), "do the subtask")
	// Back to the main session once the subagent has had its turn: the banner
	// for a session the stream has already shown, which is neither a beginning
	// nor a conversation joined part-way.
	d.userText("msg_z08b", "ses_zoo001", next(), "and back to the main session")

	// Day two, second session: CJK and emoji in body and tool summary.
	d.userText("msg_z09", "ses_zoo002", day2, "日本語のコマンド 🎌 とても長い行")
	m10 := day2 + 60_000
	add("msg_z10", "ses_zoo002", "assistant", m10, `{"model":{"id":"glm-5"},"agent":"build",
	  "content":[{"type":"tool","id":"c7","name":"bash","state":{"status":"completed",
	    "input":{"command":"echo 日本語のコマンド 🎌 とても長い行 もっと長く もっと長く もっと長く"},
	    "content":[{"type":"text","text":"ok"}],"metadata":{"exit":0}},
	    "time":{"created":1000,"completed":1500}}]}`)
	// The session going idle after the turn: no content, nothing drawn, and not
	// a message in the --list count.
	add("msg_z11", "ses_zoo002", "idle", m10+30_000,
		`{"time":{"created":`+fmt.Sprint(m10+30_000)+`},"outcome":"succeeded"}`)
	return d
}

func runGolden(t *testing.T, d *testDB, name string, args ...string) string {
	t.Helper()
	return runGoldenAt(t, d, name, "", args...)
}

// runArgs runs the tool over the fixture at the given terminal width and hands
// back what it wrote to stdout; columns "" leaves COLUMNS unset, so the width
// falls back to 100.
func runArgs(t *testing.T, d *testDB, columns string, args ...string) string {
	t.Helper()
	t.Setenv("COLUMNS", columns)
	var stdout, stderr bytes.Buffer
	full := append([]string{"--db", d.path, "--everywhere", "--all"}, args...)
	if err := run(full, &stdout, &stderr); err != nil {
		t.Fatalf("run(%v): %v\nstderr: %s", args, err, stderr.String())
	}
	return stdout.String()
}

// runGoldenAt is runGolden at a given terminal width.
func runGoldenAt(t *testing.T, d *testDB, name, columns string, args ...string) string {
	t.Helper()
	got := runArgs(t, d, columns, args...)
	golden := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s (run with -update after intended changes)\n%s",
			name, golden, diffReport("golden", string(want), "got", got))
	}
	return got
}

// diffReport is what a failed comparison of two transcripts says. Printing the
// whole of one of them was no use: these run to a hundred lines and the
// question is always which one moved, so the answer is the first line they
// disagree on and the two readings of it, side by side and quoted so a
// trailing space or a stray escape is visible. The line counts come along
// because a line inserted or dropped shows up there and nowhere in the pair.
func diffReport(wantName, want, gotName, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	n, w, g, differs := firstDiff(wantLines, gotLines)
	if !differs {
		return "the two are identical" // the caller thought otherwise
	}
	return fmt.Sprintf("first difference at line %d:\n  %s: %q\n  %s: %q\n  (%d lines %s, %d %s)",
		n, wantName, w, gotName, g, len(wantLines), wantName, len(gotLines), gotName)
}

// firstDiff names the first line two texts disagree on: its 1-based number and
// the two readings, with a line the shorter text does not reach reported as
// absent rather than as empty.
func firstDiff(want, got []string) (n int, w, g string, differs bool) {
	at := func(lines []string, i int) string {
		if i < len(lines) {
			return lines[i]
		}
		return "<no such line>"
	}
	for i := range max(len(want), len(got)) {
		if w, g := at(want, i), at(got, i); w != g {
			return i + 1, w, g, true
		}
	}
	return 0, "", "", false
}

func TestRenderGolden(t *testing.T) {
	d := zooDB(t)
	cases := []struct {
		name    string
		columns string
		args    []string
	}{
		{"compact", "", nil},
		{"full", "", []string{"--tools", "full", "--reasoning", "--stats"}},
		{"none", "", []string{"--tools", "none"}},
		{"color", "", []string{"--color", "always", "--tools", "full", "--reasoning", "--stats"}},
		{"list", "", []string{"--list"}},
		{"caps", "", []string{"--arg-width", "20", "--max-chars", "40", "--tools", "full"}},
		// A terminal too narrow for the content, which is the only way to fix
		// what wrapping actually produces: every other golden renders at 100,
		// where nothing has to fold. The coloured twin is the one that matters
		// most — a span open where a line breaks has to be closed there and
		// reopened after the prefix, and nothing else in the suite says what
		// those bytes should be.
		{"narrow", "60", []string{"--tools", "full"}},
		{"narrow-color", "60", []string{"--color", "always", "--tools", "full"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := runGoldenAt(t, d, c.name, c.columns, c.args...)
			if strings.Contains(out, "data:image") || strings.Contains(out, "QUJD") {
				t.Error("a data: URI reached the output")
			}
		})
	}
}

// TestColourTransparency is the property the whole SGR apparatus exists to
// hold, and the one nothing else asserts: a coloured run prints what a plain
// run prints, with escapes threaded through it and not one visible cell moved.
//
// It is worth stating on its own because the layout has to take colour back out
// to do its job — visibleCells strips it to measure a line, and wrapLine has to
// recognise a span, close it where it breaks the line and reopen it after the
// prefix. All of that is machinery for keeping colour invisible to the layout,
// and this is the assertion that it did. The wrapping case is the one that can
// actually fail: a span dropped, doubled or reopened a cell late is invisible
// in a diff of coloured output and obvious here.
func TestColourTransparency(t *testing.T) {
	d := zooDB(t)
	for _, c := range []struct {
		name    string
		columns string
		args    []string
	}{
		{"nothing has to fold", "", []string{"--tools", "full", "--reasoning", "--stats"}},
		{"everything folds", "60", []string{"--tools", "full", "--reasoning", "--stats"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			plain := runArgs(t, d, c.columns, c.args...)
			coloured := runArgs(t, d, c.columns, append([]string{"--color", "always"}, c.args...)...)
			if coloured == plain {
				t.Fatal("--color always produced no escapes at all")
			}
			if got := stripSGR(coloured); got != plain {
				t.Error("stripped colour differs from the plain run\n" +
					diffReport("plain", plain, "stripped", got))
			}
		})
	}
}

func TestRenderTruncationMarks(t *testing.T) {
	d := zooDB(t)
	compact := runGolden(t, d, "compact")
	if strings.Count(compact, "[output truncated]") != 2 {
		t.Errorf("compact mode should mark both truncated tools:\n%s", compact)
	}
	full := runGolden(t, d, "full", "--tools", "full", "--reasoning", "--stats")
	if !strings.Contains(full, "truncated 16 chars shown, full output at") {
		t.Error("full mode should name the outputPath")
	}
	if !strings.Contains(full, "truncated 4 chars shown, the rest was not kept") {
		t.Error("full mode should say when nothing more was kept")
	}
}

func TestTruncatedNoteWithSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool_x")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 1234), 0o644); err != nil {
		t.Fatal(err)
	}
	state := &toolState{Metadata: json.RawMessage(`{"truncated":true,"outputPath":` + jsonStr(path) + `}`)}
	want := fmt.Sprintf("3 chars shown, full output at %s (1234 bytes)", path)
	if got := truncatedNote(state, "abc"); got != want {
		t.Errorf("truncatedNote = %q, want %q", got, want)
	}
}

func TestSessionBannerFlags(t *testing.T) {
	s := &session{sessionRow: sessionRow{id: "ses_aaaaaa", title: "Zoo", timeCreated: 100}, tag: "aaaaaa"}
	// Whether the session predates the window is a fact about the session, not
	// about what this particular run of blocks happens to contain — but a
	// banner for a session already announced answers a nearer question first,
	// and says so however the window fell.
	for _, c := range []struct {
		name  string
		since *int64
		seen  bool
		want  string
	}{
		{"started inside the window", new(int64(50)), false, newFlag},
		{"started before it", new(int64(200)), false, joinFlag},
		{"--all: no window to predate", nil, false, newFlag},
		{"come back to", new(int64(50)), true, backFlag},
		{"come back to, and older than the window", new(int64(200)), true, backFlag},
	} {
		if got := bannerFlag(s, c.since, c.seen); got != c.want {
			t.Errorf("%s: bannerFlag = %q, want %q", c.name, got, c.want)
		}
	}

	paint := Paint{}
	// A banner is its two own lines and no blank rows: the gap around it is the
	// assembler's.
	for _, flag := range []string{newFlag, joinFlag, backFlag} {
		want := " " + flag + " " + tagOpen + "aaaaaa" + tagClose + " Zoo"
		if got := sessionBanner(s, flag, paint, 40)[0]; !strings.HasPrefix(got, want) {
			t.Errorf("banner = %q, want prefix %q", got, want)
		}
	}
	// The flag is charged to the title's budget, so a banner still fits.
	wide := &session{sessionRow: sessionRow{title: strings.Repeat("t", 100)}, tag: strings.Repeat("g", 10)}
	for _, line := range sessionBanner(wide, newFlag, paint, 30) {
		if cells(line) > 30 {
			t.Errorf("banner line %q is %d cells", line, cells(line))
		}
	}
}

// The path on a banner answers what the title leaves out, and it is the one
// thing on the line that may be dropped: the title is what the banner is for.
func TestSessionBannerWhere(t *testing.T) {
	paint := Paint{}
	s := &session{sessionRow: sessionRow{title: "Zoo", directory: "/proj/sub"}, tag: "aaaaaa"}
	// Whole and absolute, out at the right margin, and the line still ends
	// exactly at the width. The root is not subtracted from it the way the
	// session table subtracts it: a transcript is read away from the terminal
	// that produced it, where a relative path names nothing.
	line := sessionBanner(s, newFlag, paint, 40)[0]
	if !strings.HasSuffix(line, " /proj/sub") || cells(line) != 40 {
		t.Errorf("banner = %q (%d cells), want a 40-cell line ending in the absolute path", line, cells(line))
	}
	// A session the store never gave a directory has nothing to say out there,
	// and says it without trailing blanks.
	blank := &session{sessionRow: sessionRow{title: "Zoo"}, tag: "aaaaaa"}
	if line := sessionBanner(blank, newFlag, paint, 40)[0]; line != strings.TrimRight(line, " ") {
		t.Errorf("banner = %q, want no trailing blanks where there is no directory", line)
	}
	// Too narrow to hold both: the title takes the room back rather than the
	// path being drawn as an ellipsis and a letter.
	narrow := &session{sessionRow: sessionRow{title: strings.Repeat("t", 100), directory: "/proj/deep/sub"}, tag: "aaaaaa"}
	for _, line := range sessionBanner(narrow, newFlag, paint, 30) {
		if cells(line) > 30 {
			t.Errorf("banner line %q is %d cells", line, cells(line))
		}
	}
	if line := sessionBanner(narrow, newFlag, paint, 30)[0]; strings.Contains(line, "…") && strings.Contains(line, "sub") {
		t.Errorf("banner = %q, want the path dropped at this width", line)
	}
}

func TestIndentBlockClamp(t *testing.T) {
	paint := Paint{}
	// A negative cap clamps to zero and reports what was actually dropped.
	got := indentBlock("out", "abcdef", -5, paint, grey)
	if !strings.Contains(got, "[6 more chars]") || strings.Contains(got, "abc") {
		t.Errorf("negative cap: %q", got)
	}
	// The cap counts runes, not bytes.
	got = indentBlock("out", "ééééé", 3, paint, grey)
	if !strings.Contains(got, "ééé\n") || !strings.Contains(got, "[2 more chars]") {
		t.Errorf("rune cap: %q", got)
	}
}

func TestOneLineAndSummary(t *testing.T) {
	if got := oneLineText("  a\t b\n c  ", 100); got != "a b c" {
		t.Errorf("whitespace collapse: %q", got)
	}
	if got := oneLineText("abcdef", 4); got != "abc…" {
		t.Errorf("cut: %q", got)
	}
	if got := oneLineText("abc", 0); got != "…" {
		t.Errorf("zero limit: %q", got)
	}
	if got := oneLineText("", 0); got != "" {
		t.Errorf("zero limit empty: %q", got)
	}
	// Cells, not code points: three double-width runes fill six cells.
	if got := oneLineText("日本語です", 6); got != "日本…" {
		t.Errorf("cjk cut: %q", got)
	}

	state := &toolState{Input: json.RawMessage(`{"pattern":"pat","extra":1}`)}
	if got := toolSummary(state, 100); got != "pat" {
		t.Errorf("known key wins: %q", got)
	}
	state = &toolState{Input: json.RawMessage(`{"unknown":1}`)}
	if got := toolSummary(state, 100); got != `{"unknown":1}` {
		t.Errorf("input fallback: %q", got)
	}
	state = &toolState{}
	if got := toolSummary(state, 100); got != "" {
		t.Errorf("nothing: %q", got)
	}
}

// TestSyntheticTask pins what is read out of the <task> envelope and what is
// refused: everything refused falls back to the one-lining every other
// synthetic part gets, so a shape this does not know still renders.
func TestSyntheticTask(t *testing.T) {
	env := func(lines ...string) string { return strings.Join(lines, "\n") }
	for _, c := range []struct {
		name                       string
		text                       string
		id, state, summary, result string
		ok                         bool
	}{
		{
			name: "the envelope opencode writes",
			text: env(
				`<task id="ses_fcd374ef6ffefHxrQ5xmwEgKGZ" state="completed">`,
				`<summary>Background task completed: Sleep 15 seconds in background</summary>`,
				`<task_result>`,
				`15 seconds have passed.`,
				`</task_result>`,
				`</task>`),
			id: "ses_fcd374ef6ffefHxrQ5xmwEgKGZ", state: "completed",
			summary: "Sleep 15 seconds in background", result: "15 seconds have passed.", ok: true,
		},
		{
			// The state is read off the attribute, and the prefix stripped off
			// the summary is the one that names that state — not a fixed
			// "Background task completed: ".
			name: "a task that failed, and no result with it",
			text: env(
				`<task id="ses_abc123" state="failed">`,
				`<summary>Background task failed: Sleep 15 seconds in background</summary>`,
				`</task>`),
			id: "ses_abc123", state: "failed", summary: "Sleep 15 seconds in background", ok: true,
		},
		{
			name: "a summary that does not carry the prefix is printed whole",
			text: env(
				`<task id="ses_abc123" state="completed">`,
				`<summary>Sleep 15 seconds in background</summary>`,
				`</task>`),
			id: "ses_abc123", state: "completed", summary: "Sleep 15 seconds in background", ok: true,
		},
		{
			// What the scanning buys over encoding/xml: agent output is not
			// escaped, and a result that would fail a parser is still a result.
			name: "a result holding a bare <",
			text: env(
				`<task id="ses_abc123" state="completed">`,
				`<summary>Background task completed: compare them</summary>`,
				`<task_result>`,
				`a < b, and & is fine too`,
				`</task_result>`,
				`</task>`),
			id: "ses_abc123", state: "completed", summary: "compare them",
			result: "a < b, and & is fine too", ok: true,
		},
		{
			name: "no summary: nothing to draw the line from",
			text: env(
				`<task id="ses_abc123" state="completed">`,
				`<task_result>`,
				`15 seconds have passed.`,
				`</task_result>`,
				`</task>`),
		},
		{
			name: "the synthetic shapes that are prose",
			text: "continue where you left off",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, state, summary, result, ok := syntheticTask(c.text)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if id != c.id || state != c.state || summary != c.summary || result != c.result {
				t.Errorf("= (%q, %q, %q, %q), want (%q, %q, %q, %q)",
					id, state, summary, result, c.id, c.state, c.summary, c.result)
			}
		})
	}
}

// TestEmptyReasoningIsNotABody pins the guard the reasoning case shares with
// text. opencode commits a reasoning item before the model has written a token
// into it, so an item that exists but is still empty must not fabricate a block:
// a header over a blank rail is what a live session printed before this.
func TestEmptyReasoningIsNotABody(t *testing.T) {
	d := newTestDB(t)
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	d.session("ses_a", "", "A", "/p", base)
	d.message("msg_1", "ses_a", "assistant", 1, base,
		`{"model":{"id":"m"},"content":[{"type":"reasoning","text":"  \n"}]}`)

	if got := runArgs(t, d, "", "--reasoning"); strings.Contains(got, "assistant") {
		t.Errorf("an empty reasoning item printed a turn:\n%s", got)
	}
}

// TestEmptySyntheticIsNotABody is the same guard on a synthetic row: one with
// no text must not print a head over a blank rail.
func TestEmptySyntheticIsNotABody(t *testing.T) {
	d := newTestDB(t)
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	d.session("ses_a", "", "A", "/p", base)
	d.message("msg_1", "ses_a", "synthetic", 1, base, `{"text":" \n"}`)

	if got := runArgs(t, d, ""); strings.Contains(got, "synthetic") {
		t.Errorf("an empty synthetic row printed a turn:\n%s", got)
	}
}
