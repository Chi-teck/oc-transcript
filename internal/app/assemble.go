package app

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// transcript is what one build() produced: the rendered lines, the sessions
// that contributed, where the tail should resume, and — when there are no
// lines — why.
type transcript struct {
	lines []string
	used  []*session
	last  *cursor // the last message emitted
	seam  seam
	empty string
	held  *heldTurn
}

// heldTurn is the in-flight message a --follow round refused to print: the
// round stopped there, so the cursor did not move past it and nothing after it
// was emitted either.
type heldTurn struct {
	tag       string // the held session's short handle, for the notice
	messageID string
}

// seam is the banner state one --follow round hands the next: which session
// the stream is in the middle of, so a banner is not repeated, and which
// sessions have had a banner at all, so one the stream comes back to after a
// poll is flagged as a return rather than as a beginning — the seam is not a
// place the reader can see, so nothing about a banner may depend on which side
// of it the banner falls.
type seam struct {
	prevSess string
	seen     map[string]bool
}

// scope works out which sessions are in play: everything under the root,
// tagged, then narrowed by --session.
func scope(ctx context.Context, st *store, opts *options) (all, wanted []*session, empty string, err error) {
	rows, err := st.sessions(ctx, opts.root)
	if err != nil {
		return nil, nil, "", err
	}
	if len(rows) == 0 {
		if opts.root == "" {
			return nil, nil, "this database has no sessions at all", nil
		}
		return nil, nil, fmt.Sprintf("no opencode session has ever run under %s — try --root, or --everywhere", opts.root), nil
	}
	all = tagSessions(rows)

	wanted = all
	if opts.sessions != nil {
		wanted = slices.DeleteFunc(slices.Clone(wanted), func(s *session) bool {
			for _, frag := range opts.sessions {
				if strings.Contains(s.id, frag) {
					return false
				}
			}
			return true
		})
	}
	if len(wanted) == 0 {
		empty = fmt.Sprintf("%d session(s) in scope, none matching --session", len(all))
	}
	return all, wanted, empty, nil
}

// build renders the transcript, plus a line saying why it is empty when it is.
//
// Coming back with nothing has three unrelated causes — no session ever ran
// under the root, the filters excluded the ones that did, or the window holds
// no message — and a reader told the wrong one goes looking in the wrong
// place. Whenever no lines come back, `empty` is set.
//
// `q` bounds the messages: the --since/--until window for the opening
// transcript, a keyset cursor for each --follow round. prev carries the banner
// state across rounds so a tail neither repeats a banner nor forgets a session
// it has already announced.
func build(ctx context.Context, st *store, opts *options, q messageQuery, prev seam) (*transcript, error) {
	_, wanted, empty, err := scope(ctx, st, opts)
	if err != nil {
		return nil, err
	}
	// The round's own copy: what it hands on is what it was given plus what it
	// announced, and the caller's map is not written through.
	carried := seam{prevSess: prev.prevSess, seen: maps.Clone(prev.seen)}
	if carried.seen == nil {
		carried.seen = map[string]bool{}
	}
	t := &transcript{seam: carried}
	if empty != "" {
		t.empty = empty
		return t, nil
	}
	byID := make(map[string]*session, len(wanted))
	ids := make([]string, 0, len(wanted))
	for _, s := range wanted {
		byID[s.id] = s
		ids = append(ids, s.id)
	}
	slices.Sort(ids)
	messages, err := st.messages(ctx, ids, q)
	if err != nil {
		return nil, err
	}
	if opts.follow {
		if n := settledPrefix(messages); n < len(messages) {
			stop := messages[n]
			t.held = &heldTurn{tag: byID[stop.sessionID].tag, messageID: stop.id}
			messages = messages[:n]
		}
	}
	mids := make([]string, len(messages))
	for i, m := range messages {
		mids[i] = m.id
	}
	parts, err := st.parts(ctx, mids)
	if err != nil {
		return nil, err
	}

	// Messages render independently, so the JSON decoding — the bulk of the
	// work — fans out across cores; assembly below stays sequential, so the
	// output is byte-for-byte what a single worker would have produced.
	blocks := make([][]string, len(messages))
	var cursorIdx atomic.Int64
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), max(1, len(messages))) {
		wg.Go(func() {
			for {
				i := int(cursorIdx.Add(1)) - 1
				if i >= len(messages) {
					return
				}
				blocks[i] = renderMessage(messages[i], parts[messages[i].id], opts)
			}
		})
	}
	wg.Wait()

	// standing is what the stream already ends in, and the round's first gap is
	// the only one that has to care. A --follow round is appended to a
	// transcript that ended in endGap rows: those are out there and nothing can
	// take them back, so the first break lays down the difference and no more.
	// After that the round is writing on ground it owns.
	standing := 0
	if prev.prevSess != "" {
		standing = endGap
	}
	gap := func(rows int) {
		for range max(0, rows-standing) {
			t.lines = append(t.lines, "")
		}
		standing = 0
	}

	usedSet := map[string]bool{}
	for i, msg := range messages {
		s := byID[msg.sessionID]
		block := blocks[i]
		if len(block) == 0 {
			continue
		}
		if !usedSet[s.id] {
			usedSet[s.id] = true
			t.used = append(t.used, s)
		}
		if s.id != t.seam.prevSess {
			flag := bannerFlag(s, opts.since, t.seam.seen[s.id])
			gap(sessionGap)
			t.lines = append(t.lines, sessionBanner(s, flag, opts.paint, opts.width)...)
			gap(bannerGap)
			t.seam.prevSess = s.id
			t.seam.seen[s.id] = true
		} else {
			gap(turnGap)
		}
		t.lines = append(t.lines, block...)
		t.last = &cursor{timeCreated: msg.timeCreated, id: msg.id}
	}
	if len(t.lines) > 0 {
		gap(endGap)
	}
	if len(t.lines) == 0 {
		if t.held != nil {
			t.empty = fmt.Sprintf("nothing settled yet — the turn in %s is still being written", t.held.tag)
		} else {
			hint := " — try --all"
			if opts.all {
				hint = ""
			}
			t.empty = fmt.Sprintf("nothing from %d session(s) in this window%s", len(wanted), hint)
		}
	}
	return t, nil
}

// settledPrefix is how much of a --follow round may be emitted: everything up
// to the first message still being written. A message row is written when a
// turn starts and its parts stream in afterwards, so rendering one mid-flight
// prints half a turn — and the cursor then advances past it, so the rest is
// never read again. A turn therefore appears whole or not at all.
//
// A message is settled when it cannot grow any more parts:
//
//	a newer message exists in the same session // a next turn implies this one is done
//	role != "assistant"                        // a prompt is written whole
//	data.error is set                          // aborted or failed
//	data.time.completed is set                 // finished normally
//
// The round stops at the first unsettled message rather than skipping it: the
// cursor is one global keyset position, so emitting a later message means
// advancing past the held one, which is the bug this exists to prevent. A live
// turn therefore holds every session in scope, not only its own.
//
// The tail of a session *in this batch* is the tail of the session, because
// --follow refuses --until (cli.go:201) and so never reads a bounded window.
// Relax that and "nothing after it here" stops meaning "nothing after it", and
// finished turns are held forever.
func settledPrefix(messages []messageRow) int {
	tail := make(map[string]int, len(messages))
	for i, m := range messages {
		tail[m.sessionID] = i
	}
	for i, m := range messages {
		// Only a message with nothing after it in its session can still be
		// growing, so the decode inside settled runs at most once per session
		// — and on the opening transcript of a --follow run, that batch is
		// every message in the project.
		if i == tail[m.sessionID] && !settled(m) {
			return i
		}
	}
	return len(messages)
}

func settled(msg messageRow) bool {
	var data messageData
	if err := json.Unmarshal(msg.data, &data); err != nil {
		return true // renderMessage warns about it; the gate does not double up
	}
	switch {
	case string(data.Role) != "assistant":
		return true
	case truthy(data.Error):
		return true
	case data.Time != nil && truthy(data.Time.Completed):
		return true
	}
	return false
}
