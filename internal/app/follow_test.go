package app

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestRoundSeamSpacing pins the spacing across a --follow poll boundary to what
// one pass would have produced: two blank rows between turns, and two before a
// session banner. A round cannot take back rows already written, so each ends
// on the single row every seam has in common and the next opens with whatever
// more that seam needed.
func TestRoundSeamSpacing(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	d := newTestDB(t)
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	d.session("ses_a", "", "A", "/p", base)
	d.session("ses_b", "", "B", "/p", base)
	d.userText("msg_1", "ses_a", base, "one")

	var stdout, stderr bytes.Buffer
	opts, err := parseArgs([]string{"--db", d.path, "--everywhere", "--all"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	opts.stderr = &stderr

	ctx := context.Background()
	st, err := openStore(ctx, d.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	// Three rounds, each seeing exactly one message: the opening pass, then a
	// turn in the same session, then a turn in another one.
	var stream []string
	round := func(q messageQuery, carry seam) *transcript {
		t.Helper()
		got, err := build(ctx, st, opts, q, carry)
		if err != nil {
			t.Fatal(err)
		}
		stream = append(stream, got.lines...)
		return got
	}
	opening := round(messageQuery{}, seam{})
	d.userText("msg_2", "ses_a", base+1000, "two")
	next := round(messageQuery{after: opening.last}, opening.seam)
	d.userText("msg_3", "ses_b", base+2000, "three")
	third := round(messageQuery{after: next.last}, next.seam)
	// Back to the first session a round later. Which flag its banner takes is
	// the other thing the seam carries: nothing in this round has seen ses_a
	// before, and the reader has.
	d.userText("msg_4", "ses_a", base+3000, "four")
	round(messageQuery{after: third.last}, third.seam)

	gap := func(needle string) int {
		t.Helper()
		for i, line := range stream {
			if !strings.Contains(line, needle) {
				continue
			}
			n := 0
			for j := i - 1; j >= 0 && stream[j] == ""; j-- {
				n++
			}
			return n
		}
		t.Fatalf("%q not in stream:\n%s", needle, strings.Join(stream, "\n"))
		return 0
	}
	if got := gap("12:00:01 • user"); got != 2 {
		t.Errorf("blank rows before a turn across the seam = %d, want 2", got)
	}
	if got := gap(tagOpen + "ses_b" + tagClose + " B"); got != 2 {
		t.Errorf("blank rows before a session banner across the seam = %d, want 2", got)
	}
	banner := func(flag, id string) bool {
		t.Helper()
		for _, line := range stream {
			if strings.Contains(line, flag+" "+tagOpen+id+tagClose) {
				return true
			}
		}
		return false
	}
	if !banner(newFlag, "ses_b") {
		t.Errorf("first banner for ses_b is not flagged as a beginning:\n%s", strings.Join(stream, "\n"))
	}
	if !banner(backFlag, "ses_a") {
		t.Errorf("ses_a is not flagged as a return across the seam:\n%s", strings.Join(stream, "\n"))
	}
	if got := stream[len(stream)-1]; got != "" {
		t.Errorf("stream ends on %q, want a blank row", got)
	}
	if got := stream[len(stream)-2]; got == "" {
		t.Error("stream ends on two blank rows, want one")
	}
}

// TestFollowExactlyOnce guards --follow against duplicates: a writer inserts
// rows while the tail polls, several rows share a millisecond, and every one
// must be emitted exactly once — none dropped, none repeated. The keyset
// cursor is what makes that hold.
func TestFollowExactlyOnce(t *testing.T) {
	t.Setenv("COLUMNS", "")
	d := newTestDB(t)
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	d.session("ses_a", "", "t", "/p", base)
	// Two rows exist before the tail starts; the opening transcript takes them.
	d.userText("msg_pre0", "ses_a", base, "pre-0")
	d.userText("msg_pre1", "ses_a", base+1, "pre-1")

	var stdout, stderr bytes.Buffer
	opts, err := parseArgs([]string{"--db", d.path, "--everywhere", "--all", "--interval", "0.02"},
		&stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	opts.stderr = &stderr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := openStore(ctx, d.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	opening, err := build(ctx, st, opts, messageQuery{since: opts.since, until: opts.until}, seam{})
	if err != nil {
		t.Fatal(err)
	}
	if opening.last == nil || opening.last.id != "msg_pre1" {
		t.Fatalf("opening cursor = %+v", opening.last)
	}

	// The writer paces itself against the poller: rows land in bursts that
	// straddle poll boundaries, and each burst shares one millisecond.
	const rows = 40
	writerDone := make(chan error, 1)
	go func() {
		defer close(writerDone)
		for i := range rows {
			ts := base + 1000 + int64(i/4) // four rows per millisecond
			mid := fmt.Sprintf("msg_%03d", i)
			// seq runs with the insert order, the way the store assigns it.
			if _, err := d.db.Exec(
				"insert into session_message (id, session_id, type, seq, time_created, data) values (?,?,?,?,?,?)",
				mid, "ses_a", "user", int64(100+i), ts, `{"text":"row-`+fmt.Sprintf("%03d", i)+`"}`); err != nil {
				writerDone <- err
				return
			}
			time.Sleep(3 * time.Millisecond)
		}
	}()

	w := bufio.NewWriter(&stdout)
	followErr := make(chan error, 1)
	go func() { followErr <- follow(ctx, st, opts, w, opening.last, opening.seam) }()

	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond) // a few more polls to drain the tail
	cancel()
	if err := <-followErr; err != nil {
		t.Fatal(err)
	}

	out := stdout.String()
	for i := range rows {
		want := fmt.Sprintf("row-%03d", i)
		if got := strings.Count(out, want); got != 1 {
			t.Errorf("%s emitted %d times, want exactly once", want, got)
		}
	}
	// The opening rows must not be re-emitted by the tail.
	for _, pre := range []string{"pre-0", "pre-1"} {
		if got := strings.Count(out, pre); got != 0 {
			t.Errorf("opening row %q re-emitted %d times by the tail", pre, got)
		}
	}
	// One banner from the tail's first block at most — day rule and banner are
	// carried across rounds, never reprinted per poll.
	if banners := strings.Count(out, "ses_a  t"); banners > 1 {
		t.Errorf("session banner reprinted %d times", banners)
	}
	if rules := regexp.MustCompile(`(?m)^\d{4}-\d{2}-\d{2} ─`).FindAllString(out, -1); len(rules) > 1 {
		t.Errorf("day rule reprinted %d times", len(rules))
	}
}

// roundOpts parses the flags a build() test needs, with warnings collected
// where a failure can print them.
func roundOpts(t *testing.T, d *testDB, extra ...string) *options {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts, err := parseArgs(append([]string{"--db", d.path, "--everywhere", "--all"}, extra...),
		&stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	opts.stderr = &stderr
	return opts
}

// liveTurn writes what opencode leaves behind mid-stream: the assistant row
// is committed when the turn starts, with no completion stamp, and its content
// is rewritten in place while the model produces it. What exists at this
// instant is half a reasoning block.
func liveTurn(d *testDB, mid, sid string, ts int64) {
	d.t.Helper()
	d.message(mid, sid, "assistant", d.nextSeq(sid), ts,
		`{"model":{"id":"m"},"time":{"created":`+fmt.Sprint(ts)+`},
		  "content":[{"type":"reasoning","text":"weighing the"}]}`)
}

// finishTurn is the rest of that write sequence: the reasoning grows, the
// answer lands, and the completion stamp goes in with the last rewrite.
func finishTurn(d *testDB, mid string, ts int64) {
	d.t.Helper()
	if _, err := d.db.Exec("update session_message set data = ? where id = ?",
		`{"model":{"id":"m"},"time":{"created":`+fmt.Sprint(ts)+`,"completed":`+fmt.Sprint(ts+4)+`},
		  "finish":"stop",
		  "content":[{"type":"reasoning","text":"weighing the options"},{"type":"text","text":"the answer"}]}`,
		mid); err != nil {
		d.t.Fatal(err)
	}
}

// TestFollowHoldsUnsettledTurn is the regression this gate exists for: a poll
// landing inside a turn used to render whatever parts had arrived and advance
// the cursor past the message, so the parts written afterwards were never read
// and the transcript kept half a turn forever. The turn must now appear once,
// whole, in the round after it completes — and the two rounds together must be
// what one pass over the finished fixture prints.
func TestFollowHoldsUnsettledTurn(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	d := newTestDB(t)
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	d.session("ses_a", "", "A", "/p", base)
	d.userText("msg_1", "ses_a", base, "hello")
	liveTurn(d, "msg_live", "ses_a", base+1000)

	ctx := context.Background()
	st := d.open(t)
	opts := roundOpts(t, d, "--follow", "--reasoning")

	first, err := build(ctx, st, opts, messageQuery{}, seam{})
	if err != nil {
		t.Fatal(err)
	}
	if first.held == nil || first.held.messageID != "msg_live" {
		t.Fatalf("held = %+v, want the in-flight assistant turn", first.held)
	}
	if first.last == nil || first.last.id != "msg_1" {
		t.Fatalf("cursor = %+v, want it left on the user turn", first.last)
	}
	if got := strings.Join(first.lines, "\n"); strings.Contains(got, "weighing the") {
		t.Errorf("the in-flight turn was emitted:\n%s", got)
	}

	finishTurn(d, "msg_live", base+1000)

	second, err := build(ctx, st, opts, messageQuery{after: first.last}, first.seam)
	if err != nil {
		t.Fatal(err)
	}
	if second.held != nil {
		t.Errorf("held = %+v after the turn completed, want nil", second.held)
	}
	if second.last == nil || second.last.id != "msg_live" {
		t.Fatalf("cursor = %+v, want it past the assistant turn", second.last)
	}
	if got := strings.Join(second.lines, "\n"); strings.Contains(got, "hello") {
		t.Errorf("the user turn was repeated:\n%s", got)
	}

	// The contract: the tail is byte-for-byte a suffix of what the same range
	// prints in one pass. Only the accumulated stream can say so — a round
	// opens on ground that already ends in endGap and lays down the difference.
	onePass, err := build(ctx, st, roundOpts(t, d, "--reasoning"), messageQuery{}, seam{})
	if err != nil {
		t.Fatal(err)
	}
	got := slices.Concat(first.lines, second.lines)
	if !slices.Equal(got, onePass.lines) {
		t.Errorf("two rounds:\n%s\n\nnot one pass:\n%s",
			strings.Join(got, "\n"), strings.Join(onePass.lines, "\n"))
	}
}

// TestFollowStallBreaker pins the clause that keeps a dead turn from wedging
// the tail: a server killed mid-stream leaves a message with neither a
// completion stamp nor an error, and the moment anything else happens in that
// session the turn is settled by having a successor.
func TestFollowStallBreaker(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	d := newTestDB(t)
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	d.session("ses_a", "", "A", "/p", base)
	liveTurn(d, "msg_live", "ses_a", base+1000)
	d.userText("msg_next", "ses_a", base+2000, "still there?")

	got, err := build(context.Background(), d.open(t), roundOpts(t, d, "--follow", "--reasoning"),
		messageQuery{}, seam{})
	if err != nil {
		t.Fatal(err)
	}
	if got.held != nil {
		t.Errorf("held = %+v, want nil: the dead turn has a successor", got.held)
	}
	if got.last == nil || got.last.id != "msg_next" {
		t.Fatalf("cursor = %+v, want it past both turns", got.last)
	}
	out := strings.Join(got.lines, "\n")
	for _, want := range []string{"weighing the", "still there?"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q not emitted:\n%s", want, out)
		}
	}
}

// TestFollowUnsettledHoldsLaterSessions pins stop-don't-skip. The cursor is one
// global keyset position, so emitting session B's finished turn would mean
// advancing past session A's live one and losing the rest of it. A live turn
// holds every session in scope, not only its own.
func TestFollowUnsettledHoldsLaterSessions(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	d := newTestDB(t)
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	d.session("ses_a", "", "A", "/p", base)
	d.session("ses_b", "", "B", "/p", base)
	liveTurn(d, "msg_live", "ses_a", base+1000)
	d.userText("msg_b1", "ses_b", base+2000, "elsewhere")

	got, err := build(context.Background(), d.open(t), roundOpts(t, d, "--follow", "--reasoning"),
		messageQuery{}, seam{})
	if err != nil {
		t.Fatal(err)
	}
	if got.held == nil || got.held.messageID != "msg_live" {
		t.Fatalf("held = %+v, want session A's in-flight turn", got.held)
	}
	if len(got.lines) != 0 {
		t.Errorf("emitted past the held turn:\n%s", strings.Join(got.lines, "\n"))
	}
	if got.last != nil {
		t.Errorf("cursor = %+v, want it left where it was", got.last)
	}
	// The reader is told which of the two reasons an empty round has.
	if !strings.Contains(got.empty, "ses_a") || !strings.Contains(got.empty, "still being written") {
		t.Errorf("empty = %q, want it to name the session being waited on", got.empty)
	}
}

// TestFollowEmitsAbortedTurn covers the error clause: escape ends a turn with
// no completion stamp, and what exists is what there will be, so the turn goes
// out in the round it is seen rather than waiting for a successor.
func TestFollowEmitsAbortedTurn(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	d := newTestDB(t)
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	d.session("ses_a", "", "A", "/p", base)
	d.message("msg_ab", "ses_a", "assistant", 1, base+1000,
		`{"model":{"id":"m"},"time":{"created":`+fmt.Sprint(base+1000)+`},
		  "error":{"type":"aborted","message":"Aborted"},
		  "content":[{"type":"text","text":"half an answer"}]}`)

	got, err := build(context.Background(), d.open(t), roundOpts(t, d, "--follow"),
		messageQuery{}, seam{})
	if err != nil {
		t.Fatal(err)
	}
	if got.held != nil {
		t.Errorf("held = %+v, want nil: an aborted turn is over", got.held)
	}
	if got.last == nil || got.last.id != "msg_ab" {
		t.Fatalf("cursor = %+v, want it past the aborted turn", got.last)
	}
	if out := strings.Join(got.lines, "\n"); !strings.Contains(out, "half an answer") {
		t.Errorf("the aborted turn was not emitted:\n%s", out)
	}
}

// TestUnsettledTurnPrintsWithoutFollow pins the gate to the flag: a one-shot
// run prints what is in the store at that moment, in-flight turns and all, and
// has no cursor to burn. A refactor keying the gate on q.after would move the
// golden files.
func TestUnsettledTurnPrintsWithoutFollow(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	d := newTestDB(t)
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	d.session("ses_a", "", "A", "/p", base)
	d.userText("msg_1", "ses_a", base, "hello")
	liveTurn(d, "msg_live", "ses_a", base+1000)

	got, err := build(context.Background(), d.open(t), roundOpts(t, d, "--reasoning"),
		messageQuery{}, seam{})
	if err != nil {
		t.Fatal(err)
	}
	if got.held != nil {
		t.Errorf("held = %+v without --follow, want nil", got.held)
	}
	if out := strings.Join(got.lines, "\n"); !strings.Contains(out, "weighing the") {
		t.Errorf("the in-flight turn was withheld without --follow:\n%s", out)
	}
	if got.last == nil || got.last.id != "msg_live" {
		t.Fatalf("cursor = %+v, want it past the in-flight turn", got.last)
	}
}

// TestFollowIdleSettlesTurn pins that an idle row needs no case of its own in
// the gate: it is a later message in the session, so the assistant turn before
// it is settled even with no completion stamp — and the idle row itself is
// written whole and renders nothing, so the cursor stays on the turn.
func TestFollowIdleSettlesTurn(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	d := newTestDB(t)
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	d.session("ses_a", "", "A", "/p", base)
	d.userText("msg_1", "ses_a", base, "hello")
	liveTurn(d, "msg_live", "ses_a", base+1000)
	d.message("msg_idle", "ses_a", "idle", d.nextSeq("ses_a"), base+2000,
		`{"time":{"created":`+fmt.Sprint(base+2000)+`},"outcome":"succeeded"}`)

	got, err := build(context.Background(), d.open(t), roundOpts(t, d, "--follow", "--reasoning"),
		messageQuery{}, seam{})
	if err != nil {
		t.Fatal(err)
	}
	if got.held != nil {
		t.Errorf("held = %+v, want nil: an idle row follows the turn", got.held)
	}
	if got.last == nil || got.last.id != "msg_live" {
		t.Fatalf("cursor = %+v, want it on the assistant turn", got.last)
	}
	if out := strings.Join(got.lines, "\n"); !strings.Contains(out, "weighing the") {
		t.Errorf("the settled turn was not emitted:\n%s", out)
	}
}

// TestSettledByType is the gate's table: only an assistant row can still be
// growing, and only until it carries an error or a completion stamp.
func TestSettledByType(t *testing.T) {
	for _, c := range []struct {
		typ, data string
		want      bool
	}{
		{"user", `{"text":"hi"}`, true},
		{"synthetic", `{"text":"continue"}`, true},
		{"compaction", `{"status":"completed","reason":"auto"}`, true},
		{"idle", `{"outcome":"succeeded"}`, true},
		{"assistant", `{"time":{"created":1},"content":[]}`, false},
		{"assistant", `{"time":{"created":1},"error":{"type":"aborted","message":"Aborted"}}`, true},
		{"assistant", `{"time":{"created":1,"completed":2}}`, true},
	} {
		if got := settled(messageRow{typ: c.typ, data: []byte(c.data)}); got != c.want {
			t.Errorf("settled(%s %s) = %v, want %v", c.typ, c.data, got, c.want)
		}
	}
}
