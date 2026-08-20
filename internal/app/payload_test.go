package app

import (
	"encoding/json"
	"testing"
)

// stringOr is what puts a name and a type on an attachment line, so a field the
// store left out and a field the store wrote as null have to come back the
// same: both mean the store does not say, and both should read as the caller's
// placeholder rather than as a hole where a name belongs.
//
// The null case used to fall through to a branch that could never run —
// encoding/json unmarshals null into a string as a no-op and reports no error,
// so the check for it sat below a return that had already been taken — and an
// explicit null came back empty.
func TestStringOr(t *testing.T) {
	for _, c := range []struct {
		name string
		raw  string
		want string
	}{
		{"absent", "", "?"},
		{"null", "null", "?"},
		{"a string", `"shot.png"`, "shot.png"},
		{"an empty string is a value, not an absence", `""`, ""},
		{"not a string at all", `{"a":1}`, `{"a":1}`},
		{"a number", "12", "12"},
	} {
		var raw json.RawMessage
		if c.raw != "" {
			raw = json.RawMessage(c.raw)
		}
		if got := stringOr(raw, "?"); got != c.want {
			t.Errorf("%s: stringOr(%s) = %q, want %q", c.name, c.raw, got, c.want)
		}
	}
}

func TestNumberText(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"", "0"},
		{"null", "0"},
		{"12", "12"},
		{"1.5", "1.5"},
		{`"nope"`, "0"},
	} {
		var raw json.RawMessage
		if c.raw != "" {
			raw = json.RawMessage(c.raw)
		}
		if got := numberText(raw, "0"); got != c.want {
			t.Errorf("numberText(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}
