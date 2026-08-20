# oc-transcript

[![CI](https://github.com/Chi-teck/oc-transcript/actions/workflows/ci.yml/badge.svg)](https://github.com/Chi-teck/oc-transcript/actions/workflows/ci.yml)

Merge every [opencode](https://opencode.ai) session belonging to a project into one
chronological transcript, laid out for the terminal.

An agent driven from more than one place at once — a chat bridge, a scheduled run,
someone at the TUI — gets a separate opencode session for each. `oc-transcript` walks
opencode's SQLite store and interleaves them into a single stream, each run of blocks
opening under a banner naming the session it belongs to. A subagent's tag ends in `↓` and
its banner rule is dashed rather than solid.

## Install

Grab an archive for your platform from the
[releases page](https://github.com/Chi-teck/oc-transcript/releases) or build from source:

```bash
CGO_ENABLED=0 go build -o oc-transcript .   # or: task build
```

## Usage

```bash
oc-transcript                      # last 24h of sessions run under the cwd
oc-transcript --all --tools full   # everything, with full tool output
oc-transcript --list               # just the sessions, no messages
oc-transcript --follow             # keep printing new messages as they arrive
```

Scope is the working directory, the way git scopes itself: the sessions whose
working directory sits inside `--root` are the ones you get. `--everywhere` ignores
the root and takes the whole database.

```
 ⚑ ❬a1b2c3❭ Flaky follow test
────────────────────────────────────────────────────────────────────────

  │ 2026-08-17 10:01:00 • user
  │
  │ ci is red on main: TestFollowCursor times out. find the cause and
  │ fix it


  │ 2026-08-17 10:02:00 • assistant • claude-opus-5
  │
  │ Reproducing before I change anything.
  │
  │ bash go test ./internal/app -run TestFollowCursor        ok    10.4s
  │ read internal/app/follow.go                              ok     0.1s
  │ edit internal/app/follow.go                             ERR     0.1s
  │   err oldString not found in file
  │ edit internal/app/follow.go                              ok     0.2s
  │ bash go test ./internal/app -run TestFollowCursor        ok     2.6s
  │
  │ Fixed — the cursor now only advances past printed rows.
```

The flag before a tag says where the rest of that session is:

| Flag | Meaning                                                                                                                            |
|------|------------------------------------------------------------------------------------------------------------------------------------|
| `⚑`  | the session starts here — this is its first block                                                                                  |
| `⚐`  | it was already running when the window opened, so there is more above; widen `--since`, or read it whole with `--session ID --all` |
| `↻`  | the stream has shown it before and come back to it — the earlier part is further up this same output                               |

Times are local — whatever `$TZ` resolves to, and the same zone `--since`/`--until` are
read in. The zone is not printed, so use `TZ=UTC oc-transcript …` if a transcript
written with `--out` will be read elsewhere.

## Options

Run `oc-transcript --help` for the full list. The ones you reach for:

| Flag                           | What it does                                                                   |
|--------------------------------|--------------------------------------------------------------------------------|
| `--root`, `--everywhere`       | which sessions are in scope                                                    |
| `--since`, `--until`, `--all`  | the time window — `2h`, `30m`, `3d`, `today`, `yesterday`, or an ISO timestamp |
| `--tools {compact,full,none}`  | tool call detail                                                               |
| `--reasoning`, `--stats`       | include reasoning blocks / per-step token and cost lines                       |
| `--session`                    | narrow to sessions whose id contains a fragment                                |
| `-f`, `--follow`, `--interval` | tail new messages (cursor-driven, poll every N seconds)                        |
| `-o`, `--out`                  | write to a file instead of stdout (colour off unless `--color always`; width from `COLUMNS`, else 100) |
| `--db`                         | the store, default `$XDG_DATA_HOME/opencode/opencode.db`                       |
| `--version`                    | print the version and exit                                                     |

## Development

```bash
task check         # gofmt, go vet, golangci-lint, go test — the pre-commit gate
task test:update   # regenerate the golden files, then re-run the tests
task build         # CGO_ENABLED=0 go build -o oc-transcript .
```

Expected output is fixed by the golden files in `internal/app/testdata/`. CI runs the
same checks on every push and pull request; pushing a `v*` tag publishes a release.

## License

MIT — see [LICENSE](LICENSE).
