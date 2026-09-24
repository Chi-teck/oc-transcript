package app

import (
	"bufio"
	"context"
	"fmt"
	"time"
)

// follow polls for messages newer than what has been printed and appends each
// turn once it has settled — see settledPrefix — until interrupted.
//
// The cursor is a keyset — the (time_created, seq, id) of the last message actually
// emitted — and each round asks for rows strictly past it, in the same
// (time_created, seq, id) order the transcript is read in. That is race-free even
// for rows sharing a millisecond, which a wall-clock cursor cannot separate
// and so re-emits or drops. Until something has been emitted the window's
// own start bounds the poll, so nothing between the opening transcript and
// the first new message is skipped. The session banner carries over from round
// to round, and the tail goes to the same writer the opening transcript went
// to — the file when --out was given.
func follow(ctx context.Context, st *store, opts *options, w *bufio.Writer, cur *cursor, carry seam) error {
	ticker := time.NewTicker(time.Duration(opts.interval * float64(time.Second)))
	defer ticker.Stop()

	// A held turn is invisible: a model thinking for two minutes and a server
	// killed mid-turn produce the same empty rounds, and the cursor is one
	// global position, so the second wedges every session in scope. Say so
	// once, on stderr, where the transcript's sink is not.
	const holdNotice = 30 * time.Second
	var heldID string
	var heldSince time.Time
	var said bool

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		q := messageQuery{after: cur}
		if cur == nil {
			q.since = opts.since
		}
		round, err := build(ctx, st, opts, q, carry)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if len(round.lines) > 0 {
			if err := writeLines(w, round.lines); err != nil {
				return fmt.Errorf("cannot write: %v", err)
			}
		}
		if round.last != nil {
			cur = round.last
		}
		carry = round.seam
		switch {
		case round.held == nil:
			heldID, said = "", false
		case round.held.messageID != heldID:
			heldID, heldSince, said = round.held.messageID, time.Now(), false
		case !said && time.Since(heldSince) >= holdNotice:
			opts.warnf("waiting on the turn in %s: unfinished after %s — the tail is held until it completes",
				round.held.tag, holdNotice)
			said = true
		}
	}
}
