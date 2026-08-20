// Command oc-transcript merges every opencode session belonging to a project
// into one chronological transcript.
//
// An agent driven from more than one place at once — a chat bridge, a scheduled
// run, someone at the TUI — gets a separate opencode session for each, so reading
// back what it actually did means reading several sessions side by side. This
// walks opencode's SQLite store and interleaves them into one chronological
// stream, each session carried down the left edge in its own colour so the
// conversations stay apart where they overlap.
//
// Point --root at any checkout: the sessions whose working directory sits inside
// it are the ones you get.
//
// Message bodies are printed exactly as the agent received them. Whatever a
// bridge prepends to a prompt is body text like any other and is never edited out.
//
// The database is opened read-only; nothing here writes to it.
//
// Output is laid out for the terminal: widths are measured in display cells
// rather than code points, the session table's columns are budgeted against
// the terminal width, long lines wrap back onto their own indent, truncated
// tool output is flagged, --follow is cursor-driven and writes where the
// transcript went, and the flag parser takes no abbreviations.
package main

import "github.com/Chi-teck/oc-transcript/internal/app"

func main() { app.Main() }
