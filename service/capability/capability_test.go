package capability

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRejectsUnknownFields(t *testing.T) {
	_, problems := Parse(`{"params":{"temprature":{"min":0,"max":1}}}`)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "params.temprature")
}

func TestParseRejectsInvalidRanges(t *testing.T) {
	_, problems := Parse(`{"params":{"temperature":{"min":2,"max":1}},"reasoning":{"budget_tokens":{"min":100,"max":50}}}`)
	require.Len(t, problems, 2)
	assert.Contains(t, problems[0], "reasoning.budget_tokens")
	assert.Contains(t, problems[1], "params.temperature")
}

func TestCheckSubsetRejectsNewDimensionAndWidening(t *testing.T) {
	base := Spec{
		Context: ContextSpec{MaxOutputTokens: ptrInt64(4096)},
		Params:  ParamsSpec{Temperature: &FloatRange{Min: 0, Max: 1}},
	}
	over := Spec{
		Context: ContextSpec{MaxOutputTokens: ptrInt64(8192)},
		Params:  ParamsSpec{Temperature: &FloatRange{Min: 0, Max: 1.5}},
		Tools:   ToolSpec{Supported: ptrBool(true)},
	}
	problems := CheckSubset(base, over)
	require.Len(t, problems, 3)
	assert.Contains(t, problems[0], "context.max_output_tokens")
	assert.Contains(t, problems[1], "tools.supported: model-level does not declare")
	assert.Contains(t, problems[2], "params.temperature")
}

func TestMergeOnlyOverridesDeclaredFields(t *testing.T) {
	base := Spec{
		Context: ContextSpec{MaxInputTokens: ptrInt64(1000), MaxOutputTokens: ptrInt64(200)},
		Tools:   ToolSpec{Supported: ptrBool(true), MaxTools: ptrInt(10)},
	}
	over := Spec{
		Context: ContextSpec{MaxOutputTokens: ptrInt64(100)},
		Tools:   ToolSpec{Supported: ptrBool(false)},
	}
	effective := Merge(base, over)
	assert.Equal(t, int64(1000), *effective.Context.MaxInputTokens)
	assert.Equal(t, int64(100), *effective.Context.MaxOutputTokens)
	assert.False(t, *effective.Tools.Supported)
	assert.Equal(t, 10, *effective.Tools.MaxTools)
}

func TestEvaluateReportsHardAndSoftViolations(t *testing.T) {
	spec := Spec{
		Modalities: ModalitySpec{Input: InputModalities{
			Text:  ptrBool(true),
			Image: ModalitySupport{Supported: ptrBool(false)},
		}},
		Tools:      ToolSpec{Supported: ptrBool(false)},
		Structured: StructuredSpec{JSONSchema: ptrBool(false)},
		Reasoning:  ReasoningSpec{Supported: ptrBool(true), Efforts: []string{"low", "high"}},
		Params:     ParamsSpec{Temperature: &FloatRange{Min: 0, Max: 1}},
	}
	needs := &Needs{
		InputModalities: []string{"text", "image"},
		ToolsCount:      1,
		ToolChoiceMode:  "required",
		JSONSchema:      true,
		ReasoningEffort: "medium",
		Temperature:     ptrFloat(1.5),
	}
	violations := Evaluate(spec, needs)
	dimensions := make(map[string]Severity, len(violations))
	for _, violation := range violations {
		dimensions[violation.Dimension] = violation.Severity
	}
	assert.Equal(t, SeverityHard, dimensions["modalities.input.image"])
	assert.Equal(t, SeverityHard, dimensions["tools.supported"])
	assert.Equal(t, SeverityHard, dimensions["structured_output.json_schema"])
	assert.Equal(t, SeverityHard, dimensions["reasoning.effort"])
	assert.Equal(t, SeveritySoft, dimensions["params.temperature"])
	assert.False(t, Satisfied(violations))
}

func TestEvaluateReportsSamplingParamViolationsAsSoft(t *testing.T) {
	spec := Spec{Params: ParamsSpec{
		TopP: &FloatRange{Min: 0, Max: 0.5},
		TopK: &IntRange{Min: 1, Max: 10},
		Stop: &Limit{Max: 2},
		N:    &Limit{Max: 1},
		Seed: ptrBool(false),
	}}
	needs := &Needs{
		TopP:        ptrFloat(0.7),
		TopK:        ptrInt64(20),
		StopCount:   3,
		Completions: ptrInt(2),
		Seed:        ptrInt64(7),
	}
	violations := Evaluate(spec, needs)
	dimensions := make(map[string]Severity, len(violations))
	for _, violation := range violations {
		dimensions[violation.Dimension] = violation.Severity
	}
	assert.Equal(t, SeveritySoft, dimensions["params.top_p"])
	assert.Equal(t, SeveritySoft, dimensions["params.top_k"])
	assert.Equal(t, SeveritySoft, dimensions["params.stop"])
	assert.Equal(t, SeveritySoft, dimensions["params.n"])
	assert.Equal(t, SeveritySoft, dimensions["params.seed"])
	assert.True(t, Satisfied(violations), "sampling parameters are recorded, never blocked")
}

func TestEvaluateRequiresDeclaredBuiltinTools(t *testing.T) {
	needs := &Needs{BuiltinTools: []string{"web_search"}}
	violations := Evaluate(Spec{}, needs)
	require.Len(t, violations, 1)
	assert.Equal(t, "tools.builtin", violations[0].Dimension)
	assert.True(t, Satisfied([]Violation{}))
}

// tools.builtin 是唯一"没列出就是不支持"的枚举维度，所以渠道写 [] 要能把模型级的
// 内置工具收窄到零；不写（或 null）才继承模型级。
func TestMergeBuiltinToolsDistinguishesEmptyFromAbsent(t *testing.T) {
	base := Spec{Tools: ToolSpec{Supported: ptrBool(true), Builtin: []string{"web_search", "code_execution"}}}
	needsWebSearch := &Needs{BuiltinTools: []string{"web_search"}}

	tests := []struct {
		name            string
		narrowing       string
		wantBuiltin     []string
		wantWebSearchOK bool
	}{
		{"字段不写则继承模型级", `{"tools":{"supported":true}}`, []string{"web_search", "code_execution"}, true},
		{"显式 null 也继承模型级", `{"tools":{"builtin":null}}`, []string{"web_search", "code_execution"}, true},
		{"空数组收窄到没有任何内置工具", `{"tools":{"builtin":[]}}`, []string{}, false},
		{"更短的列表整体替换模型级", `{"tools":{"builtin":["code_execution"]}}`, []string{"code_execution"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			over, problems := Parse(tt.narrowing)
			require.Empty(t, problems)
			require.Empty(t, CheckSubset(base, over), "收窄必须是模型级的子集才允许保存")

			effective := Merge(base, over)
			assert.Equal(t, tt.wantBuiltin, effective.Tools.Builtin)
			assert.Equal(t, tt.wantWebSearchOK, Satisfied(Evaluate(effective, needsWebSearch)))
			assert.Equal(t, tt.wantWebSearchOK, slices.Contains(Summarize(effective).Capabilities, SummaryWebSearch))
		})
	}
}

// 控制器读取渠道收窄时会重新编码，空数组不能被 omitempty 吞掉，
// 否则抽屉里看不到已保存的收窄，下次保存还会把它丢掉。
func TestEncodeKeepsDeclaredEmptyBuiltinTools(t *testing.T) {
	declared, problems := Parse(`{"tools":{"builtin":[]}}`)
	require.Empty(t, problems)
	encoded, err := Encode(declared)
	require.NoError(t, err)
	assert.Contains(t, encoded, `"builtin":[]`)

	reparsed, problems := Parse(encoded)
	require.Empty(t, problems)
	require.NotNil(t, reparsed.Tools.Builtin)
	assert.Empty(t, reparsed.Tools.Builtin)

	undeclared, err := Encode(Spec{})
	require.NoError(t, err)
	assert.NotContains(t, undeclared, "builtin")
}

func TestExtractChatNeeds(t *testing.T) {
	body := []byte(`{
		"model":"m","stream":true,"max_tokens":512,"temperature":0.3,
		"top_p":0.7,"top_k":5,"stop":["a","b"],"n":2,"seed":9,
		"presence_penalty":0.4,"frequency_penalty":0.1,
		"tools":[{"type":"function"},{"type":"web_search"}],
		"tool_choice":"required","response_format":{"type":"json_schema"},
		"messages":[
			{"role":"system","content":"sys"},
			{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}
		]
	}`)
	needs, err := Extract(FormatOpenAIChat, body)
	require.NoError(t, err)
	assert.True(t, needs.Stream)
	assert.Equal(t, int64(512), *needs.MaxOutputTokens)
	assert.Equal(t, 1, needs.ToolsCount)
	assert.Contains(t, needs.BuiltinTools, "web_search")
	assert.Equal(t, "required", needs.ToolChoiceMode)
	assert.True(t, needs.JSONSchema)
	assert.Equal(t, 1, needs.SystemMessages)
	assert.Equal(t, 0.7, *needs.TopP)
	assert.Equal(t, int64(5), *needs.TopK)
	assert.Equal(t, 2, needs.StopCount)
	assert.Equal(t, 2, *needs.Completions)
	assert.Equal(t, int64(9), *needs.Seed)
	assert.Equal(t, 0.4, *needs.PresencePenalty)
	assert.Equal(t, 0.1, *needs.FrequencyPenalty)
	assert.Contains(t, needs.InputModalities, "image")
	assert.Contains(t, needs.Mimes, "image/png")
	assert.Zero(t, needs.EstimatedInputTokens, "输入 token 估算取决于候选模型的家族，不在提取阶段算")
}

func TestSummarizeDoesNotInventBuiltinTools(t *testing.T) {
	summary := Summarize(Spec{})
	assert.NotContains(t, summary.Capabilities, SummaryWebSearch)
	assert.NotContains(t, summary.Capabilities, SummaryCodeExec)
}

func TestParametersOnlyIncludesDeclaredDimensions(t *testing.T) {
	parameters := Parameters(Spec{
		Context: ContextSpec{MaxOutputTokens: ptrInt64(1024)},
		Params: ParamsSpec{
			Temperature: &FloatRange{Min: 0, Max: 1.5},
			N:           &Limit{Max: 2},
		},
		Streaming: StreamingSpec{Supported: ptrBool(true)},
	})
	byName := make(map[string]Parameter, len(parameters))
	for _, parameter := range parameters {
		byName[parameter.Name] = parameter
	}
	assert.Equal(t, "1 ~ 1024", byName["max_tokens"].Range)
	assert.Equal(t, "0 ~ 1.5", byName["temperature"].Range)
	assert.Equal(t, "1 ~ 2", byName["n"].Range)
	assert.Equal(t, "boolean", byName["stream"].Type)
	assert.NotContains(t, byName, "tools")
}

func ptrBool(value bool) *bool        { return &value }
func ptrInt(value int) *int           { return &value }
func ptrInt64(value int64) *int64     { return &value }
func ptrFloat(value float64) *float64 { return &value }
