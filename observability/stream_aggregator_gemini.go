package observability

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
)

// geminiStreamAggregator rebuilds the answer of a Gemini generateContent
// stream by replaying every frame through the converter the relay path uses to
// serve a Gemini upstream in OpenAI format. Each frame carries only the parts
// generated since the previous one, so the answer is the sum of the frames.
type geminiStreamAggregator struct {
	text      strings.Builder
	reasoning strings.Builder
	toolCalls []dto.ToolCallResponse
}

func newGeminiStreamAggregator(int64) streamAggregator { return &geminiStreamAggregator{} }

func (a *geminiStreamAggregator) accept(data string) bool {
	var frame dto.GeminiChatResponse
	if err := common.UnmarshalJsonStr(data, &frame); err != nil {
		return false
	}
	// A frame of another protocol unmarshals into an empty response; only the
	// Gemini envelope fields tell the two apart.
	if len(frame.Candidates) == 0 && !frame.HasUsageMetadata && frame.PromptFeedback == nil {
		return false
	}
	chunk, _ := relayconvert.StreamResponseGeminiChat2OpenAI(&frame)
	for i := range chunk.Choices {
		delta := &chunk.Choices[i].Delta
		a.text.WriteString(delta.GetContentString())
		a.reasoning.WriteString(delta.GetReasoningContent())
		for _, call := range delta.ToolCalls {
			// The stream index is a position inside one frame and means
			// nothing on a finished message.
			call.Index = nil
			a.toolCalls = append(a.toolCalls, call)
		}
	}
	return true
}

func (a *geminiStreamAggregator) output() streamOutput {
	if a.text.Len() == 0 && a.reasoning.Len() == 0 && len(a.toolCalls) == 0 {
		return streamOutput{}
	}
	value, err := common.Marshal([]assistantStreamMessage{{
		Role:             "assistant",
		Content:          a.text.String(),
		ReasoningContent: a.reasoning.String(),
		ToolCalls:        a.toolCalls,
	}})
	if err != nil {
		return streamOutput{}
	}
	return streamOutput{value: value}
}
