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
		// Columns the tool never reads (agent, model, cost) are here so the
		// fixture keeps the same shape as the real store.
		`create table session (id text primary key, parent_id text, title text,
		   directory text, agent text, model text, time_created integer,
		   time_updated integer, cost real, tokens_input integer,
		   tokens_output integer, tokens_cache_read integer,
		   tokens_cache_write integer)`,
		`create table message (id text primary key, session_id text,
		   time_created integer, data text)`,
		`create table part (id text primary key, message_id text, session_id text,
		   time_created integer, data text)`,
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
		"insert into session (id, parent_id, title, directory, time_created) values (?,?,?,?,?)",
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
		`update session set time_updated = ?, tokens_input = ?, tokens_output = ?,
		   tokens_cache_read = ?, tokens_cache_write = ? where id = ?`,
		updated, in, out, cacheRead, cacheWrite, id); err != nil {
		d.t.Fatal(err)
	}
}

func (d *testDB) message(id, sid string, ts int64, data string) {
	d.t.Helper()
	if _, err := d.db.Exec(
		"insert into message (id, session_id, time_created, data) values (?,?,?,?)",
		id, sid, ts, data); err != nil {
		d.t.Fatal(err)
	}
}

func (d *testDB) part(id, mid, sid string, ts int64, data string) {
	d.t.Helper()
	if _, err := d.db.Exec(
		"insert into part (id, message_id, session_id, time_created, data) values (?,?,?,?,?)",
		id, mid, sid, ts, data); err != nil {
		d.t.Fatal(err)
	}
}

// userText adds a one-part user message and returns nothing it doesn't need to.
func (d *testDB) userText(mid, sid string, ts int64, text string) {
	d.t.Helper()
	d.message(mid, sid, ts, `{"role":"user","time":{"created":`+fmt.Sprint(ts)+`}}`)
	d.part("prt_"+mid, mid, sid, ts, `{"type":"text","text":`+jsonStr(text)+`}`)
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

func TestStoreIsReadOnly(t *testing.T) {
	d := newTestDB(t)
	st := d.open(t)
	if _, err := st.db.Exec("insert into session (id, directory, time_created) values ('x','/x',1)"); err == nil {
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
// results must merge into exactly the order one statement would have given.
func TestMessagesChunkedMerge(t *testing.T) {
	d := newTestDB(t)
	tx, err := d.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	const n = 1200
	ids := make([]string, n)
	type key struct {
		ts int64
		id string
	}
	var want []key
	for i := range n {
		sid := fmt.Sprintf("ses_%04d", i)
		ids[i] = sid
		if _, err := tx.Exec("insert into session (id, directory, time_created) values (?,?,?)",
			sid, "/p", int64(i)); err != nil {
			t.Fatal(err)
		}
		// Timestamps interleave across chunks, and rows tie every 100 apart so
		// the id tiebreak matters; message ids run opposite to session ids.
		ts := int64((i * 7) % 100)
		mid := fmt.Sprintf("msg_%04d", n-1-i)
		if _, err := tx.Exec("insert into message (id, session_id, time_created, data) values (?,?,?,?)",
			mid, sid, ts, `{"role":"user"}`); err != nil {
			t.Fatal(err)
		}
		want = append(want, key{ts, mid})
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(want, func(a, b key) int {
		if a.ts != b.ts {
			return int(a.ts - b.ts)
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
		if m.timeCreated != want[i].ts || m.id != want[i].id {
			t.Fatalf("row %d = (%d, %s), want (%d, %s)", i, m.timeCreated, m.id, want[i].ts, want[i].id)
		}
	}
}

func TestMessagesWindowAndCursor(t *testing.T) {
	d := newTestDB(t)
	d.session("ses_a", "", "", "/p", 1)
	for i, ts := range []int64{10, 20, 20, 30} {
		d.message(fmt.Sprintf("msg_%d", i), "ses_a", ts, `{"role":"user"}`)
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
	after, err := st.messages(ctx, []string{"ses_a"}, messageQuery{after: &cursor{timeCreated: 20, id: "msg_1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].id != "msg_2" || after[1].id != "msg_3" {
		t.Errorf("cursor rows = %v", after)
	}
}

func TestPartsOrderAndZeroParts(t *testing.T) {
	d := newTestDB(t)
	d.session("ses_a", "", "", "/p", 1)
	d.message("msg_1", "ses_a", 10, `{"role":"user"}`)
	d.message("msg_2", "ses_a", 20, `{"role":"assistant"}`) // zero parts
	// Ids sort against timestamps: time order must win.
	d.part("prt_z", "msg_1", "ses_a", 1, `{"type":"text","text":"one"}`)
	d.part("prt_a", "msg_1", "ses_a", 2, `{"type":"text","text":"two"}`)
	d.part("prt_b", "msg_1", "ses_a", 2, `{"type":"text","text":"three"}`)
	st := d.open(t)
	got, err := st.parts(context.Background(), []string{"msg_1", "msg_2"})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, p := range got["msg_1"] {
		order = append(order, p.id)
	}
	if want := []string{"prt_z", "prt_a", "prt_b"}; !slices.Equal(order, want) {
		t.Errorf("part order = %v, want %v", order, want)
	}
	if len(got["msg_2"]) != 0 {
		t.Errorf("zero-part message got parts: %v", got["msg_2"])
	}
}
