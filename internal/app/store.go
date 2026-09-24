package app

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "modernc.org/sqlite" // database/sql driver, pure Go
)

// The store is opened read-only and nothing here writes to it. The traps this
// code steps around: scope by session_v2.directory, order by time_created with
// seq, then id, as tiebreakers, chunk every IN (…) list.

// chunkSize bounds the bound-parameter count of one statement. The ceiling
// varies by SQLite build (999 before 3.32), so never rely on the ambient limit.
const chunkSize = 500

type store struct {
	db *sql.DB
}

type sessionRow struct {
	id, parentID, title, directory string
	timeCreated                    int64
	// The running totals opencode denormalizes onto the session, for the
	// columns of the --list table that describe the session rather than the
	// window. timeUpdated is 0 when the store has none — no real millisecond
	// stamp is — and hasTokens tells a session that spent nothing apart from one
	// the store never accounted for.
	timeUpdated int64
	tokens      int64
	hasTokens   bool
}

type messageRow struct {
	id, sessionID, typ string
	timeCreated, seq   int64
	data               []byte
}

// openStore opens the database read-only and probes it, so a missing file, an
// unreadable WAL, a file that is not a database or a database opencode 2 has
// not migrated all fail here, with a one-line message, rather than on the first
// query.
func openStore(ctx context.Context, path string) (*store, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("no opencode database at %s (try `opencode db path`)", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("no opencode database at %s (try `opencode db path`)", path)
	}
	// `file:` DSN: the driver hands it to sqlite3_open_v2 with SQLITE_OPEN_URI,
	// so mode=ro reaches SQLite and the handle is genuinely read-only.
	//
	// busy_timeout: WAL means a reader never blocks on the writer, but the
	// server checkpointing or restarting the WAL can still hand back
	// SQLITE_BUSY. A --follow polling for hours meets that eventually, and
	// waiting five seconds is better than ending the tail.
	dsn := "file:" + uriEscape(abs) + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err == nil {
		db.SetMaxOpenConns(1)
		err = db.PingContext(ctx)
		if err == nil {
			var n int
			err = db.QueryRowContext(ctx, "select count(*) from sqlite_master"+
				" where type='table' and name='session_message'").Scan(&n)
			if err == nil && n == 0 {
				_ = db.Close()
				return nil, fmt.Errorf("%s is not an opencode 2 database", path)
			}
		}
	}
	if err != nil {
		if db != nil {
			_ = db.Close()
		}
		// The newline is deliberate: the second line names the fix.
		//nolint:staticcheck // ST1005: user-facing CLI text, not a wrapped error
		return nil, fmt.Errorf("cannot open %s read-only: %v\n"+
			"opencode uses WAL; the -wal and -shm files next to it must be readable too.", path, err)
	}
	return &store{db: db}, nil
}

// uriEscape escapes the characters that would be mis-read inside a file: URI.
func uriEscape(p string) string {
	r := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23")
	return r.Replace(p)
}

func (s *store) Close() error { return s.db.Close() }

// sessions returns every session whose working directory sits inside root, in
// time order. Scoping by directory rather than by opencode's project id is
// deliberate: the id is derived from the enclosing git worktree, so unrelated
// work can land under the same one, and a session started in a subdirectory
// of a project still belongs to it. An empty root drops the scoping and
// takes the whole database.
func (s *store) sessions(ctx context.Context, root string) ([]sessionRow, error) {
	// The token total is the four figures --stats reports per step — in, out,
	// cached, written — summed over the session, not a fifth reading of what a
	// session spent. tokens_reasoning is left out of it: providers that report
	// it count it inside the output figure, so adding it would count those
	// tokens twice. Cache reads dominate the total on any session that ran more
	// than a turn or two, which is worth knowing before reading a large one as
	// an expensive one. The sum coalesces so one absent column does not null the
	// whole figure, and the flag beside it says whether any of the four was
	// there at all — a session the store never accounted for is not a session
	// that spent nothing.
	query := `select id, parent_id, title, directory, time_created, time_updated,
	    coalesce(tokens_input, 0) + coalesce(tokens_output, 0) +
	      coalesce(tokens_cache_read, 0) + coalesce(tokens_cache_write, 0),
	    tokens_input is not null or tokens_output is not null or
	      tokens_cache_read is not null or tokens_cache_write is not null
	  from session_v2`
	var args []any
	if root != "" {
		// The boundary is the platform's separator: a Windows opencode stores
		// `C:\…` directories, and a boundary that does not match drops every
		// session in a subdirectory from its own root.
		//
		// substr and not like: like is ASCII case-insensitive by default, so it
		// would fold /foo into /Foo — two unrelated projects on a case-sensitive
		// filesystem — while the `directory = ?` half beside it stays exact.
		// Both halves of the predicate have to agree, and comparing the prefix
		// outright is also the end of the escaping the like form needed.
		prefix := root + string(filepath.Separator)
		query += " where directory = ? or substr(directory, 1, length(?)) = ?"
		args = append(args, root, prefix, prefix)
	}
	// id only as a tiebreaker: two sessions can share a millisecond, and the
	// order they come back in has to be the same on every run.
	query += " order by time_created, id"
	var out []sessionRow
	rows, err := s.db.QueryContext(ctx, query, args...)
	err = scanAll(rows, err, func(rows *sql.Rows) error {
		var r sessionRow
		var parent, title, dir sql.NullString
		var updated sql.NullInt64
		var hasTokens int64
		if err := rows.Scan(&r.id, &parent, &title, &dir, &r.timeCreated,
			&updated, &r.tokens, &hasTokens); err != nil {
			return err
		}
		r.parentID, r.title, r.directory = parent.String, title.String, dir.String
		r.timeUpdated, r.hasTokens = updated.Int64, hasTokens != 0
		out = append(out, r)
		return nil
	})
	return out, err
}

// messageCounts returns how many messages each of the given sessions holds.
//
// The whole session is counted, never the --since/--until window: the figures
// beside a session in the --list table describe the session, the way its start
// time already does, and a count that moved with the window would be the only
// one of them that did.
//
// It is a count of messages and not of the blocks a transcript draws from them.
// Every row but an idle one counts: idle marks the end of a turn and carries
// nothing. An assistant turn that left no content behind renders as nothing and
// is still a message, so a session can show more here than a reader can count
// below it — and a subagent is a session of its own, so a parent's count stops
// at its own turns.
func (s *store) messageCounts(ctx context.Context, ids []string) (map[string]int, error) {
	out := make(map[string]int, len(ids))
	for chunk := range slices.Chunk(ids, chunkSize) {
		query := "select session_id, count(*) from session_message where session_id in (" +
			placeholders(len(chunk)) + ") and type <> 'idle' group by session_id"
		rows, err := s.db.QueryContext(ctx, query, anySlice(chunk)...)
		err = scanAll(rows, err, func(rows *sql.Rows) error {
			var sid string
			var n int
			if err := rows.Scan(&sid, &n); err != nil {
				return err
			}
			out[sid] = n
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// cursor is a keyset position in the (time_created, seq, id) order messages
// are read in: the row it names has been emitted, everything after it has not.
type cursor struct {
	timeCreated, seq int64
	id               string
}

// messageQuery bounds a messages() call. since/until are the --since/--until
// window (nil = open); after, when set, replaces since with "strictly past
// this row" in (time_created, seq, id) order, which is what --follow needs to
// be race-free.
type messageQuery struct {
	since, until *int64
	after        *cursor
}

// messages returns the messages of the given sessions inside the query bounds,
// ordered by (time_created, seq, id). seq comes before id because the v2
// migration split some messages into rows that share a time_created, and only
// seq keeps them in the order they were written. The id list is chunked, so
// the result is merged and re-sorted; SQLite's default BINARY collation is
// byte order, the same as strings.Compare, so the merged order is what one
// statement would have produced.
func (s *store) messages(ctx context.Context, ids []string, q messageQuery) ([]messageRow, error) {
	var out []messageRow
	for chunk := range slices.Chunk(ids, chunkSize) {
		query := "select id, session_id, type, seq, time_created, data from session_message where session_id in (" +
			placeholders(len(chunk)) + ")"
		args := anySlice(chunk)
		switch {
		case q.after != nil:
			query += " and (time_created > ? or (time_created = ? and" +
				" (seq > ? or (seq = ? and id > ?))))"
			args = append(args, q.after.timeCreated, q.after.timeCreated,
				q.after.seq, q.after.seq, q.after.id)
		case q.since != nil:
			query += " and time_created >= ?"
			args = append(args, *q.since)
		}
		if q.until != nil {
			query += " and time_created < ?"
			args = append(args, *q.until)
		}
		query += " order by time_created, seq, id"
		rows, err := s.db.QueryContext(ctx, query, args...)
		err = scanAll(rows, err, func(rows *sql.Rows) error {
			var r messageRow
			if err := rows.Scan(&r.id, &r.sessionID, &r.typ, &r.seq, &r.timeCreated, &r.data); err != nil {
				return err
			}
			out = append(out, r)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(out, func(a, b messageRow) int {
		if a.timeCreated != b.timeCreated {
			return cmp.Compare(a.timeCreated, b.timeCreated)
		}
		if a.seq != b.seq {
			return cmp.Compare(a.seq, b.seq)
		}
		return strings.Compare(a.id, b.id)
	})
	return out, nil
}

// scanAll drains a query, calling scan once per row, and folds the error
// handling — query failure, scan failure, iteration failure — into one place.
func scanAll(rows *sql.Rows, err error, scan func(*sql.Rows) error) error {
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
