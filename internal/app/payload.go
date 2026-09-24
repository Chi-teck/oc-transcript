package app

import "encoding/json"

// Typed views of the JSON the store keeps in session_message.data. Only what
// the renderer reads is declared; everything else passes through. Fields the
// store is loose about are flexString or json.RawMessage so one odd value does
// not take a whole row down.
//
// This is the shape of what the store hands over, not a rendering decision, so
// it sits beside the store rather than inside the renderer: how a tool item is
// drawn changes often and what a tool item contains does not.

type messageData struct {
	Agent   flexString        `json:"agent"`
	Model   *modelRef         `json:"model"`
	Error   json.RawMessage   `json:"error"`
	Time    *messageTime      `json:"time"`
	Text    flexString        `json:"text"`    // user, synthetic
	Files   []userFile        `json:"files"`   // user
	Content []json.RawMessage `json:"content"` // assistant; decoded per item so one bad item is skipped, not the turn
	Tokens  *stepTokens       `json:"tokens"`
	Cost    json.RawMessage   `json:"cost"`
	Finish  flexString        `json:"finish"`
	Reason  flexString        `json:"reason"` // compaction
	Status  flexString        `json:"status"` // compaction
}

type modelRef struct {
	ID      flexString `json:"id"`
	Variant flexString `json:"variant"`
}

// messageTime is the turn's own clock. Only `completed` is read: its presence
// is what says a turn is over and will not grow any more parts.
type messageTime struct {
	Completed json.RawMessage `json:"completed"`
}

// userFile is an attachment on a user message. The payload sits beside name
// and mime as base64; it is not declared, so the decoder skips it.
type userFile struct {
	Name flexString `json:"name"`
	Mime flexString `json:"mime"`
}

// contentItem is one entry of an assistant message's content: text, reasoning
// or tool.
type contentItem struct {
	Type  flexString `json:"type"`
	Text  flexString `json:"text"`
	Name  flexString `json:"name"`
	ID    flexString `json:"id"`
	State *toolState `json:"state"`
	Time  *toolTime  `json:"time"`
}

type toolState struct {
	Status   flexString      `json:"status"`
	Input    json.RawMessage `json:"input"` // no schema; kept raw so the in: block keeps the source key order
	Metadata json.RawMessage `json:"metadata"`
	Error    json.RawMessage `json:"error"`
	Content  []toolOutput    `json:"content"`
}

// toolOutput is one item of a tool's output: text, or a file whose uri is an
// image of 60–550 KB of base64. The uri is not declared, so it is never decoded.
type toolOutput struct {
	Type flexString `json:"type"`
	Text flexString `json:"text"`
	Mime flexString `json:"mime"`
}

type toolTime struct {
	Created   json.RawMessage `json:"created"`
	Completed json.RawMessage `json:"completed"`
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

// errorText renders a turn or tool error, stored as {type, message}, as
// "<type>: <message>". Any other shape prints as its compact JSON; absent or
// null gives "".
func errorText(raw json.RawMessage) string {
	if raw == nil || string(raw) == "null" {
		return ""
	}
	var e struct {
		Type    *string `json:"type"`
		Message *string `json:"message"`
	}
	if err := json.Unmarshal(raw, &e); err == nil && e.Type != nil && e.Message != nil {
		return *e.Type + ": " + *e.Message
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
