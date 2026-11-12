package capability

import "fmt"

// Parameter 是模型对某个请求参数的支持声明，供模型广场的参数表展示。
// 只包含声明过的维度；未声明的参数不会出现在列表里。
type Parameter struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"` // number | integer | boolean | string | array | object | enum
	Range      string   `json:"range,omitempty"`
	MaxItems   *int     `json:"max_items,omitempty"`
	EnumValues []string `json:"enum_values,omitempty"`
}

// Parameters 从模型级声明派生请求参数表。
//
// 区间使用声明值；布尔能力显式为 false 视为不支持，不出现在表里。
// 默认值属于协议事实，由前端静态补齐。
func Parameters(spec Spec) []Parameter {
	var out []Parameter

	if spec.Context.MaxOutputTokens != nil {
		out = append(out, Parameter{
			Name:  "max_tokens",
			Type:  "integer",
			Range: fmt.Sprintf("1 ~ %d", *spec.Context.MaxOutputTokens),
		})
	}
	if rangeValue := spec.Params.Temperature; rangeValue != nil {
		out = append(out, Parameter{
			Name:  "temperature",
			Type:  "number",
			Range: floatRangeText(*rangeValue),
		})
	}
	if rangeValue := spec.Params.TopP; rangeValue != nil {
		out = append(out, Parameter{
			Name:  "top_p",
			Type:  "number",
			Range: floatRangeText(*rangeValue),
		})
	}
	if rangeValue := spec.Params.TopK; rangeValue != nil {
		out = append(out, Parameter{
			Name:  "top_k",
			Type:  "integer",
			Range: fmt.Sprintf("%d ~ %d", rangeValue.Min, rangeValue.Max),
		})
	}
	if Truthy(spec.Params.FrequencyPenalty) {
		out = append(out, Parameter{Name: "frequency_penalty", Type: "number", Range: "-2 ~ 2"})
	}
	if Truthy(spec.Params.PresencePenalty) {
		out = append(out, Parameter{Name: "presence_penalty", Type: "number", Range: "-2 ~ 2"})
	}
	if limit := spec.Params.Stop; limit != nil {
		max := limit.Max
		out = append(out, Parameter{Name: "stop", Type: "array", MaxItems: &max})
	}
	if Truthy(spec.Params.Seed) {
		out = append(out, Parameter{Name: "seed", Type: "integer"})
	}
	if limit := spec.Params.N; limit != nil {
		out = append(out, Parameter{
			Name:  "n",
			Type:  "integer",
			Range: fmt.Sprintf("1 ~ %d", limit.Max),
		})
	}
	if Truthy(spec.Streaming.Supported) {
		out = append(out, Parameter{Name: "stream", Type: "boolean"})
	}
	if Truthy(spec.Structured.JSONMode) || Truthy(spec.Structured.JSONSchema) {
		out = append(out, Parameter{Name: "response_format", Type: "object"})
	}
	if Truthy(spec.Tools.Supported) {
		out = append(out, Parameter{Name: "tools", Type: "array"})
	}
	if len(spec.Tools.ToolChoice) > 0 {
		out = append(out, Parameter{
			Name:       "tool_choice",
			Type:       "enum",
			EnumValues: append([]string{}, spec.Tools.ToolChoice...),
		})
	}
	if Truthy(spec.Params.Logprobs) {
		out = append(out, Parameter{Name: "logprobs", Type: "boolean"})
	}
	if len(spec.Reasoning.Efforts) > 0 {
		out = append(out, Parameter{
			Name:       "reasoning_effort",
			Type:       "enum",
			EnumValues: append([]string{}, spec.Reasoning.Efforts...),
		})
	}
	if rangeValue := spec.Reasoning.BudgetTokens; rangeValue != nil {
		out = append(out, Parameter{
			Name:  "reasoning_budget_tokens",
			Type:  "integer",
			Range: fmt.Sprintf("%d ~ %d", rangeValue.Min, rangeValue.Max),
		})
	}

	return out
}

func floatRangeText(value FloatRange) string {
	return fmt.Sprintf("%s ~ %s", formatFloat(value.Min), formatFloat(value.Max))
}
