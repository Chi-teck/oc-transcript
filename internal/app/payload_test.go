package app

import (
	"encoding/json"
	"testing"
)

// errorText draws a turn or tool error. The {type, message} shape reads as
// one phrase; anything else is shown as the JSON it is rather than dropped,
// and an error the store did not write is no text at all.
func TestErrorText(t *testing.T) {
	for _, c := range []struct {
		name string
		raw  string
		want string
	}{
		{"absent", "", ""},
		{"null", "null", ""},
		{"type and message", `{"type":"aborted","message":"Aborted"}`, "aborted: Aborted"},
		{"extra keys are ignored", `{"message":"boom","type":"tool.execution","code":1}`, "tool.execution: boom"},
		{"no message", `{"type":"aborted"}`, `{"type":"aborted"}`},
		{"another shape", `{"name":"MessageAbortedError","data":{"message":"Aborted"}}`,
			`{"name":"MessageAbortedError","data":{"message":"Aborted"}}`},
		{"a bare string", `"boom"`, `"boom"`},
	} {
		var raw json.RawMessage
		if c.raw != "" {
			raw = json.RawMessage(c.raw)
		}
		if got := errorText(raw); got != c.want {
			t.Errorf("%s: errorText(%s) = %q, want %q", c.name, c.raw, got, c.want)
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
