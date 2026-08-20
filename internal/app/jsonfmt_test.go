package app

import (
	"encoding/json"
	"testing"
)

func TestReformatJSONCompact(t *testing.T) {
	cases := map[string]string{
		`{"b":1,"a":[1,2,{"x":null}],"c":"s"}`: `{"b":1,"a":[1,2,{"x":null}],"c":"s"}`,
		`[true,false,null]`:                    `[true,false,null]`,
		`{}`:                                   `{}`,
		`[]`:                                   `[]`,
		`"plain"`:                              `"plain"`,
		`3.14`:                                 `3.14`,
		`1e3`:                                  `1e3`, // number spelling comes from the source
		`{"k":"héllo <&> 日本"}`:                 `{"k":"héllo <&> 日本"}`,
		`{"k":"ab\nc"}`:                        `{"k":"ab\nc"}`,
		`{"z":1,"a":2}`:                        `{"z":1,"a":2}`, // source key order, never sorted
	}
	for in, want := range cases {
		if got := reformatJSON([]byte(in), -1); got != want {
			t.Errorf("reformatJSON(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestReformatJSONIndent(t *testing.T) {
	in := `{"b":{"y":1,"x":[1,2]},"a":[],"c":{}}`
	want := "{\n  \"b\": {\n    \"y\": 1,\n    \"x\": [\n      1,\n      2\n    ]\n  },\n  \"a\": [],\n  \"c\": {}\n}"
	if got := reformatJSON([]byte(in), 2); got != want {
		t.Errorf("reformatJSON indent:\n%s\nwant:\n%s", got, want)
	}
}

func TestReformatJSONMalformed(t *testing.T) {
	for _, in := range []string{`{"a":`, ``, `nonsense`, `{"a":1} extra`} {
		if got := reformatJSON([]byte(" "+in+" "), -1); got != in {
			t.Errorf("reformatJSON(%q) = %q, want the input back", in, got)
		}
	}
}

func TestFlexString(t *testing.T) {
	cases := map[string]string{
		`"text"`:  "text",
		`null`:    "",
		`42`:      "42",
		`{"a":1}`: `{"a":1}`,
	}
	for in, want := range cases {
		var f flexString
		if err := json.Unmarshal([]byte(in), &f); err != nil {
			t.Fatalf("flexString(%s): %v", in, err)
		}
		if string(f) != want {
			t.Errorf("flexString(%s) = %q, want %q", in, f, want)
		}
	}
}

func TestTruthy(t *testing.T) {
	falsy := []string{``, `null`, `false`, `0`, `0.0`, `""`, `{}`, `[]`}
	for _, in := range falsy {
		if truthy(json.RawMessage(in)) {
			t.Errorf("truthy(%q) = true, want false", in)
		}
	}
	truey := []string{`true`, `1`, `-0.5`, `"x"`, `{"a":1}`, `[0]`}
	for _, in := range truey {
		if !truthy(json.RawMessage(in)) {
			t.Errorf("truthy(%q) = false, want true", in)
		}
	}
}
