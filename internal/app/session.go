package app

// tagFor is the short handle for a session: the tail of its id, which is the
// part that varies — an opencode id is a fixed prefix and a sortable stamp, so
// the first characters of two sessions started the same day are the same
// characters.
func tagFor(sessionID string) string {
	if r := []rune(sessionID); len(r) > 6 {
		return string(r[len(r)-6:])
	}
	return sessionID
}

// session is a sessionRow plus what tagSessions works out about it.
type session struct {
	sessionRow
	tag string
	sub bool
}

// tagSessions gives every session a tag and marks the subagent ones. The list
// is returned in the order it came in.
func tagSessions(rows []sessionRow) []*session {
	byID := make(map[string]bool, len(rows))
	ordered := make([]*session, 0, len(rows))
	for _, row := range rows {
		byID[row.id] = true
		ordered = append(ordered, &session{sessionRow: row})
	}
	for _, s := range ordered {
		// A subagent is one whose parent is in scope too: the mark says this
		// run of work was asked for by another session on the screen.
		s.sub = s.parentID != "" && byID[s.parentID]
		s.tag = tagFor(s.id)
		if s.sub {
			s.tag += "↓"
		}
	}
	return ordered
}
