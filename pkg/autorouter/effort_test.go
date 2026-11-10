package autorouter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func support(channels ...ChannelReasoning) EffortSupport {
	var s EffortSupport
	for _, channel := range channels {
		s.Add(channel)
	}
	return s
}

func TestEffortSupportClamp(t *testing.T) {
	declared := func(efforts ...string) ChannelReasoning { return ChannelReasoning{Declared: true, Efforts: efforts} }
	tests := []struct {
		name       string
		support    EffortSupport
		desired    string
		wantEffort string
		wantAction EffortAction
	}{
		{"no reasoning declaration keeps the client's fields", support(), "high", "", EffortKeep},
		{"channels without the dimension do not count", support(ChannelReasoning{}), "high", "", EffortKeep},
		{"unsupported strips the client's fields", support(ChannelReasoning{Declared: true, Unsupported: true}), "high", "", EffortStrip},
		{"a declared effort is written as is", support(declared("low", "medium", "high")), "medium", "medium", EffortSet},
		{"above the ceiling falls to the nearest", support(declared("none", "low", "medium")), "xhigh", "medium", EffortSet},
		{"a tie prefers the stronger effort", support(declared("low", "high")), "medium", "high", EffortSet},
		{"below the floor rises to it", support(declared("high", "xhigh")), "low", "high", EffortSet},
		{"unrestricted accepts any ladder effort", support(ChannelReasoning{Declared: true}), "max", "max", EffortSet},
		{"mandatory never lands on none", support(ChannelReasoning{Declared: true, Mandatory: true}), "none", "minimal", EffortSet},
		{"efforts are the union over channels", support(declared("low"), declared("high")), "xhigh", "high", EffortSet},
		{"a supporting channel outweighs an unsupported one", support(ChannelReasoning{Declared: true, Unsupported: true}, declared("medium")), "high", "medium", EffortSet},
		{"an unknown desired effort is treated as medium", support(declared("low", "high")), "", "high", EffortSet},
		{"only off-ladder efforts declared keeps the client's fields", support(declared("turbo")), "high", "", EffortKeep},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			effort, action := tt.support.Clamp(tt.desired)
			assert.Equal(t, tt.wantEffort, effort)
			assert.Equal(t, tt.wantAction, action)
		})
	}
}

func TestPatchBody(t *testing.T) {
	tests := []struct {
		name     string
		protocol Protocol
		body     string
		model    string
		action   EffortAction
		effort   string
		want     string
	}{
		{"model only", ProtocolChat, `{"model":"auto","a":1}`, "m", EffortKeep, "", `{"model":"m","a":1}`},
		{"keep leaves the client's reasoning alone", ProtocolChat,
			`{"model":"auto","reasoning_effort":"low"}`, "m", EffortKeep, "", `{"model":"m","reasoning_effort":"low"}`},
		{"chat writes reasoning_effort and clears the nested fields", ProtocolChat,
			`{"model":"auto","reasoning":{"effort":"low","max_tokens":2048,"exclude":true}}`, "m", EffortSet, "high",
			`{"model":"m","reasoning":{"exclude":true},"reasoning_effort":"high"}`},
		{"chat drops a reasoning object that became empty", ProtocolChat,
			`{"model":"auto","reasoning":{"enabled":false}}`, "m", EffortSet, "high", `{"model":"m","reasoning_effort":"high"}`},
		{"chat leaves a malformed reasoning value for validation", ProtocolChat,
			`{"model":"auto","reasoning":"x"}`, "m", EffortSet, "high", `{"model":"m","reasoning":"x","reasoning_effort":"high"}`},
		{"chat strip removes both fields", ProtocolChat,
			`{"model":"auto","reasoning_effort":"low","reasoning":{"effort":"low"}}`, "m", EffortStrip, "", `{"model":"m"}`},
		{"responses writes reasoning.effort and keeps the summary", ProtocolResponses,
			`{"model":"auto","reasoning":{"summary":"auto"}}`, "m", EffortSet, "high", `{"model":"m","reasoning":{"summary":"auto","effort":"high"}}`},
		{"responses creates the reasoning object", ProtocolResponses,
			`{"model":"auto"}`, "m", EffortSet, "low", `{"model":"m","reasoning":{"effort":"low"}}`},
		{"responses strip removes reasoning", ProtocolResponses,
			`{"model":"auto","reasoning":{"effort":"low"}}`, "m", EffortStrip, "", `{"model":"m"}`},
		{"anthropic writes output_config.effort and drops thinking", ProtocolAnthropic,
			`{"model":"auto","thinking":{"type":"enabled","budget_tokens":4000}}`, "m", EffortSet, "high",
			`{"model":"m","output_config":{"effort":"high"}}`},
		{"anthropic keeps the other output_config members", ProtocolAnthropic,
			`{"model":"auto","output_config":{"format":{"type":"json_schema"},"effort":"low"}}`, "m", EffortSet, "xhigh",
			`{"model":"m","output_config":{"format":{"type":"json_schema"},"effort":"xhigh"}}`},
		{"anthropic none disables thinking", ProtocolAnthropic,
			`{"model":"auto","thinking":{"type":"enabled","budget_tokens":4000},"output_config":{"effort":"high"}}`, "m", EffortSet, "none",
			`{"model":"m","thinking":{"type":"disabled"}}`},
		{"anthropic strip removes thinking and effort", ProtocolAnthropic,
			`{"model":"auto","thinking":{"type":"enabled"},"output_config":{"effort":"low","format":1}}`, "m", EffortStrip, "",
			`{"model":"m","output_config":{"format":1}}`},
		{"anthropic with a malformed output_config is left alone", ProtocolAnthropic,
			`{"model":"auto","thinking":{"type":"enabled"},"output_config":"x"}`, "m", EffortSet, "high",
			`{"model":"m","thinking":{"type":"enabled"},"output_config":"x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patched, err := PatchBody([]byte(tt.body), tt.protocol, tt.model, tt.action, tt.effort)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(patched))
			assert.True(t, gjson.ValidBytes(patched))
		})
	}
}
