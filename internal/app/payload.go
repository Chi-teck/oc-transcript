package app

import "encoding/json"

// Typed views of the JSON the store keeps in message.data and part.data. Only
// what the renderer reads is declared; everything else passes through. Fields
// the store is loose about are flexString or json.RawMessage so one odd value
// does not take a whole row down.
//
// This is the shape of what the store hands over, not a rendering decision, so
// it sits beside the store rather than inside the renderer: how a patch part is
// drawn changes often and what a patch part contains does not.

type messageData struct {
	Role    flexString      `json:"role"`
	ModelID flexString      `json:"modelID"`
	Variant flexString      `json:"variant"`
	Agent   flexString      `json:"agent"`
	Error   json.RawMessage `json:"error"`
}

type partData struct {
	Type      flexString      `json:"type"`
	Text      flexString      `json:"text"`
	Synthetic json.RawMessage `json:"synthetic"`
	Tool      flexString      `json:"tool"`
	State     *toolState      `json:"state"`
	Filename  json.RawMessage `json:"filename"`
	Mime      json.RawMessage `json:"mime"`
	Files     json.RawMessage `json:"files"`
	Hash      flexString      `json:"hash"`
	Tokens    *stepTokens     `json:"tokens"`
	Cost      json.RawMessage `json:"cost"`
	Reason    flexString      `json:"reason"`
}

type toolState struct {
	Status   flexString      `json:"status"`
	Input    json.RawMessage `json:"input"` // no schema; kept raw so the in: block keeps the source key order
	Output   flexString      `json:"output"`
	Metadata json.RawMessage `json:"metadata"`
	Title    flexString      `json:"title"`
	Time     *toolTime       `json:"time"`
	Error    flexString      `json:"error"`
}

type toolTime struct {
	Start json.RawMessage `json:"start"`
	End   json.RawMessage `json:"end"`
}

type toolMetadata struct {
	Truncated  json.RawMessage `json:"truncated"`
	OutputPath flexString      `json:"outputPath"`
}

type stepTokens struct {
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output"`
	Cache  struct {
		Read  json.RawMessage `json:"read"`
		Write json.RawMessage `json:"write"`
	} `json:"cache"`
}

// stringOr reads a string field: absent or null gives the default, a string
// gives itself, anything else its compact JSON.
func stringOr(raw json.RawMessage, dflt string) string {
	if raw == nil || string(raw) == "null" {
		return dflt
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return reformatJSON(raw, -1)
}

// numberText is what an f-string prints for a JSON number: the literal for an
// int, the same for a float (whose repr is its shortest round-trip form, which
// is how the store spelled it). Anything that is not a number prints as dflt.
func numberText(raw json.RawMessage, dflt string) string {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil || n == "" {
		return dflt
	}
	return string(n)
}

func numberValue(raw json.RawMessage) (float64, bool) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil || n == "" {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}
