package common

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldForwardCodexServiceTierUsesGlobalPolicyWithoutTags(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalPolicy := settings.GPTRequestFastPolicy
	t.Cleanup(func() {
		settings.GPTRequestFastPolicy = originalPolicy
	})

	info := &RelayInfo{
		ChannelMeta: &ChannelMeta{
			ChannelType: constant.ChannelTypeCodex,
			ChannelTag:  "untagged-channel",
		},
	}

	settings.GPTRequestFastPolicy = model_setting.GPTRequestFastPolicyDisabled
	assert.False(t, ShouldForwardCodexServiceTier(info))

	settings.GPTRequestFastPolicy = model_setting.GPTRequestFastPolicyAllow
	assert.True(t, ShouldForwardCodexServiceTier(info))
}

func TestShouldForwardGPTServiceTierSupportsOpenAIChannelWithoutTags(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalPolicy := settings.GPTRequestFastPolicy
	t.Cleanup(func() {
		settings.GPTRequestFastPolicy = originalPolicy
	})

	info := &RelayInfo{ChannelMeta: &ChannelMeta{
		ChannelType:       constant.ChannelTypeOpenAI,
		ChannelTag:        "direct-openai",
		UpstreamModelName: "gpt-5.1",
	}}
	settings.GPTRequestFastPolicy = model_setting.GPTRequestFastPolicyAllow
	assert.True(t, ShouldForwardGPTServiceTier(info))
}

func TestIsGPTRequestPolicyChannelRequiresGPTModelOnOpenAIChannels(t *testing.T) {
	for _, tt := range []struct {
		name        string
		channelType int
		upstream    string
		origin      string
		want        bool
	}{
		{name: "openai channel with gpt model", channelType: constant.ChannelTypeOpenAI, upstream: "gpt-5.1", want: true},
		{name: "openai channel with o-series model", channelType: constant.ChannelTypeOpenAI, upstream: "o3-mini", want: true},
		{name: "openai channel with namespaced gpt model", channelType: constant.ChannelTypeOpenAI, upstream: "openai/gpt-4o", want: true},
		{name: "openai channel with deepseek model", channelType: constant.ChannelTypeOpenAI, upstream: "deepseek-v3", want: false},
		{name: "openai channel with claude model", channelType: constant.ChannelTypeOpenAI, upstream: "claude-sonnet-5", want: false},
		{name: "openai channel with qwen model", channelType: constant.ChannelTypeOpenAI, upstream: "qwen3-max", want: false},
		{name: "openai channel falls back to origin model", channelType: constant.ChannelTypeOpenAI, origin: "gpt-5.1", want: true},
		{name: "openai channel origin fallback stays non-gpt", channelType: constant.ChannelTypeOpenAI, origin: "glm-4.6", want: false},
		{name: "codex channel serves gpt models only", channelType: constant.ChannelTypeCodex, upstream: "codex-mini-latest", want: true},
		{name: "unrelated channel type", channelType: constant.ChannelTypeAnthropic, upstream: "gpt-5.1", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := &RelayInfo{
				OriginModelName: tt.origin,
				ChannelMeta: &ChannelMeta{
					ChannelType:       tt.channelType,
					UpstreamModelName: tt.upstream,
				},
			}
			assert.Equal(t, tt.want, IsGPTRequestPolicyChannel(info))
		})
	}
}

func TestApplyGPTRequestPolicyJSONLeavesNonGPTModelsUntouched(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalFast := settings.GPTRequestFastPolicy
	originalReasoning := settings.GPTRequestReasoningPolicy
	t.Cleanup(func() {
		settings.GPTRequestFastPolicy = originalFast
		settings.GPTRequestReasoningPolicy = originalReasoning
	})
	settings.GPTRequestFastPolicy = model_setting.GPTRequestFastPolicyDisabled
	settings.GPTRequestReasoningPolicy = model_setting.GPTRequestReasoningPolicyCapXHigh

	info := &RelayInfo{
		ReasoningEffort: "max",
		ChannelMeta: &ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "deepseek-chat",
		},
	}
	body := []byte(`{"service_tier":"fast","reasoning":{"effort":"max"},"reasoning_effort":"max"}`)
	out, err := ApplyGPTRequestPolicyJSON(info, body)
	require.NoError(t, err)
	assert.JSONEq(t, string(body), string(out))

	ApplyGPTReasoningEffortCap(info)
	assert.Equal(t, "max", info.GetReasoningEffort())
}

func TestApplyGPTChatRequestPolicy(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalFast := settings.GPTRequestFastPolicy
	originalReasoning := settings.GPTRequestReasoningPolicy
	t.Cleanup(func() {
		settings.GPTRequestFastPolicy = originalFast
		settings.GPTRequestReasoningPolicy = originalReasoning
	})

	info := &RelayInfo{ChannelMeta: &ChannelMeta{
		ChannelType:       constant.ChannelTypeOpenAI,
		UpstreamModelName: "gpt-5.1",
	}}
	request := &dto.GeneralOpenAIRequest{
		ServiceTier:     json.RawMessage(`"fast"`),
		ReasoningEffort: "xhigh",
	}

	settings.GPTRequestFastPolicy = model_setting.GPTRequestFastPolicyDisabled
	settings.GPTRequestReasoningPolicy = model_setting.GPTRequestReasoningPolicyCapHigh
	ApplyGPTChatRequestPolicy(info, request)
	assert.Nil(t, request.ServiceTier)
	assert.Equal(t, "high", request.ReasoningEffort)
	assert.Equal(t, "high", info.GetReasoningEffort())

	settings.GPTRequestFastPolicy = model_setting.GPTRequestFastPolicyAllow
	settings.GPTRequestReasoningPolicy = model_setting.GPTRequestReasoningPolicyClient
	request.ServiceTier = json.RawMessage(`"fast"`)
	request.ReasoningEffort = "xhigh"
	ApplyGPTChatRequestPolicy(info, request)
	assert.JSONEq(t, `"priority"`, string(request.ServiceTier))
	assert.Equal(t, "xhigh", request.ReasoningEffort)
}

func TestApplyGPTRequestPolicyJSONSupportsResponsesAndChatShapes(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalFast := settings.GPTRequestFastPolicy
	originalReasoning := settings.GPTRequestReasoningPolicy
	t.Cleanup(func() {
		settings.GPTRequestFastPolicy = originalFast
		settings.GPTRequestReasoningPolicy = originalReasoning
	})
	settings.GPTRequestFastPolicy = model_setting.GPTRequestFastPolicyAllow
	settings.GPTRequestReasoningPolicy = model_setting.GPTRequestReasoningPolicyCapHigh

	info := &RelayInfo{ChannelMeta: &ChannelMeta{
		ChannelType:       constant.ChannelTypeOpenAI,
		UpstreamModelName: "gpt-5.1",
	}}
	out, err := ApplyGPTRequestPolicyJSON(info, []byte(`{"service_tier":"fast","reasoning":{"effort":"xhigh"},"reasoning_effort":"max"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"service_tier":"priority","reasoning":{"effort":"high"},"reasoning_effort":"high"}`, string(out))

	settings.GPTRequestReasoningPolicy = model_setting.GPTRequestReasoningPolicyCapXHigh
	out, err = ApplyGPTRequestPolicyJSON(info, []byte(`{"reasoning":{"effort":"max"},"reasoning_effort":"max"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"reasoning":{"effort":"xhigh"},"reasoning_effort":"xhigh"}`, string(out))
}

func TestApplyGPTRequestPolicyJSONCapsClaudeOriginatedChatRequests(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalReasoning := settings.GPTRequestReasoningPolicy
	t.Cleanup(func() {
		settings.GPTRequestReasoningPolicy = originalReasoning
	})
	settings.GPTRequestReasoningPolicy = model_setting.GPTRequestReasoningPolicyCapXHigh

	// A /v1/messages request on a GPT channel is converted to the chat
	// completions shape before the policy runs, so output_config.effort has
	// already become reasoning_effort.
	info := &RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		ReasoningEffort: "max",
		ChannelMeta: &ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gpt-5.1",
		},
	}
	out, err := ApplyGPTRequestPolicyJSON(info, []byte(`{"model":"gpt-5.1","reasoning_effort":"max"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"gpt-5.1","reasoning_effort":"xhigh"}`, string(out))

	ApplyGPTReasoningEffortCap(info)
	assert.Equal(t, "xhigh", info.GetReasoningEffort())
}

func TestApplyCodexReasoningPolicyCapsOnlyAboveHigh(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalPolicy := settings.GPTRequestReasoningPolicy
	t.Cleanup(func() {
		settings.GPTRequestReasoningPolicy = originalPolicy
	})
	settings.GPTRequestReasoningPolicy = model_setting.GPTRequestReasoningPolicyCapHigh

	info := &RelayInfo{ChannelMeta: &ChannelMeta{ChannelType: constant.ChannelTypeCodex, ChannelTag: "untagged-channel"}}
	for _, tt := range []struct {
		input string
		want  string
	}{
		{input: "max", want: "high"},
		{input: "xhigh", want: "high"},
		{input: "high", want: "high"},
		{input: "medium", want: "medium"},
	} {
		reasoning := &dto.Reasoning{Effort: tt.input}
		ApplyCodexReasoningPolicy(info, reasoning)
		assert.Equal(t, tt.want, reasoning.Effort)
	}
}

func TestApplyCodexReasoningPolicyCapsAtXHigh(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalPolicy := settings.GPTRequestReasoningPolicy
	t.Cleanup(func() {
		settings.GPTRequestReasoningPolicy = originalPolicy
	})
	settings.GPTRequestReasoningPolicy = model_setting.GPTRequestReasoningPolicyCapXHigh

	info := &RelayInfo{ChannelMeta: &ChannelMeta{ChannelType: constant.ChannelTypeCodex}}
	for _, tt := range []struct {
		input string
		want  string
	}{
		{input: "max", want: "xhigh"},
		{input: "xhigh", want: "xhigh"},
		{input: "high", want: "high"},
	} {
		reasoning := &dto.Reasoning{Effort: tt.input}
		ApplyCodexReasoningPolicy(info, reasoning)
		assert.Equal(t, tt.want, reasoning.Effort)
	}
}

func TestRelayInfoGetFinalRequestRelayFormatPrefersExplicitFinal(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		RequestConversionChain:  []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
		FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToConversionChain(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToRelayFormat(t *testing.T) {
	info := &RelayInfo{
		RelayFormat: types.RelayFormatGemini,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatGemini), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatNilReceiver(t *testing.T) {
	var info *RelayInfo
	require.Equal(t, types.RelayFormat(""), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoMetaTypedNilReceiver(t *testing.T) {
	var info *RelayInfo
	var meta convmeta.Meta = info

	assert.Empty(t, meta.GetOriginModelName())
	assert.Empty(t, meta.GetUpstreamModelName())
	assert.False(t, meta.HasChannelMeta())
	assert.Zero(t, meta.GetChannelID())
	assert.Zero(t, meta.GetChannelType())
	assert.False(t, meta.GetIsStream())
	assert.Empty(t, meta.GetReasoningEffort())
	assert.Nil(t, meta.ReasoningState())
	assert.Zero(t, meta.GetEstimatePromptTokens())
	assert.Zero(t, meta.GetSendResponseCount())

	assert.NotPanics(t, func() {
		meta.SetReasoningEffort("high")
		meta.IncrSendResponseCount()
		meta.AppendRequestConversion(types.RelayFormatClaude)
		meta.SetResponsesToolState(&convmeta.ResponsesToolState{})
	})
	assert.Nil(t, meta.ResponsesToolState())

	firstState := meta.EnsureClaudeConvertInfo()
	secondState := meta.EnsureClaudeConvertInfo()
	require.NotNil(t, firstState)
	require.NotNil(t, secondState)
	assert.Equal(t, convmeta.LastMessageTypeNone, firstState.LastMessagesType)
	assert.NotSame(t, firstState, secondState)

	firstOptions := meta.ConvOptions()
	secondOptions := meta.ConvOptions()
	require.NotNil(t, firstOptions)
	require.NotNil(t, secondOptions)
	assert.NotSame(t, firstOptions, secondOptions)
	assert.NotNil(t, firstOptions.Claude.DefaultMaxTokens)
	assert.NotNil(t, firstOptions.Gemini.SupportsImagine)
	assert.NotNil(t, firstOptions.Gemini.SafetySetting)
	assert.NotNil(t, firstOptions.PreserveThinkingSuffix)
	assert.NotNil(t, firstOptions.PreserveEffortTail)
}

func TestRelayInfoResponsesToolStateRoundTrip(t *testing.T) {
	info := &RelayInfo{}
	state := &convmeta.ResponsesToolState{CustomToolNames: map[string]struct{}{"exec": {}}}

	info.SetResponsesToolState(state)
	assert.Same(t, state, info.ResponsesToolState())
	assert.True(t, convmeta.ResponsesToolStateOf(info).IsCustomTool("exec"))

	info.SetResponsesToolState(nil)
	assert.Nil(t, info.ResponsesToolState())
	assert.False(t, convmeta.ResponsesToolStateOf(info).IsCustomTool("exec"))
}

func TestGenRelayInfoCapturesRequestReasoningEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		path        string
		relayFormat types.RelayFormat
		request     dto.Request
		expected    string
	}{
		{
			name:        "OpenAI chat top-level effort",
			path:        "/v1/chat/completions",
			relayFormat: types.RelayFormatOpenAI,
			request:     &dto.GeneralOpenAIRequest{Model: "gpt-5.6-sol", ReasoningEffort: " high "},
			expected:    "high",
		},
		{
			name:        "OpenRouter nested chat effort",
			path:        "/v1/chat/completions",
			relayFormat: types.RelayFormatOpenAI,
			request:     &dto.GeneralOpenAIRequest{Model: "anthropic/claude", Reasoning: json.RawMessage(`{"effort":"xhigh"}`)},
			expected:    "xhigh",
		},
		{
			name:        "OpenAI Responses effort",
			path:        "/v1/responses",
			relayFormat: types.RelayFormatOpenAIResponses,
			request:     &dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", Reasoning: &dto.Reasoning{Effort: "max"}},
			expected:    "max",
		},
		{
			name:        "explicit none is preserved",
			path:        "/v1/responses",
			relayFormat: types.RelayFormatOpenAIResponses,
			request:     &dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", Reasoning: &dto.Reasoning{Effort: "none"}},
			expected:    "none",
		},
		{
			name:        "non-string nested effort is ignored",
			path:        "/v1/chat/completions",
			relayFormat: types.RelayFormatOpenAI,
			request:     &dto.GeneralOpenAIRequest{Model: "anthropic/claude", Reasoning: json.RawMessage(`{"effort":42}`)},
			expected:    "",
		},
		{
			name:        "Claude output config effort",
			path:        "/v1/messages",
			relayFormat: types.RelayFormatClaude,
			request:     &dto.ClaudeRequest{Model: "claude-opus-4-7", OutputConfig: json.RawMessage(`{"effort":"medium"}`)},
			expected:    "medium",
		},
		{
			name:        "Gemini thinking level",
			path:        "/v1beta/models/gemini-3-pro:generateContent",
			relayFormat: types.RelayFormatGemini,
			request: &dto.GeminiChatRequest{GenerationConfig: dto.GeminiChatGenerationConfig{
				ThinkingConfig: &dto.GeminiThinkingConfig{ThinkingLevel: "low"},
			}},
			expected: "low",
		},
		{
			name:        "Gemini uppercase enum thinking level is canonicalized",
			path:        "/v1beta/models/gemini-3.7-flash:generateContent",
			relayFormat: types.RelayFormatGemini,
			request: &dto.GeminiChatRequest{GenerationConfig: dto.GeminiChatGenerationConfig{
				ThinkingConfig: &dto.GeminiThinkingConfig{ThinkingLevel: " MEDIUM "},
			}},
			expected: "medium",
		},
		{
			name:        "Gemini unknown thinking level is recorded as sent",
			path:        "/v1beta/models/gemini-3.7-flash:generateContent",
			relayFormat: types.RelayFormatGemini,
			request: &dto.GeminiChatRequest{GenerationConfig: dto.GeminiChatGenerationConfig{
				ThinkingConfig: &dto.GeminiThinkingConfig{ThinkingLevel: "ULTRA"},
			}},
			expected: "ULTRA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", tt.path, nil)

			info, err := GenRelayInfo(ctx, tt.relayFormat, tt.request, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, info.ReasoningEffort)
		})
	}
}

func TestGenRelayInfoCapturesFastServiceTier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		format  types.RelayFormat
		request dto.Request
		want    bool
	}{
		{
			name:    "responses fast",
			format:  types.RelayFormatOpenAIResponses,
			request: &dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", ServiceTier: "fast"},
			want:    true,
		},
		{
			name:    "responses priority",
			format:  types.RelayFormatOpenAIResponses,
			request: &dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", ServiceTier: "priority"},
			want:    true,
		},
		{
			name:    "responses standard",
			format:  types.RelayFormatOpenAIResponses,
			request: &dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", ServiceTier: "standard"},
			want:    false,
		},
		{
			name:    "chat raw fast",
			format:  types.RelayFormatOpenAI,
			request: &dto.GeneralOpenAIRequest{Model: "gpt-5.6-sol", ServiceTier: json.RawMessage(`"fast"`)},
			want:    true,
		},
		{
			name:    "chat raw priority",
			format:  types.RelayFormatOpenAI,
			request: &dto.GeneralOpenAIRequest{Model: "gpt-5.6-sol", ServiceTier: json.RawMessage(`"priority"`)},
			want:    true,
		},
		{
			name:    "empty tier",
			format:  types.RelayFormatOpenAIResponsesCompaction,
			request: &dto.OpenAIResponsesCompactionRequest{Model: "gpt-5.6-sol"},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			info, err := GenRelayInfo(ctx, tt.format, tt.request, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, info.FastMode)
		})
	}
}

func TestGenRelayInfoKeepsOriginAndLeavesBillingUnset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	const model = "qwen3.8-max@thinking:on@temperature:0.2"
	ctx.Set("original_model", model)

	info, err := GenRelayInfo(ctx, types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{Model: model}, nil)
	require.NoError(t, err)
	assert.Equal(t, model, info.OriginModelName)
	assert.Empty(t, info.BillingModelName)
	assert.Equal(t, model, info.GetBillingModelName())
}

func TestInitChannelMetaRestoresRequestReasoningEffortForRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	request := &dto.OpenAIResponsesRequest{
		Model:     "gpt-5.6-sol",
		Reasoning: &dto.Reasoning{Effort: "max"},
	}
	info, err := GenRelayInfo(ctx, types.RelayFormatOpenAIResponses, request, nil)
	require.NoError(t, err)

	info.SetReasoningEffort("high")
	info.InitChannelMeta(ctx)
	assert.Equal(t, "max", info.ReasoningEffort)

	info.SetReasoningEffort("low")
	info.InitChannelMeta(ctx)
	assert.Equal(t, "max", info.ReasoningEffort)
}

func TestInitChannelMetaAppliesAdvancedCustomRoutePassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	advancedCustom := &dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath:           "/v1/chat/completions",
				UpstreamPath:           "/v1/chat/completions",
				Models:                 []string{"native-model"},
				PassThroughBodyEnabled: true,
			},
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/v1/messages",
				Converter:    relayconvert.ConverterOpenAIChatToClaudeMessages,
			},
		},
	}
	require.NoError(t, advancedCustom.Validate())

	tests := []struct {
		name            string
		channelType     int
		channelSetting  dto.ChannelSettings
		model           string
		wantPassThrough bool
		wantEffort      string
	}{
		{
			name:            "matched route enables pass-through and drops reasoning effort",
			channelType:     constant.ChannelTypeAdvancedCustom,
			model:           "native-model",
			wantPassThrough: true,
			wantEffort:      "",
		},
		{
			name:            "fallback converter route keeps conversion",
			channelType:     constant.ChannelTypeAdvancedCustom,
			model:           "other-model",
			wantPassThrough: false,
			wantEffort:      "high",
		},
		{
			name:            "channel-level pass-through still applies to converter route",
			channelType:     constant.ChannelTypeAdvancedCustom,
			channelSetting:  dto.ChannelSettings{PassThroughBodyEnabled: true},
			model:           "other-model",
			wantPassThrough: true,
			wantEffort:      "",
		},
		{
			name:            "route flag is ignored for other channel types",
			channelType:     constant.ChannelTypeOpenAI,
			model:           "native-model",
			wantPassThrough: false,
			wantEffort:      "high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			ctx.Set("original_model", tt.model)
			common.SetContextKey(ctx, constant.ContextKeyChannelType, tt.channelType)
			common.SetContextKey(ctx, constant.ContextKeyChannelSetting, tt.channelSetting)
			common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AdvancedCustom: advancedCustom})

			info, err := GenRelayInfo(ctx, types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{Model: tt.model, ReasoningEffort: "high"}, nil)
			require.NoError(t, err)

			info.InitChannelMeta(ctx)

			assert.Equal(t, tt.wantPassThrough, info.ChannelSetting.PassThroughBodyEnabled)
			assert.Equal(t, tt.wantEffort, info.ReasoningEffort)
		})
	}
}

func TestInitChannelMetaResetsPerAttemptStreamStateAndPreservesRequestState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	info, err := GenRelayInfo(ctx, types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{Model: "gpt-test"}, nil)
	require.NoError(t, err)

	claudeState := relayconvert.NewClaudeToChatStreamState()
	_, err = claudeState.ConvertChunk(&dto.ClaudeResponse{
		Type:  "content_block_start",
		Index: ptr(7),
		ContentBlock: &dto.ClaudeMediaMessage{
			Type: "tool_use",
			Id:   "toolu_1",
			Name: "lookup",
		},
	})
	require.NoError(t, err)
	_, err = claudeState.ConvertChunk(&dto.ClaudeResponse{
		Type:  "content_block_delta",
		Index: ptr(7),
		Delta: &dto.ClaudeMediaMessage{
			Type:        "input_json_delta",
			PartialJson: ptr(`{"q":"x"}`),
		},
	})
	require.NoError(t, err)

	geminiState, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatGemini, relayconvert.ResponseStreamOptions{
		ID:    "chatcmpl_1",
		Model: "gpt-test",
	})
	require.NoError(t, err)

	info.SendResponseCount = 3
	info.ClaudeToChatStreamState = claudeState
	info.ChatToGeminiStreamState = geminiState
	info.LastError = types.NewError(assert.AnError, types.ErrorCodeBadResponseBody)
	info.StreamStatus = NewStreamStatus()
	info.StreamStatus.RecordError("attempt 1 soft error")
	info.RecordConversionDiagnostics(context.Background(), []types.ConversionDiagnostic{{
		Code:     "test.loss",
		Message:  "attempt 1 conversion loss",
		Severity: types.ConversionDiagnosticWarning,
		From:     types.RelayFormatClaude,
		To:       types.RelayFormatOpenAI,
	}})

	info.InitChannelMeta(ctx)

	assert.Zero(t, info.SendResponseCount)
	assert.Nil(t, info.ClaudeToChatStreamState)
	assert.Nil(t, info.ChatToGeminiStreamState)

	require.NotNil(t, info.StreamStatus)
	assert.True(t, info.StreamStatus.HasErrors())
	assert.Equal(t, 1, info.StreamStatus.TotalErrorCount())
	diagnostics := info.ConversionDiagnostics()
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "test.loss", diagnostics[0].Code)
	require.NotNil(t, info.LastError)

	freshClaude := relayconvert.NewClaudeToChatStreamState()
	_, err = freshClaude.ConvertChunk(&dto.ClaudeResponse{
		Type:  "content_block_delta",
		Index: ptr(7),
		Delta: &dto.ClaudeMediaMessage{
			Type:        "input_json_delta",
			PartialJson: ptr(`{"q":"x"}`),
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown content block index")

	info.IncrSendResponseCount()
	responses := relayconvert.StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id:    "chatcmpl_retry",
		Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("hello")},
		}},
	}, info)
	require.NotEmpty(t, responses)
	assert.Equal(t, "message_start", responses[0].Type)
}

func ptr[T any](value T) *T {
	return &value
}
