package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func parse(t *testing.T, args ...string) (*options, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return parseArgs(args, &stdout, &stderr)
}

// Bad flag syntax exits 2; bad values inside an accepted flag exit 1.
func isUsage(err error) bool { return errors.As(err, new(usageError)) }

func TestParseArgsEnums(t *testing.T) {
	for _, args := range [][]string{
		{"--tools", "sideways"},
		{"--color", "sometimes"},
		{"--tools", "FULL"},
	} {
		_, err := parse(t, args...)
		if err == nil || !isUsage(err) || !strings.Contains(err.Error(), "must be one of") {
			t.Errorf("parse(%v) = %v, want a usage error naming the choices", args, err)
		}
	}
	opts, err := parse(t, "--tools", "none", "--color", "never")
	if err != nil || opts.tools != "none" || opts.color != "never" {
		t.Errorf("valid enums: %v %+v", err, opts)
	}
}

func TestParseArgsUnknown(t *testing.T) {
	if _, err := parse(t, "--nope"); err == nil || !isUsage(err) {
		t.Errorf("unknown flag: %v", err)
	}
	// No argparse-style abbreviation: --everyw is unknown, not --everywhere.
	if _, err := parse(t, "--everyw"); err == nil || !isUsage(err) {
		t.Errorf("abbreviated flag: %v", err)
	}
	// -tools must not parse as a shorthand cluster.
	if _, err := parse(t, "-tools", "full"); err == nil || !isUsage(err) {
		t.Errorf("-tools: %v", err)
	}
	if _, err := parse(t, "stray"); err == nil || !isUsage(err) ||
		!strings.Contains(err.Error(), "unrecognized arguments: stray") {
		t.Errorf("positional: %v", err)
	}
}

func TestParseArgsSessions(t *testing.T) {
	opts, err := parse(t, "--session", "x", "--session", "y")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"x", "y"}; strings.Join(opts.sessions, ",") != strings.Join(want, ",") {
		t.Errorf("sessions = %v", opts.sessions)
	}
}

func TestParseArgsValidation(t *testing.T) {
	if _, err := parse(t, "--interval", "-1"); err == nil || isUsage(err) ||
		!strings.Contains(err.Error(), "--interval wants a positive number of seconds") {
		t.Errorf("negative interval: %v", err)
	}
	if _, err := parse(t, "--interval", "0"); err == nil {
		t.Error("zero interval accepted")
	}
	if _, err := parse(t, "--list", "--follow"); err == nil ||
		err.Error() != "--follow cannot be combined with --list" {
		t.Errorf("--list --follow: %v", err)
	}
	if _, err := parse(t, "--until", "12:00", "--follow"); err == nil ||
		err.Error() != "--follow cannot be combined with --until" {
		t.Errorf("--until --follow: %v", err)
	}
	// -f is --follow: the two flags with a shorthand are -f and -o.
	if opts, err := parse(t, "-f"); err != nil || !opts.follow {
		t.Errorf("-f: %v %+v", err, opts)
	}
	if _, err := parse(t, "--since", "nonsense"); err == nil ||
		!strings.HasPrefix(err.Error(), "cannot parse time \"nonsense\"") {
		t.Errorf("bad since: %v", err)
	}
}

func TestParseArgsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := parseArgs([]string{"--help"}, &stdout, &stderr)
	if !errors.Is(err, pflag.ErrHelp) {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"Usage: oc-transcript", "--everywhere", "2h, 30m, 3d, today, yesterday",
		"-f, --follow", "-o, --out"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("usage text lacks %q", want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("--help wrote to stderr: %q", stderr.String())
	}
	// The help is hand-grouped, so this is what keeps it honest: every flag
	// parseArgs defines must be named in it.
	for _, name := range []string{"root", "everywhere", "db", "since", "until", "all",
		"session", "tools", "reasoning", "stats",
		"max-chars", "arg-width", "color", "list", "follow", "interval", "out", "version"} {
		if !strings.Contains(stdout.String(), "--"+name) {
			t.Errorf("usage text lacks --%s", name)
		}
	}
}

// Usage triggered by a bad flag belongs on stderr; stdout stays clean.
func TestUsageOnErrorGoesToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := parseArgs([]string{"--nope"}, &stdout, &stderr)
	if err == nil || !isUsage(err) {
		t.Fatalf("--nope: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("bad flag wrote to stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Usage: oc-transcript") {
		t.Errorf("bad flag did not print usage to stderr: %q", stderr.String())
	}
}

func TestParseArgsVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := parseArgs([]string{"--version"}, &stdout, &stderr)
	if !errors.Is(err, errVersionPrinted) {
		t.Fatalf("--version: %v", err)
	}
	if got := stdout.String(); !strings.HasPrefix(got, "oc-transcript ") ||
		strings.TrimSpace(got) == "oc-transcript" {
		t.Errorf("--version printed %q, want the name and a version", got)
	}
	// It answers whatever else is on the line, as --help does.
	if _, err := parse(t, "--version", "stray"); !errors.Is(err, errVersionPrinted) {
		t.Errorf("--version with a positional: %v", err)
	}
}

func TestRunErrors(t *testing.T) {
	t.Setenv("COLUMNS", "")
	var stdout, stderr bytes.Buffer

	missing := filepath.Join(t.TempDir(), "no.db")
	err := run([]string{"--db", missing}, &stdout, &stderr)
	if err == nil || isUsage(err) ||
		err.Error() != fmt.Sprintf("no opencode database at %s (try `opencode db path`)", missing) {
		t.Errorf("missing db: %v", err)
	}

	d := newTestDB(t)
	err = run([]string{"--db", d.path, "--everywhere", "-o", "/proc/nope/x.md"}, &stdout, &stderr)
	if err == nil || !strings.HasPrefix(err.Error(), "cannot write /proc/nope/x.md:") {
		t.Errorf("unwritable out: %v", err)
	}
}

func TestRunEmptyMessages(t *testing.T) {
	t.Setenv("COLUMNS", "")
	d := newTestDB(t)
	// The reason a transcript is empty is an answer about the query, not
	// transcript content: it goes to stderr, and stdout stays clean.
	out := func(args ...string) string {
		var stdout, stderr bytes.Buffer
		if err := run(append([]string{"--db", d.path}, args...), &stdout, &stderr); err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		if stdout.Len() != 0 {
			t.Errorf("run(%v) wrote to stdout: %q", args, stdout.String())
		}
		return stderr.String()
	}

	if got := out("--everywhere"); !strings.Contains(got, "this database has no sessions at all") {
		t.Errorf("no sessions: %q", got)
	}

	d.session("ses_a", "", "t", "/somewhere/else", 1)
	d.userText("msg_1", "ses_a", 1, "hi")
	if got := out("--root", "/not/there"); !strings.Contains(got,
		"no opencode session has ever run under /not/there — try --root, or --everywhere") {
		t.Errorf("wrong root: %q", got)
	}
	if got := out("--everywhere", "--all", "--session", "nope"); !strings.Contains(got,
		"1 session(s) in scope, none matching --session") {
		t.Errorf("filtered out: %q", got)
	}
	if got := out("--everywhere", "--since", "2100-01-01"); !strings.Contains(got,
		"nothing from 1 session(s) in this window — try --all") {
		t.Errorf("empty window: %q", got)
	}
	if got := out("--everywhere", "--since", "2100-01-01", "--list"); !strings.Contains(got,
		"nothing from 1 session(s) in this window") {
		t.Errorf("empty list: %q", got)
	}
	// With --all the hint would be useless, so it goes — here everything in
	// scope is a zero-part assistant turn, which renders nothing.
	d2 := newTestDB(t)
	d2.session("ses_b", "", "t", "/p", 1)
	d2.message("msg_b", "ses_b", 1, `{"role":"assistant"}`)
	var so, se bytes.Buffer
	if err := run([]string{"--db", d2.path, "--everywhere", "--all"}, &so, &se); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(se.String(), "nothing from 1 session(s) in this window\n") ||
		strings.Contains(se.String(), "try --all") {
		t.Errorf("--all drops the hint: %q", se.String())
	}
}

func TestRunWritesFile(t *testing.T) {
	t.Setenv("COLUMNS", "")
	d := newTestDB(t)
	d.session("ses_a", "", "t", "/p", 1)
	d.userText("msg_1", "ses_a", 1, "hello file")

	path := filepath.Join(t.TempDir(), "out.md")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--db", d.path, "--everywhere", "--all", "-o", path}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout not empty with --out: %q", stdout.String())
	}
	if got := stderr.String(); got != "wrote "+path+"\n" {
		t.Errorf("stderr = %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello file") || strings.Contains(string(data), "\033[") {
		t.Errorf("file content: %q", data)
	}
}
