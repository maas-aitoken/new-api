package capability

import (
	"sort"
	"strings"
)

// Severity 是违规的严重度。
type Severity string

const (
	SeverityHard Severity = "hard"
	SeveritySoft Severity = "soft"
)

// Violation 一条不满足。
type Violation struct {
	Dimension string   `json:"dimension"`
	Required  any      `json:"required"`
	Supported any      `json:"supported,omitempty"`
	Severity  Severity `json:"severity"`
}

// Evaluate 返回 spec 无法满足 needs 的全部违规项。
func Evaluate(spec Spec, needs *Needs) []Violation {
	if needs == nil {
		return nil
	}
	var out []Violation
	add := func(dimension string, required, supported any) {
		out = append(out, Violation{
			Dimension: dimension,
			Required:  required,
			Supported: supported,
			Severity:  severityOf(dimension),
		})
	}

	if needs.Stream && !Supports(spec.Streaming.Supported) {
		add("streaming.supported", true, false)
	}

	if needs.MaxOutputTokens != nil && spec.Context.MaxOutputTokens != nil &&
		*needs.MaxOutputTokens > *spec.Context.MaxOutputTokens {
		add("context.max_output_tokens", *needs.MaxOutputTokens, *spec.Context.MaxOutputTokens)
	}
	if needs.EstimatedInputTokens > 0 && spec.Context.MaxInputTokens != nil &&
		needs.EstimatedInputTokens > *spec.Context.MaxInputTokens {
		add("context.max_input_tokens", needs.EstimatedInputTokens, *spec.Context.MaxInputTokens)
	}

	for _, modality := range needs.InputModalities {
		if !modalitySupported(spec.Modalities.Input, modality) {
			add("modalities.input."+modality, true, false)
		}
	}
	for _, mime := range needs.Mimes {
		modality := ModalityFromMime(mime)
		support := modalitySupport(spec.Modalities.Input, modality)
		if len(support.Formats) > 0 && !Allows(support.Formats, mime) {
			add("modalities.input."+modality+".formats", mime, support.Formats)
		}
	}
	// 传入方式在 Needs 里是全局汇总的，因此只要有一个在用模态接受该方式即放行。
	for _, source := range needs.Sources {
		if !anyModalityAllowsSource(spec.Modalities.Input, needs.InputModalities, source) {
			add("modalities.input.sources", source, sourceLists(spec.Modalities.Input, needs.InputModalities))
		}
	}
	for _, modality := range sortedIntKeys(needs.MediaCounts) {
		support := modalitySupport(spec.Modalities.Input, modality)
		if support.MaxCount != nil && needs.MediaCounts[modality] > *support.MaxCount {
			add("modalities.input."+modality+".max_count", needs.MediaCounts[modality], *support.MaxCount)
		}
	}
	for _, modality := range sortedIntKeys(needs.MediaMaxSizeMB) {
		support := modalitySupport(spec.Modalities.Input, modality)
		if support.MaxSizeMB != nil && needs.MediaMaxSizeMB[modality] > *support.MaxSizeMB {
			add("modalities.input."+modality+".max_size_mb", needs.MediaMaxSizeMB[modality], *support.MaxSizeMB)
		}
	}

	if needs.ToolsCount > 0 && !Supports(spec.Tools.Supported) {
		add("tools.supported", needs.ToolsCount, false)
	}
	if needs.ToolsCount > 0 && !AcceptsCount(spec.Tools.MaxTools, needs.ToolsCount) {
		add("tools.max_tools", needs.ToolsCount, *spec.Tools.MaxTools)
	}
	if needs.ToolChoiceMode != "" && !Allows(spec.Tools.ToolChoice, needs.ToolChoiceMode) {
		add("tools.tool_choice", needs.ToolChoiceMode, spec.Tools.ToolChoice)
	}
	if needs.ParallelToolCalls != nil && *needs.ParallelToolCalls && spec.Tools.Parallel != nil && !*spec.Tools.Parallel {
		add("tools.parallel", true, false)
	}
	// 内置工具是唯一"未声明即不支持"的维度。
	for _, tool := range needs.BuiltinTools {
		if !containsValue(spec.Tools.Builtin, tool) {
			add("tools.builtin", tool, spec.Tools.Builtin)
		}
	}

	if needs.JSONMode && !Supports(spec.Structured.JSONMode) {
		add("structured_output.json_mode", true, false)
	}
	if needs.JSONSchema && !Supports(spec.Structured.JSONSchema) {
		add("structured_output.json_schema", true, false)
	}

	reasoningDisabled := needs.ReasoningEffort == "none" ||
		(needs.ReasoningBudget != nil && *needs.ReasoningBudget == 0) ||
		(needs.ReasoningEnabled != nil && !*needs.ReasoningEnabled)
	reasoningEnabled := (needs.ReasoningEnabled != nil && *needs.ReasoningEnabled) ||
		(needs.ReasoningEffort != "" && needs.ReasoningEffort != "none") ||
		(needs.ReasoningBudget != nil && *needs.ReasoningBudget > 0)

	if reasoningDisabled && spec.Reasoning.Mandatory != nil && *spec.Reasoning.Mandatory {
		add("reasoning.mandatory", false, true)
	}
	if reasoningEnabled && !Supports(spec.Reasoning.Supported) {
		add("reasoning.supported", true, false)
	}
	// 档位统一按 efforts 集合校验，none 也在此列。
	if effort := needs.ReasoningEffort; effort != "" {
		if !Allows(spec.Reasoning.Efforts, effort) {
			add("reasoning.effort", effort, spec.Reasoning.Efforts)
		}
	}
	if needs.ReasoningBudget != nil && *needs.ReasoningBudget > 0 && spec.Reasoning.BudgetTokens != nil &&
		!spec.Reasoning.BudgetTokens.Accepts(*needs.ReasoningBudget) {
		add("reasoning.budget_tokens", *needs.ReasoningBudget, spec.Reasoning.BudgetTokens)
	}

	// 采样参数只报不拦：客户端可以接受被裁掉的采样偏好。
	if needs.Temperature != nil && !spec.Params.Temperature.Accepts(*needs.Temperature) {
		add("params.temperature", *needs.Temperature, spec.Params.Temperature)
	}
	if needs.TopP != nil && !spec.Params.TopP.Accepts(*needs.TopP) {
		add("params.top_p", *needs.TopP, spec.Params.TopP)
	}
	if needs.TopK != nil && !spec.Params.TopK.Accepts(*needs.TopK) {
		add("params.top_k", *needs.TopK, spec.Params.TopK)
	}
	if needs.StopCount > 0 && spec.Params.Stop != nil && spec.Params.Stop.Max > 0 && needs.StopCount > spec.Params.Stop.Max {
		add("params.stop", needs.StopCount, *spec.Params.Stop)
	}
	if needs.Completions != nil && spec.Params.N != nil && spec.Params.N.Max > 0 && *needs.Completions > spec.Params.N.Max {
		add("params.n", *needs.Completions, *spec.Params.N)
	}
	if needs.Seed != nil && !Truthy(spec.Params.Seed) {
		add("params.seed", *needs.Seed, false)
	}
	if needs.Logprobs != nil && *needs.Logprobs && !Truthy(spec.Params.Logprobs) {
		add("params.logprobs", true, false)
	}
	if needs.PresencePenalty != nil && *needs.PresencePenalty != 0 && !Truthy(spec.Params.PresencePenalty) {
		add("params.presence_penalty", *needs.PresencePenalty, false)
	}
	if needs.FrequencyPenalty != nil && *needs.FrequencyPenalty != 0 && !Truthy(spec.Params.FrequencyPenalty) {
		add("params.frequency_penalty", *needs.FrequencyPenalty, false)
	}

	if needs.SystemMessages > 0 && spec.Messages.SystemRole == "none" {
		add("messages.system_role", needs.SystemMessages, "none")
	}
	if needs.SystemMessages > 1 && spec.Messages.MultiSystem != nil && !*spec.Messages.MultiSystem {
		add("messages.multi_system", needs.SystemMessages, false)
	}
	if needs.AssistantPrefill && spec.Messages.AssistantPrefill != nil && !*spec.Messages.AssistantPrefill {
		add("messages.assistant_prefill", true, false)
	}

	return out
}

// Satisfied 报告是否没有任何 hard 违规。soft 违规不影响结论。
func Satisfied(violations []Violation) bool {
	for _, violation := range violations {
		if violation.Severity == SeverityHard {
			return false
		}
	}
	return true
}

func severityOf(dimension string) Severity {
	if strings.HasPrefix(dimension, "params.") || dimension == "tools.parallel" {
		return SeveritySoft
	}
	return SeverityHard
}

func modalitySupported(input InputModalities, modality string) bool {
	switch modality {
	case "text":
		return Supports(input.Text)
	default:
		return Supports(modalitySupport(input, modality).Supported)
	}
}

func modalitySupport(input InputModalities, modality string) ModalitySupport {
	switch modality {
	case "image":
		return input.Image
	case "audio":
		return input.Audio
	case "video":
		return input.Video
	default:
		return input.File
	}
}

func anyModalityAllowsSource(input InputModalities, modalities []string, source string) bool {
	for _, modality := range modalities {
		support := modalitySupport(input, modality)
		if len(support.Sources) == 0 || Allows(support.Sources, source) {
			return true
		}
	}
	return false
}

func sortedIntKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sourceLists(input InputModalities, modalities []string) []string {
	set := map[string]struct{}{}
	for _, modality := range modalities {
		for _, source := range modalitySupport(input, modality).Sources {
			set[source] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for source := range set {
		out = append(out, source)
	}
	sort.Strings(out)
	return out
}
