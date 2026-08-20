package app

import (
	"slices"
	"testing"
)

func TestTagSessionsMarksSubagents(t *testing.T) {
	rows := []sessionRow{
		{id: "ses_aaaaaa", timeCreated: 1},
		{id: "ses_bbbbbb", parentID: "ses_aaaaaa", timeCreated: 2}, // child
		{id: "ses_cccccc", parentID: "ses_bbbbbb", timeCreated: 3}, // grandchild — three-level chain
		{id: "ses_dddddd", timeCreated: 4},
		{id: "ses_eeeeee", parentID: "ses_offscr", timeCreated: 5}, // parent out of scope
	}
	sessions := tagSessions(rows)

	byID := map[string]*session{}
	for _, s := range sessions {
		byID[s.id] = s
	}
	for _, id := range []string{"ses_bbbbbb", "ses_cccccc"} {
		if !byID[id].sub {
			t.Errorf("%s: sub=false, want a subagent down the parent chain", id)
		}
	}
	for _, id := range []string{"ses_aaaaaa", "ses_dddddd", "ses_eeeeee"} {
		if byID[id].sub {
			t.Errorf("%s: sub=true, want a top-level session", id)
		}
	}

	if got := byID["ses_aaaaaa"].tag; got != "aaaaaa" {
		t.Errorf("tag = %q", got)
	}
	if got := byID["ses_bbbbbb"].tag; got != "bbbbbb↓" {
		t.Errorf("subagent tag = %q", got)
	}

	// The input order is the output order: a caller reading the list back gets
	// the sessions as they started.
	got := make([]string, len(sessions))
	for i, s := range sessions {
		got[i] = s.id
	}
	want := []string{"ses_aaaaaa", "ses_bbbbbb", "ses_cccccc", "ses_dddddd", "ses_eeeeee"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// An id shorter than the tag is its own tag, rather than being padded or
// panicking on the slice.
func TestTagForShortID(t *testing.T) {
	if got := tagFor("abc"); got != "abc" {
		t.Errorf("tagFor(short) = %q", got)
	}
}
