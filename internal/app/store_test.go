package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestMain pins the zone so golden files and rendered timestamps do not
// depend on the machine the tests run on.
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

// testDB is a synthetic opencode store in a temp dir. Writing to a fixture is
// fine; the repo's read-only rule is about the real store.
type testDB struct {
	t    *testing.T
	path string
	db   *sql.DB
}

func newTestDB(t *testing.T) *testDB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		"pragma journal_mode = wal",
		// Columns the tool never reads (agent, model, cost, version) are here so
		// the fixture keeps the same shape as the real store.
		`create table session_v2 (id text primary key, parent_id text, title text,
		   directory text, agent text, model text, version text, time_created integer,
		   time_updated integer, cost real, tokens_input integer,
		   tokens_output integer, tokens_cache_read integer,
		   tokens_cache_write integer)`,
		`create table session_message (id text primary key, session_id text,
		   type text not null, seq integer not null, time_created integer,
		   time_updated integer, data text)`,
		`create unique index session_message_seq on session_message (session_id, seq)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return &testDB{t: t, path: path, db: db}
}

func (d *testDB) session(id, parent, title, dir string, ts int64) {
	d.t.Helper()
	var p, ttl any
	if parent != "" {
		p = parent
	}
	if title != "" {
		ttl = title
	}
	if _, err := d.db.Exec(
		"insert into session_v2 (id, parent_id, title, directory, time_created) values (?,?,?,?,?)",
		id, p, ttl, dir, ts); err != nil {
		d.t.Fatal(err)
	}
}

// spent fills in the totals opencode denormalizes onto a session — when it was
// last touched and what it cost. Kept apart from session() so that the sessions
// which say nothing about either keep the NULLs a fresh row has, which is the
// other half of what the table has to render.
func (d *testDB) spent(id string, updated, in, out, cacheRead, cacheWrite int64) {
	d.t.Helper()
	if _, err := d.db.Exec(
		`update session_v2 set time_updated = ?, tokens_input = ?, tokens_output = ?,
		   tokens_cache_read = ?, tokens_cache_write = ? where id = ?`,
		updated, in, out, cacheRead, cacheWrite, id); err != nil {
		d.t.Fatal(err)
	}
}

// message adds one session_message row. seq is the per-session position the
// store assigns, unique within a session.
func (d *testDB) message(id, sid, typ string, seq, ts int64, data string) {
	d.t.Helper()
	if _, err := d.db.Exec(
		`insert into session_message (id, session_id, type, seq, time_created, time_updated, data)
		   values (?,?,?,?,?,?,?)`,
		id, sid, typ, seq, ts, ts, data); err != nil {
		d.t.Fatal(err)
	}
}

// nextSeq is the seq a row appended to the session now would take.
func (d *testDB) nextSeq(sid string) int64 {
	d.t.Helper()
	var n int64
	if err := d.db.QueryRow("select coalesce(max(seq), 0) + 1 from session_message where session_id = ?",
		sid).Scan(&n); err != nil {
		d.t.Fatal(err)
	}
	return n
}

// userText appends a user message holding only text.
func (d *testDB) userText(mid, sid string, ts int64, text string) {
	d.t.Helper()
	d.message(mid, sid, "user", d.nextSeq(sid), ts,
		`{"time":{"created":`+fmt.Sprint(ts)+`},"text":`+jsonStr(text)+`}`)
}

func jsonStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func (d *testDB) open(t *testing.T) *store {
	t.Helper()
	st, err := openStore(context.Background(), d.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// ---------------------------------------------------------------- tests

func TestOpenStoreErrors(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "nope.db")
	if _, err := openStore(ctx, missing); err == nil ||
		err.Error() != fmt.Sprintf("no opencode database at %s (try `opencode db path`)", missing) {
		t.Errorf("missing file: %v", err)
	}
	notdb := filepath.Join(t.TempDir(), "not.db")
	if err := os.WriteFile(notdb, []byte("just text, no sqlite header at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(ctx, notdb); err == nil ||
		!strings.HasPrefix(err.Error(), fmt.Sprintf("cannot open %s read-only:", notdb)) ||
		!strings.Contains(err.Error(), "-wal and -shm") {
		t.Errorf("non-database file: %v", err)
	}
}

// A database opencode 2 has not migrated has only the v1 tables. It must be
// refused at open, by name, rather than failing later on a missing table.
func TestOpenStorePreV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`create table session (id text primary key, directory text, time_created integer)`,
		`create table message (id text primary key, session_id text, time_created integer, data text)`,
		`create table part (id text primary key, message_id text, session_id text, time_created integer, data text)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := openStore(context.Background(), path)
	if err == nil {
		_ = st.Close()
		t.Fatal("a pre-v2 database opened")
	}
	if !strings.Contains(err.Error(), "is not an opencode 2 database") {
		t.Errorf("pre-v2 database: %v", err)
	}
}

func TestStoreIsReadOnly(t *testing.T) {
	d := newTestDB(t)
	st := d.open(t)
	if _, err := st.db.Exec("insert into session_v2 (id, directory, time_created) values ('x','/x',1)"); err == nil {
		t.Fatal("write on the read-only handle succeeded")
	}
}

func TestSessionScoping(t *testing.T) {
	d := newTestDB(t)
	// Spelled with the platform separator, the way the opencode that wrote the
	// row would have spelled them: the scoping matches on that separator.
	root := filepath.Join("/home/u", "proj_abc")
	d.session("ses_a", "", "in root", root, 1)
	d.session("ses_b", "", "in subdir", filepath.Join(root, "deep", "er"), 2)
	d.session("ses_c", "", "wildcard decoy", filepath.Join("/home/u", "projXabc"), 3)
	d.session("ses_d", "", "percent decoy", filepath.Join("/home/u", "proj%abc"), 4)
	d.session("ses_e", "", "sibling", root+"2", 5)
	// Differs from the root in case alone: on a case-sensitive filesystem that
	// is somebody else's project, and a case-insensitive match would splice it
	// into this transcript.
	d.session("ses_f", "", "case decoy", filepath.Join("/home/u", "Proj_ABC", "deep"), 6)
	// Written in the order that leaves the id order to the tiebreaker.
	d.session("ses_z", "", "same millisecond", root, 7)
	d.session("ses_y", "", "same millisecond", root, 7)
	st := d.open(t)
	ctx := context.Background()

	rows, err := st.sessions(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.id)
	}
	if want := []string{"ses_a", "ses_b", "ses_y", "ses_z"}; !slices.Equal(got, want) {
		t.Errorf("scoped ids = %v, want %v", got, want)
	}

	all, err := st.sessions(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 8 {
		t.Errorf("unscoped rows = %d, want 8", len(all))
	}
	// Time order, not id order.
	if all[0].id != "ses_a" || all[4].id != "ses_e" {
		t.Errorf("unscoped order = %v", all)
	}
}

// TestMessagesChunkedMerge is the regression test for the unchunked IN list:
// 1200 sessions exceed the pre-3.32 variable limit of 999, and the chunked
// results must merge into exactly the order one statement would have given —
// time_created, then seq, then id.
func TestMessagesChunkedMerge(t *testing.T) {
	d := newTestDB(t)
	tx, err := d.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	const n = 1200
	ids := make([]string, n)
	type key struct {
		ts, seq int64
		id      string
	}
	var want []key
	for i := range n {
		sid := fmt.Sprintf("ses_%04d", i)
		ids[i] = sid
		if _, err := tx.Exec("insert into session_v2 (id, directory, time_created) values (?,?,?)",
			sid, "/p", int64(i)); err != nil {
			t.Fatal(err)
		}
		// Timestamps interleave across chunks, and rows tie every 100 apart so
		// the tiebreaks matter: seq cycles through three values, and message ids
		// run opposite to session ids, so among rows equal on both the id decides.
		ts := int64((i * 7) % 100)
		seq := int64(i % 3)
		mid := fmt.Sprintf("msg_%04d", n-1-i)
		if _, err := tx.Exec("insert into session_message (id, session_id, type, seq, time_created, data) values (?,?,?,?,?,?)",
			mid, sid, "user", seq, ts, `{}`); err != nil {
			t.Fatal(err)
		}
		want = append(want, key{ts, seq, mid})
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(want, func(a, b key) int {
		if a.ts != b.ts {
			return int(a.ts - b.ts)
		}
		if a.seq != b.seq {
			return int(a.seq - b.seq)
		}
		return strings.Compare(a.id, b.id)
	})

	st := d.open(t)
	got, err := st.messages(context.Background(), ids, messageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("messages = %d, want %d", len(got), n)
	}
	for i, m := range got {
		if m.timeCreated != want[i].ts || m.seq != want[i].seq || m.id != want[i].id {
			t.Fatalf("row %d = (%d, %d, %s), want (%d, %d, %s)",
				i, m.timeCreated, m.seq, m.id, want[i].ts, want[i].seq, want[i].id)
		}
	}
}

func TestMessagesWindowAndCursor(t *testing.T) {
	d := newTestDB(t)
	d.session("ses_a", "", "", "/p", 1)
	for i, ts := range []int64{10, 20, 20, 30} {
		d.message(fmt.Sprintf("msg_%d", i), "ses_a", "user", int64(i+1), ts, `{}`)
	}
	st := d.open(t)
	ctx := context.Background()

	lo, hi := int64(20), int64(30)
	got, err := st.messages(ctx, []string{"ses_a"}, messageQuery{since: &lo, until: &hi})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].id != "msg_1" || got[1].id != "msg_2" {
		t.Errorf("window rows = %v", got)
	}

	// The keyset cursor takes the second of two rows sharing a millisecond.
	after, err := st.messages(ctx, []string{"ses_a"}, messageQuery{after: &cursor{timeCreated: 20, seq: 2, id: "msg_1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].id != "msg_2" || after[1].id != "msg_3" {
		t.Errorf("cursor rows = %v", after)
	}
}

// TestMessagesSeqBeforeID pins the case seq is in the order for: the v2
// migration split a v1 message into two rows sharing a time_created, and the
// random id tail sorts them against the order they were written in. seq has
// to win, in the query and across a cursor placed between the two — an
// id-keyed cursor there would re-read the first row or skip the second.
func TestMessagesSeqBeforeID(t *testing.T) {
	d := newTestDB(t)
	d.session("ses_a", "", "", "/p", 1)
	d.message("msg_0", "ses_a", "user", 1, 10, `{}`)
	d.message("msg_x9", "ses_a", "user", 2, 20, `{}`)      // written first, sorts last by id
	d.message("msg_x1", "ses_a", "synthetic", 3, 20, `{}`) // written second, sorts first by id
	d.message("msg_2", "ses_a", "assistant", 4, 30, `{}`)
	st := d.open(t)
	ctx := context.Background()

	ids := func(rows []messageRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.id)
		}
		return out
	}
	all, err := st.messages(ctx, []string{"ses_a"}, messageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(all), []string{"msg_0", "msg_x9", "msg_x1", "msg_2"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}

	between, err := st.messages(ctx, []string{"ses_a"},
		messageQuery{after: &cursor{timeCreated: 20, seq: 2, id: "msg_x9"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(between), []string{"msg_x1", "msg_2"}; !slices.Equal(got, want) {
		t.Errorf("after the first of the pair = %v, want %v", got, want)
	}

	past, err := st.messages(ctx, []string{"ses_a"},
		messageQuery{after: &cursor{timeCreated: 20, seq: 3, id: "msg_x1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(past), []string{"msg_2"}; !slices.Equal(got, want) {
		t.Errorf("after the second of the pair = %v, want %v", got, want)
	}
}

// Every row but an idle one is a message: idle marks the end of a turn and
// carries nothing, while synthetic and compaction rows were v1 user messages.
func TestMessageCountsSkipIdle(t *testing.T) {
	d := newTestDB(t)
	d.session("ses_a", "", "", "/p", 1)
	d.session("ses_b", "", "", "/p", 2)
	for i, typ := range []string{"user", "assistant", "synthetic", "compaction", "idle", "idle"} {
		d.message(fmt.Sprintf("msg_a%d", i), "ses_a", typ, int64(i+1), int64(10+i), `{}`)
	}
	d.message("msg_b0", "ses_b", "idle", 1, 10, `{}`)
	got, err := d.open(t).messageCounts(context.Background(), []string{"ses_a", "ses_b"})
	if err != nil {
		t.Fatal(err)
	}
	if got["ses_a"] != 4 || got["ses_b"] != 0 {
		t.Errorf("counts = %v, want ses_a 4 and ses_b 0", got)
	}
}
