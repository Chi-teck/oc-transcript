package app

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"regexp"
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
			if _, err := d.db.Exec(
				"insert into message (id, session_id, time_created, data) values (?,?,?,?)",
				mid, "ses_a", ts, `{"role":"user"}`); err != nil {
				writerDone <- err
				return
			}
			if _, err := d.db.Exec(
				"insert into part (id, message_id, session_id, time_created, data) values (?,?,?,?,?)",
				"prt_"+mid, mid, "ses_a", ts, `{"type":"text","text":"row-`+fmt.Sprintf("%03d", i)+`"}`); err != nil {
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
