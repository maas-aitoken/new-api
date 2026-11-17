package autorouter

import (
	"slices"

	"github.com/tidwall/gjson"
)

// contentFields lists, per inbound protocol, the fields that carry the
// conversation itself. Every other top-level field of a request body is a
// parameter: sampling knobs, reasoning effort and whatever a client merged in
// from its SDK's extra_body.
var contentFields = map[Protocol][]string{
	ProtocolChat:      {"messages", "tools"},
	ProtocolAnthropic: {"system", "messages", "tools"},
	ProtocolResponses: {"instructions", "input", "tools"},
}

// RequestParams returns the request body without its conversation content, as a
// JSON object of at most maxBytes bytes. Values are copied as written, so large
// integers keep their digits. A field that would push the object past maxBytes
// is left out whole, which keeps the result valid JSON. It is empty when the
// protocol is unknown, the body is not a JSON object, or no parameter fits.
func RequestParams(body []byte, protocol Protocol, maxBytes int) string {
	content, known := contentFields[protocol]
	root := gjson.ParseBytes(body)
	if !known || !root.IsObject() {
		return ""
	}
	params := []byte{'{'}
	root.ForEach(func(key, value gjson.Result) bool {
		if slices.Contains(content, key.String()) {
			return true
		}
		size := len(params) + len(key.Raw) + 1 + len(value.Raw) + 1 // field, ':' and the closing brace
		if len(params) > 1 {
			size++ // separator
		}
		if size > maxBytes {
			return true
		}
		if len(params) > 1 {
			params = append(params, ',')
		}
		params = append(params, key.Raw...)
		params = append(params, ':')
		params = append(params, value.Raw...)
		return true
	})
	if len(params) == 1 {
		return ""
	}
	return string(append(params, '}'))
}
