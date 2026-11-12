package observability

import "github.com/QuantumNous/new-api/constant"

// streamOutput is what an aggregator made of a finished stream.
type streamOutput struct {
	// value is the rebuilt answer. Empty means the stream produced nothing
	// worth putting on the trace.
	value []byte
	// selfCapped marks a value the aggregator already kept inside the capture
	// budget. Clipping such a value again would cut its JSON envelope in half,
	// so the caller writes it as-is.
	selfCapped bool
	// truncated marks a value that lost content to that budget.
	truncated bool
}

// streamAggregator rebuilds the final answer of one upstream protocol from the
// SSE frames of a stream. Each protocol gets its own implementation in its own
// file, so adding a provider means adding a file and one registry entry rather
// than growing the trace runtime.
type streamAggregator interface {
	// accept consumes one frame and reports whether it belongs to this
	// protocol. Only the first frame's answer decides anything: it picks the
	// aggregator that owns the rest of the stream.
	accept(data string) bool
	// output returns what the stream amounted to, once it has ended.
	output() streamOutput
}

// apiTypeUnknown marks a stream whose upstream API type was never recorded, so
// every aggregator stays a first-choice candidate.
const apiTypeUnknown = -1

// streamAggregatorEntry pairs a protocol aggregator with the upstream API types
// known to speak it. The API type only decides the order in which aggregators
// are offered the first frame: an upstream whose API type is not listed is
// still offered every aggregator afterwards, because custom and aggregating
// channels can proxy a protocol their API type does not advertise.
type streamAggregatorEntry struct {
	apiTypes      []int
	newAggregator func(maxBytes int64) streamAggregator
}

// streamAggregators is ordered from the most specific protocol to the loosest,
// because the first aggregator that accepts a frame owns the stream. The OpenAI
// chat shape matches the widest range of frames and therefore comes last, and
// speaks for every upstream.
var streamAggregators = []streamAggregatorEntry{
	{
		apiTypes:      []int{constant.APITypeOpenAI, constant.APITypeCodex},
		newAggregator: newResponsesStreamAggregator,
	},
	{
		apiTypes:      []int{constant.APITypeAnthropic, constant.APITypeAws, constant.APITypeVertexAi},
		newAggregator: newClaudeStreamAggregator,
	},
	{
		apiTypes:      []int{constant.APITypeGemini, constant.APITypeVertexAi},
		newAggregator: newGeminiStreamAggregator,
	},
	{
		newAggregator: newOpenAIChatStreamAggregator,
	},
}

// preferredFor reports whether this aggregator is a first-choice candidate for
// an upstream API type.
func (e streamAggregatorEntry) preferredFor(apiType int) bool {
	if apiType == apiTypeUnknown || len(e.apiTypes) == 0 {
		return true
	}
	for _, candidate := range e.apiTypes {
		if candidate == apiType {
			return true
		}
	}
	return false
}
