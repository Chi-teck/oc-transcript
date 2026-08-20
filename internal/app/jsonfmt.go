package app

import (
	"bytes"
	"encoding/json"
	"strings"
)

// reformatJSON re-formats a JSON document: compact when indent < 0, otherwise
// pretty-printed with that indent. json.Indent and json.Compact rewrite the raw
// bytes rather than decoding and re-encoding, so the source's key order,
// escaping and number spelling all survive. Malformed input comes back as-is,
// so the caller always has something to print.
func reformatJSON(raw []byte, indent int) string {
	var out bytes.Buffer
	var err error
	if indent < 0 {
		err = json.Compact(&out, raw)
	} else {
		err = json.Indent(&out, raw, "", strings.Repeat(" ", indent))
	}
	if err != nil {
		return string(bytes.TrimSpace(raw))
	}
	return out.String()
}

// flexString decodes a field the store is loose about: a JSON string comes
// through as itself, null as "", and anything else as its compact encoding
// rather than failing the whole row.
type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	if s, ok := unquoteFast(data); ok {
		*f = flexString(s)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*f = ""
		return nil
	}
	*f = flexString(reformatJSON(data, -1))
	return nil
}

// unquoteFast unwraps a JSON string with no escapes without re-validating it —
// the outer decoder already has. The bytes arrive straight from a decode, so
// a leading quote plus no backslash means the payload is the literal text.
func unquoteFast(data []byte) (string, bool) {
	data = bytes.TrimSpace(data)
	if len(data) >= 2 && data[0] == '"' && bytes.IndexByte(data, '\\') < 0 {
		return string(data[1 : len(data)-1]), true
	}
	return "", false
}

// truthy reports whether a raw JSON value counts as set: absent, null, false,
// 0, "" and empty containers are false.
func truthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case '{', '[':
		dec := json.NewDecoder(bytes.NewReader(raw))
		if _, err := dec.Token(); err != nil {
			return true
		}
		return dec.More()
	case '"':
		return len(raw) > 2
	case 'n', 'f':
		return false
	case 't':
		return true
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		if f, err := n.Float64(); err == nil {
			return f != 0
		}
	}
	return true
}
