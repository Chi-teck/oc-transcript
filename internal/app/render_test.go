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

// zooDB builds a fixture covering every part type, all four tool statuses,
// truncated output, an error turn, synthetic text, a huge data: URI, and a
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

	d.userText("msg_z01", "ses_zoo001", next(), "[chat | user=x]\nhello world")

	m2 := next()
	d.message("msg_z02", "ses_zoo001", m2,
		`{"role":"assistant","modelID":"glm-5","variant":"default","agent":"plan"}`)
	for i, part := range []string{
		`{"type":"step-start","snapshot":"cafe"}`,
		`{"type":"reasoning","text":"weighing the options\nacross two lines"}`,
		`{"type":"text","text":"I will run tools.\n\n"}`,
		`{"type":"tool","tool":"bash","callID":"c1","state":{"status":"completed",
		  "input":{"command":"echo hi"},"output":"hi\n","metadata":{"exit":0},
		  "time":{"start":1000,"end":2200}}}`,
		`{"type":"tool","tool":"webfetch","callID":"c2","state":{"status":"error",
		  "input":{"url":"http://x"},"error":"boom: connection refused",
		  "time":{"start":1000,"end":1400}}}`,
		`{"type":"tool","tool":"bash","callID":"c3","state":{"status":"running",
		  "input":{"command":"sleep 999"},"time":{"start":1000}}}`,
		`{"type":"tool","tool":"guess","callID":"c4","state":{"status":"pending",
		  "input":{"query":"q"},"time":{"start":1000,"end":3000}}}`,
		`{"type":"tool","tool":"bash","callID":"c5","state":{"status":"completed",
		  "input":{"command":"ps aux"},"output":"USER PID\nroot 1\n",
		  "metadata":{"exit":0,"truncated":true,"outputPath":"/nonexistent/tool-output/tool_x"},
		  "time":{"start":1000,"end":1100}}}`,
		`{"type":"tool","tool":"glob","callID":"c6","state":{"status":"completed",
		  "input":{"pattern":"*"},"output":"a\nb\n","metadata":{"count":100,"truncated":true},
		  "time":{"start":1000,"end":1050}}}`,
		`{"type":"step-finish","reason":"tool-calls","cost":0.0123,
		  "tokens":{"input":10,"output":5,"cache":{"read":1,"write":2}}}`,
	} {
		d.part(fmt.Sprintf("prt_z02%02d", i), "msg_z02", "ses_zoo001", m2+int64(i), part)
	}

	m3 := next()
	d.message("msg_z03", "ses_zoo001", m3, `{"role":"user"}`)
	dataURI := "data:image/png;base64," + strings.Repeat("QUJD", 50_000) // ~200 KB
	d.part("prt_z03a", "msg_z03", "ses_zoo001", m3,
		`{"type":"file","mime":"image/png","filename":"shot.png","url":`+jsonStr(dataURI)+`}`)
	d.part("prt_z03b", "msg_z03", "ses_zoo001", m3+1, `{"type":"text","text":"see attached"}`)

	m4 := next()
	d.message("msg_z04", "ses_zoo001", m4,
		`{"role":"assistant","modelID":"glm-5",
		  "error":{"name":"MessageAbortedError","data":{"message":"Aborted"}}}`)
	d.part("prt_z04", "msg_z04", "ses_zoo001", m4, `{"type":"text","text":"partial answer"}`)

	// Zero parts and no error: must render nothing, not a stray header.
	d.message("msg_z05", "ses_zoo001", next(), `{"role":"assistant","modelID":"glm-5"}`)

	m6 := next()
	d.message("msg_z06", "ses_zoo001", m6, `{"role":"user"}`)
	d.part("prt_z06", "msg_z06", "ses_zoo001", m6,
		`{"type":"text","text":"continue where you left off","synthetic":true}`)

	m7 := next()
	d.message("msg_z07", "ses_zoo001", m7, `{"role":"assistant","modelID":"glm-5"}`)
	d.part("prt_z07a", "msg_z07", "ses_zoo001", m7,
		`{"type":"patch","hash":"deadbeefcafe","files":["/proj/a.go","/proj/b.go"]}`)
	d.part("prt_z07b", "msg_z07", "ses_zoo001", m7+1, `{"type":"compaction","auto":true}`)

	d.userText("msg_z08", "ses_zoo003", next(), "do the subtask")
	// Back to the main session once the subagent has had its turn: the banner
	// for a session the stream has already shown, which is neither a beginning
	// nor a conversation joined part-way.
	d.userText("msg_z08b", "ses_zoo001", next(), "and back to the main session")

	// Day two, second session: CJK and emoji in body and tool summary.
	d.userText("msg_z09", "ses_zoo002", day2, "日本語のコマンド 🎌 とても長い行")
	m10 := day2 + 60_000
	d.message("msg_z10", "ses_zoo002", m10, `{"role":"assistant","modelID":"glm-5","agent":"build"}`)
	d.part("prt_z10", "msg_z10", "ses_zoo002", m10,
		`{"type":"tool","tool":"bash","callID":"c7","state":{"status":"completed",
		  "input":{"command":"echo 日本語のコマンド 🎌 とても長い行 もっと長く もっと長く もっと長く"},
		  "output":"ok","metadata":{"exit":0},"time":{"start":1000,"end":1500}}}`)
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
	state := &toolState{Output: "abc", Metadata: json.RawMessage(`{"truncated":true,"outputPath":` + jsonStr(path) + `}`)}
	want := fmt.Sprintf("3 chars shown, full output at %s (1234 bytes)", path)
	if got := truncatedNote(state); got != want {
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

	state := &toolState{Input: json.RawMessage(`{"pattern":"pat","extra":1}`), Title: "the title"}
	if got := toolSummary(state, 100); got != "pat" {
		t.Errorf("known key wins: %q", got)
	}
	state = &toolState{Input: json.RawMessage(`{"unknown":1}`), Title: "the title"}
	if got := toolSummary(state, 100); got != "the title" {
		t.Errorf("title fallback: %q", got)
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
