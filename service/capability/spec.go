// Package capability 定义硬约束能力的值域类型，以及"两层收窄"的语义。
//
// 只有两层：
//   - 模型层（models.capabilities）：该对外模型全部硬约束能力的声明。
//   - (channel, model) 层（channel_model_capabilities）：某个具体渠道对该模型能力的收窄，
//     只能是模型层的子集。
//
// 两层共用同一个 Spec 类型：模型层写全量，渠道层写部分；渠道层里的零值字段
// 表示"沿用模型层"，因此收窄天然不可能放大能力。
//
// 零值约定（nil / 0 / 空切片）统一表示"未声明"，不参与校验；
// 收窄可以从"未声明"收紧到具体值，反向不允许。
package capability

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Spec 是一条完整或部分的能力声明。模型层用完整形态，渠道层用部分形态。
type Spec struct {
	Context    ContextSpec    `json:"context"`
	Modalities ModalitySpec   `json:"modalities"`
	Tools      ToolSpec       `json:"tools"`
	Structured StructuredSpec `json:"structured_output"`
	Reasoning  ReasoningSpec  `json:"reasoning"`
	Params     ParamsSpec     `json:"params"`
	Messages   MessageSpec    `json:"messages"`
	Streaming  StreamingSpec  `json:"streaming"`
}

// ContextSpec 上下文与输出长度上限，单位 token。
type ContextSpec struct {
	MaxInputTokens  *int64 `json:"max_input_tokens,omitempty"`
	MaxOutputTokens *int64 `json:"max_output_tokens,omitempty"`
}

// ModalitySpec 输入/输出模态。
type ModalitySpec struct {
	Input  InputModalities `json:"input"`
	Output []string        `json:"output,omitempty"` // text | image | audio
}

// InputModalities 五种输入模态。Text 用裸指针：纯文本是最基本的门槛，
// 未声明时不校验，显式 false 才代表不支持。
type InputModalities struct {
	Text  *bool           `json:"text,omitempty"`
	Image ModalitySupport `json:"image"`
	Audio ModalitySupport `json:"audio"`
	Video ModalitySupport `json:"video"`
	File  ModalitySupport `json:"file"`
}

// ModalitySupport 一种输入模态的支持情况。
// 空 Formats / 空 Sources 表示"未枚举"，不做枚举校验。
type ModalitySupport struct {
	Supported *bool    `json:"supported,omitempty"`
	Formats   []string `json:"formats,omitempty"` // MIME，如 image/png
	Sources   []string `json:"sources,omitempty"` // url | base64 | file_id
	MaxCount  *int     `json:"max_count,omitempty"`
	MaxSizeMB *int     `json:"max_size_mb,omitempty"`
}

// ToolSpec 工具调用能力。
type ToolSpec struct {
	Supported  *bool    `json:"supported,omitempty"`
	Parallel   *bool    `json:"parallel,omitempty"`
	ToolChoice []string `json:"tool_choice,omitempty"` // auto | none | required | named
	// Builtin 区分三态：不写（nil）= 沿用上一层；[] = 一个内置工具都不支持；
	// 非空 = 只支持列出的。必须用 omitzero：omitempty 会把 [] 当成没写而丢掉。
	Builtin  []string `json:"builtin,omitzero"` // web_search | code_execution | file_search | computer_use
	MaxTools *int     `json:"max_tools,omitempty"`
}

// StructuredSpec 结构化输出。json_mode 只保证是合法 JSON，json_schema 按 schema 校验。
type StructuredSpec struct {
	JSONMode   *bool `json:"json_mode,omitempty"`
	JSONSchema *bool `json:"json_schema,omitempty"`
}

// ReasoningSpec 推理/思考能力。
type ReasoningSpec struct {
	Supported *bool    `json:"supported,omitempty"`
	Mandatory *bool    `json:"mandatory,omitempty"` // true 表示关不掉
	Efforts   []string `json:"efforts,omitempty"`
	// BudgetTokens 是 token 预算的闭区间。
	BudgetTokens *IntRange `json:"budget_tokens,omitempty"`
	// OutputField 是思考内容在响应里的回显字段名，仅供展示，不参与校验。
	OutputField string `json:"output_field,omitempty"`
}

// Declared 报告声明里是否写了推理维度的任何一项。
// 什么都没写表示该模型不做推理校验，也就不知道它能不能思考。
func (r ReasoningSpec) Declared() bool {
	return r.Supported != nil || r.Mandatory != nil || len(r.Efforts) > 0 || r.BudgetTokens != nil
}

// ParamsSpec 采样参数值域。这里只登记值域，不做裁剪。
type ParamsSpec struct {
	Temperature      *FloatRange `json:"temperature,omitempty"`
	TopP             *FloatRange `json:"top_p,omitempty"`
	TopK             *IntRange   `json:"top_k,omitempty"`
	Seed             *bool       `json:"seed,omitempty"`
	Logprobs         *bool       `json:"logprobs,omitempty"`
	PresencePenalty  *bool       `json:"presence_penalty,omitempty"`
	FrequencyPenalty *bool       `json:"frequency_penalty,omitempty"`
	Stop             *Limit      `json:"stop,omitempty"`
	N                *Limit      `json:"n,omitempty"`
}

// Limit 是计数型维度的值域：一次请求最多允许多少个。
type Limit struct {
	Max int `json:"max"`
}

// IntRange 整数闭区间。
type IntRange struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

// FloatRange 浮点闭区间。
type FloatRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// MessageSpec 消息结构习惯。
type MessageSpec struct {
	SystemRole       string `json:"system_role,omitempty"` // system | developer | none
	AssistantPrefill *bool  `json:"assistant_prefill,omitempty"`
	MultiSystem      *bool  `json:"multi_system,omitempty"`
}

// StreamingSpec 流式能力。
type StreamingSpec struct {
	Supported     *bool `json:"supported,omitempty"`
	UsageInStream *bool `json:"usage_in_stream,omitempty"`
}

// Parse 解析并校验一条声明。raw 为空表示未声明，返回零值与 nil。
// 除类型错误外，还会拒绝 Spec 中不存在的字段名，避免错字被静默忽略。
func Parse(raw string) (Spec, []string) {
	var spec Spec
	if strings.TrimSpace(raw) == "" {
		return spec, nil
	}
	if err := common.UnmarshalJsonStr(raw, &spec); err != nil {
		return spec, []string{"capabilities: " + err.Error()}
	}
	problems := unknownKeys(raw)
	problems = append(problems, Validate(spec)...)
	return spec, problems
}

// MustParse 在调用方已经校验过时使用；解析失败返回零值。
func MustParse(raw string) Spec {
	spec, problems := Parse(raw)
	if len(problems) > 0 {
		return Spec{}
	}
	return spec
}

// Encode 把声明编码成存储/传输用的 JSON 字符串。
func Encode(spec Spec) (string, error) {
	data, err := common.Marshal(spec)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// unknownKeys 用反射遍历声明结构，报告下发 JSON 里不存在的字段路径。
// 类型错误已由 Unmarshal 拦下，这里只负责"拼错的字段名"。
func unknownKeys(raw string) []string {
	var value any
	if err := common.UnmarshalJsonStr(raw, &value); err != nil {
		return nil
	}
	var out []string
	walkJSONKeys(value, reflect.TypeFor[Spec](), "", &out)
	return out
}

func walkJSONKeys(value any, typ reflect.Type, path string, out *[]string) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return
		}
		fields := make(map[string]reflect.Type, typ.NumField())
		for i := range typ.NumField() {
			field := typ.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if tag == "" || tag == "-" {
				continue
			}
			fields[tag] = field.Type
		}
		for key, child := range object {
			childType, ok := fields[key]
			if !ok {
				*out = append(*out, joinPath(path, key)+": unknown field")
				continue
			}
			walkJSONKeys(child, childType, joinPath(path, key), out)
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return
		}
		for _, item := range items {
			walkJSONKeys(item, typ.Elem(), path+"[]", out)
		}
	}
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

// Merge 返回 base 被 over 收窄后的生效能力。over 中未声明的字段继续沿用 base。
func Merge(base, over Spec) Spec {
	out := base

	if over.Context.MaxInputTokens != nil {
		out.Context.MaxInputTokens = over.Context.MaxInputTokens
	}
	if over.Context.MaxOutputTokens != nil {
		out.Context.MaxOutputTokens = over.Context.MaxOutputTokens
	}

	if over.Modalities.Input.Text != nil {
		out.Modalities.Input.Text = over.Modalities.Input.Text
	}
	out.Modalities.Input.Image = mergeModality(base.Modalities.Input.Image, over.Modalities.Input.Image)
	out.Modalities.Input.Audio = mergeModality(base.Modalities.Input.Audio, over.Modalities.Input.Audio)
	out.Modalities.Input.Video = mergeModality(base.Modalities.Input.Video, over.Modalities.Input.Video)
	out.Modalities.Input.File = mergeModality(base.Modalities.Input.File, over.Modalities.Input.File)
	if len(over.Modalities.Output) > 0 {
		out.Modalities.Output = over.Modalities.Output
	}

	if over.Tools.Supported != nil {
		out.Tools.Supported = over.Tools.Supported
	}
	if over.Tools.Parallel != nil {
		out.Tools.Parallel = over.Tools.Parallel
	}
	if len(over.Tools.ToolChoice) > 0 {
		out.Tools.ToolChoice = over.Tools.ToolChoice
	}
	// 内置工具没列出就是不支持，所以渠道写 [] 是有意义的收窄，不能按"没声明"回落。
	if over.Tools.Builtin != nil {
		out.Tools.Builtin = over.Tools.Builtin
	}
	if over.Tools.MaxTools != nil {
		out.Tools.MaxTools = over.Tools.MaxTools
	}

	if over.Structured.JSONMode != nil {
		out.Structured.JSONMode = over.Structured.JSONMode
	}
	if over.Structured.JSONSchema != nil {
		out.Structured.JSONSchema = over.Structured.JSONSchema
	}

	if over.Reasoning.Supported != nil {
		out.Reasoning.Supported = over.Reasoning.Supported
	}
	if over.Reasoning.Mandatory != nil {
		out.Reasoning.Mandatory = over.Reasoning.Mandatory
	}
	if len(over.Reasoning.Efforts) > 0 {
		out.Reasoning.Efforts = over.Reasoning.Efforts
	}
	if over.Reasoning.BudgetTokens != nil {
		out.Reasoning.BudgetTokens = over.Reasoning.BudgetTokens
	}
	if over.Reasoning.OutputField != "" {
		out.Reasoning.OutputField = over.Reasoning.OutputField
	}

	if over.Params.Temperature != nil {
		out.Params.Temperature = over.Params.Temperature
	}
	if over.Params.TopP != nil {
		out.Params.TopP = over.Params.TopP
	}
	if over.Params.TopK != nil {
		out.Params.TopK = over.Params.TopK
	}
	if over.Params.Seed != nil {
		out.Params.Seed = over.Params.Seed
	}
	if over.Params.Logprobs != nil {
		out.Params.Logprobs = over.Params.Logprobs
	}
	if over.Params.PresencePenalty != nil {
		out.Params.PresencePenalty = over.Params.PresencePenalty
	}
	if over.Params.FrequencyPenalty != nil {
		out.Params.FrequencyPenalty = over.Params.FrequencyPenalty
	}
	if over.Params.Stop != nil {
		out.Params.Stop = over.Params.Stop
	}
	if over.Params.N != nil {
		out.Params.N = over.Params.N
	}

	if over.Messages.SystemRole != "" {
		out.Messages.SystemRole = over.Messages.SystemRole
	}
	if over.Messages.AssistantPrefill != nil {
		out.Messages.AssistantPrefill = over.Messages.AssistantPrefill
	}
	if over.Messages.MultiSystem != nil {
		out.Messages.MultiSystem = over.Messages.MultiSystem
	}

	if over.Streaming.Supported != nil {
		out.Streaming.Supported = over.Streaming.Supported
	}
	if over.Streaming.UsageInStream != nil {
		out.Streaming.UsageInStream = over.Streaming.UsageInStream
	}

	return out
}

func mergeModality(base, over ModalitySupport) ModalitySupport {
	out := base
	if over.Supported != nil {
		out.Supported = over.Supported
	}
	if len(over.Formats) > 0 {
		out.Formats = over.Formats
	}
	if len(over.Sources) > 0 {
		out.Sources = over.Sources
	}
	if over.MaxCount != nil {
		out.MaxCount = over.MaxCount
	}
	if over.MaxSizeMB != nil {
		out.MaxSizeMB = over.MaxSizeMB
	}
	return out
}

// CheckSubset 校验 over 落在 base 的值域内，且 base 声明过该维度。
//
// 两条不变量：
//  1. 渠道层只能在模型层声明过的维度上收窄，不能凭空新增能力；
//  2. 收窄后的取值必须是模型层值域的子集。
func CheckSubset(base, over Spec) []string {
	var out []string
	add := func(format string, args ...any) {
		out = append(out, fmt.Sprintf(format, args...))
	}

	if over.Context.MaxInputTokens != nil {
		if requireDeclared(&out, "context.max_input_tokens", base.Context.MaxInputTokens != nil) &&
			*over.Context.MaxInputTokens > *base.Context.MaxInputTokens {
			add("context.max_input_tokens: %d exceeds model-level %d", *over.Context.MaxInputTokens, *base.Context.MaxInputTokens)
		}
	}
	if over.Context.MaxOutputTokens != nil {
		if requireDeclared(&out, "context.max_output_tokens", base.Context.MaxOutputTokens != nil) &&
			*over.Context.MaxOutputTokens > *base.Context.MaxOutputTokens {
			add("context.max_output_tokens: %d exceeds model-level %d", *over.Context.MaxOutputTokens, *base.Context.MaxOutputTokens)
		}
	}

	if over.Modalities.Input.Text != nil {
		if requireDeclared(&out, "modalities.input.text", base.Modalities.Input.Text != nil) &&
			*over.Modalities.Input.Text && !*base.Modalities.Input.Text {
			add("modalities.input.text: model-level does not support text")
		}
	}
	checkModalitySubset(&out, "modalities.input.image", base.Modalities.Input.Image, over.Modalities.Input.Image)
	checkModalitySubset(&out, "modalities.input.audio", base.Modalities.Input.Audio, over.Modalities.Input.Audio)
	checkModalitySubset(&out, "modalities.input.video", base.Modalities.Input.Video, over.Modalities.Input.Video)
	checkModalitySubset(&out, "modalities.input.file", base.Modalities.Input.File, over.Modalities.Input.File)
	checkEnumeratedSubset(&out, "modalities.output", base.Modalities.Output, over.Modalities.Output)

	checkBoolSubset(&out, "tools.supported", base.Tools.Supported, over.Tools.Supported)
	checkBoolSubset(&out, "tools.parallel", base.Tools.Parallel, over.Tools.Parallel)
	checkEnumeratedSubset(&out, "tools.tool_choice", base.Tools.ToolChoice, over.Tools.ToolChoice)
	checkEnumeratedSubset(&out, "tools.builtin", base.Tools.Builtin, over.Tools.Builtin)
	if over.Tools.MaxTools != nil {
		if requireDeclared(&out, "tools.max_tools", base.Tools.MaxTools != nil) &&
			*over.Tools.MaxTools > *base.Tools.MaxTools {
			add("tools.max_tools: %d exceeds model-level %d", *over.Tools.MaxTools, *base.Tools.MaxTools)
		}
	}

	checkBoolSubset(&out, "structured_output.json_mode", base.Structured.JSONMode, over.Structured.JSONMode)
	checkBoolSubset(&out, "structured_output.json_schema", base.Structured.JSONSchema, over.Structured.JSONSchema)

	checkBoolSubset(&out, "reasoning.supported", base.Reasoning.Supported, over.Reasoning.Supported)
	if over.Reasoning.Mandatory != nil {
		// mandatory 方向相反：false 表示"可以关掉"，比模型层的 true 更弱，属于合法收窄。
		if requireDeclared(&out, "reasoning.mandatory", base.Reasoning.Mandatory != nil) &&
			!*over.Reasoning.Mandatory && *base.Reasoning.Mandatory {
			add("reasoning.mandatory: model-level requires reasoning, channel cannot declare it optional")
		}
	}
	checkEnumeratedSubset(&out, "reasoning.efforts", base.Reasoning.Efforts, over.Reasoning.Efforts)
	if over.Reasoning.BudgetTokens != nil {
		if requireDeclared(&out, "reasoning.budget_tokens", base.Reasoning.BudgetTokens != nil) {
			b, o := base.Reasoning.BudgetTokens, over.Reasoning.BudgetTokens
			if o.Min < b.Min || o.Max > b.Max {
				add("reasoning.budget_tokens: [%d,%d] exceeds model-level [%d,%d]", o.Min, o.Max, b.Min, b.Max)
			}
		}
	}

	if over.Params.Temperature != nil {
		if requireDeclared(&out, "params.temperature", base.Params.Temperature != nil) {
			checkFloatRange(&out, "params.temperature", *base.Params.Temperature, *over.Params.Temperature)
		}
	}
	if over.Params.TopP != nil {
		if requireDeclared(&out, "params.top_p", base.Params.TopP != nil) {
			checkFloatRange(&out, "params.top_p", *base.Params.TopP, *over.Params.TopP)
		}
	}
	if over.Params.TopK != nil {
		if requireDeclared(&out, "params.top_k", base.Params.TopK != nil) {
			b, o := *base.Params.TopK, *over.Params.TopK
			if o.Min < b.Min || o.Max > b.Max {
				add("params.top_k: [%d,%d] exceeds model-level [%d,%d]", o.Min, o.Max, b.Min, b.Max)
			}
		}
	}
	checkBoolSubset(&out, "params.seed", base.Params.Seed, over.Params.Seed)
	checkBoolSubset(&out, "params.logprobs", base.Params.Logprobs, over.Params.Logprobs)
	checkBoolSubset(&out, "params.presence_penalty", base.Params.PresencePenalty, over.Params.PresencePenalty)
	checkBoolSubset(&out, "params.frequency_penalty", base.Params.FrequencyPenalty, over.Params.FrequencyPenalty)
	checkLimitSubset(&out, "params.stop", base.Params.Stop, over.Params.Stop)
	checkLimitSubset(&out, "params.n", base.Params.N, over.Params.N)

	if over.Messages.SystemRole != "" {
		if requireDeclared(&out, "messages.system_role", base.Messages.SystemRole != "") &&
			over.Messages.SystemRole != base.Messages.SystemRole {
			add("messages.system_role: channel declares %q but model-level declares %q",
				over.Messages.SystemRole, base.Messages.SystemRole)
		}
	}
	checkBoolSubset(&out, "messages.assistant_prefill", base.Messages.AssistantPrefill, over.Messages.AssistantPrefill)
	checkBoolSubset(&out, "messages.multi_system", base.Messages.MultiSystem, over.Messages.MultiSystem)

	checkBoolSubset(&out, "streaming.supported", base.Streaming.Supported, over.Streaming.Supported)
	checkBoolSubset(&out, "streaming.usage_in_stream", base.Streaming.UsageInStream, over.Streaming.UsageInStream)

	return out
}

// Validate 校验一条声明自身是否合法：区间方向与取值范围。
func Validate(spec Spec) []string {
	var out []string
	add := func(format string, args ...any) {
		out = append(out, fmt.Sprintf(format, args...))
	}

	if spec.Context.MaxInputTokens != nil && *spec.Context.MaxInputTokens <= 0 {
		add("context.max_input_tokens: %d must be positive", *spec.Context.MaxInputTokens)
	}
	if spec.Context.MaxOutputTokens != nil && *spec.Context.MaxOutputTokens <= 0 {
		add("context.max_output_tokens: %d must be positive", *spec.Context.MaxOutputTokens)
	}

	validateModality(&out, "modalities.input.image", spec.Modalities.Input.Image)
	validateModality(&out, "modalities.input.audio", spec.Modalities.Input.Audio)
	validateModality(&out, "modalities.input.video", spec.Modalities.Input.Video)
	validateModality(&out, "modalities.input.file", spec.Modalities.Input.File)

	if spec.Tools.MaxTools != nil && *spec.Tools.MaxTools <= 0 {
		add("tools.max_tools: %d must be positive", *spec.Tools.MaxTools)
	}

	if r := spec.Reasoning.BudgetTokens; r != nil {
		if r.Min < 0 {
			add("reasoning.budget_tokens.min: %d cannot be negative", r.Min)
		}
		if r.Min > r.Max {
			add("reasoning.budget_tokens: min %d is greater than max %d", r.Min, r.Max)
		}
	}
	// 推理关不掉就不能在 efforts 里列出 none，否则同一条声明自相矛盾。
	if spec.Reasoning.Mandatory != nil && *spec.Reasoning.Mandatory &&
		slicesContains(spec.Reasoning.Efforts, "none") {
		add("reasoning.efforts: none cannot be listed when mandatory=true")
	}

	if r := spec.Params.Temperature; r != nil {
		validateFloatRange(&out, "params.temperature", *r)
	}
	if r := spec.Params.TopP; r != nil {
		validateFloatRange(&out, "params.top_p", *r)
	}
	if r := spec.Params.TopK; r != nil {
		if r.Min < 0 {
			add("params.top_k.min: %d cannot be negative", r.Min)
		}
		if r.Min > r.Max {
			add("params.top_k: min %d is greater than max %d", r.Min, r.Max)
		}
	}
	if r := spec.Params.Stop; r != nil && r.Max <= 0 {
		add("params.stop: max %d must be positive", r.Max)
	}
	if r := spec.Params.N; r != nil && r.Max <= 0 {
		add("params.n: max %d must be positive", r.Max)
	}

	return out
}

func slicesContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func requireDeclared(out *[]string, dimension string, declared bool) bool {
	if declared {
		return true
	}
	*out = append(*out, dimension+": model-level does not declare this dimension")
	return false
}

func checkModalitySubset(out *[]string, dimension string, base, over ModalitySupport) {
	if over.Supported != nil {
		if requireDeclared(out, dimension+".supported", base.Supported != nil) &&
			*over.Supported && !*base.Supported {
			*out = append(*out, fmt.Sprintf("%s: model-level does not support this modality", dimension))
		}
	}
	checkEnumeratedSubset(out, dimension+".formats", base.Formats, over.Formats)
	checkEnumeratedSubset(out, dimension+".sources", base.Sources, over.Sources)
	if over.MaxCount != nil {
		if requireDeclared(out, dimension+".max_count", base.MaxCount != nil) && *over.MaxCount > *base.MaxCount {
			*out = append(*out, fmt.Sprintf("%s.max_count: %d exceeds model-level %d", dimension, *over.MaxCount, *base.MaxCount))
		}
	}
	if over.MaxSizeMB != nil {
		if requireDeclared(out, dimension+".max_size_mb", base.MaxSizeMB != nil) && *over.MaxSizeMB > *base.MaxSizeMB {
			*out = append(*out, fmt.Sprintf("%s.max_size_mb: %d exceeds model-level %d", dimension, *over.MaxSizeMB, *base.MaxSizeMB))
		}
	}
}

func checkBoolSubset(out *[]string, dimension string, base, over *bool) {
	if over == nil {
		return
	}
	if !requireDeclared(out, dimension, base != nil) {
		return
	}
	if *over && !*base {
		*out = append(*out, dimension+": model-level does not support this, channel cannot declare support")
	}
}

func checkEnumeratedSubset(out *[]string, dimension string, base, over []string) {
	if len(over) == 0 {
		return
	}
	if !requireDeclared(out, dimension, len(base) > 0) {
		return
	}
	allowed := make(map[string]struct{}, len(base))
	for _, value := range base {
		allowed[value] = struct{}{}
	}
	var extra []string
	for _, value := range over {
		if _, ok := allowed[value]; !ok {
			extra = append(extra, value)
		}
	}
	if len(extra) == 0 {
		return
	}
	sort.Strings(extra)
	*out = append(*out, fmt.Sprintf("%s: %s is not in model-level [%s]",
		dimension, strings.Join(extra, ", "), strings.Join(base, ", ")))
}

func checkLimitSubset(out *[]string, dimension string, base, over *Limit) {
	if over == nil {
		return
	}
	if !requireDeclared(out, dimension, base != nil) {
		return
	}
	if over.Max > base.Max {
		*out = append(*out, fmt.Sprintf("%s: max %d exceeds model-level %d", dimension, over.Max, base.Max))
	}
}

func checkFloatRange(out *[]string, dimension string, base, over FloatRange) {
	if over.Min < base.Min || over.Max > base.Max {
		*out = append(*out, fmt.Sprintf("%s: [%s,%s] exceeds model-level [%s,%s]",
			dimension, formatFloat(over.Min), formatFloat(over.Max),
			formatFloat(base.Min), formatFloat(base.Max)))
	}
}

func validateModality(out *[]string, dimension string, m ModalitySupport) {
	if m.MaxCount != nil && *m.MaxCount <= 0 {
		*out = append(*out, fmt.Sprintf("%s.max_count: %d must be positive", dimension, *m.MaxCount))
	}
	if m.MaxSizeMB != nil && *m.MaxSizeMB <= 0 {
		*out = append(*out, fmt.Sprintf("%s.max_size_mb: %d must be positive", dimension, *m.MaxSizeMB))
	}
}

func validateFloatRange(out *[]string, dimension string, r FloatRange) {
	if r.Min > r.Max {
		*out = append(*out, fmt.Sprintf("%s: min %s is greater than max %s", dimension, formatFloat(r.Min), formatFloat(r.Max)))
	}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Accepts 判断一个具体取值是否落在值域里。nil / 空表示未声明，一律放行。
func (r *FloatRange) Accepts(v float64) bool {
	if r == nil {
		return true
	}
	return v >= r.Min && v <= r.Max
}

// Accepts 判断一个具体取值是否落在值域里。
func (r *IntRange) Accepts(v int64) bool {
	if r == nil {
		return true
	}
	return v >= r.Min && v <= r.Max
}

// Accepts 判断数量是否在上限内。Max <= 0 表示未设上限。
func (l *Limit) Accepts(v int) bool {
	if l == nil || l.Max <= 0 {
		return true
	}
	return v <= l.Max
}

// AcceptsCount 判断计数型维度是否在上限内。nil 或非正值表示未设上限。
func AcceptsCount(limit *int, v int) bool {
	return limit == nil || *limit <= 0 || v <= *limit
}

// Allows 判断枚举集合是否包含该取值。空集合表示未枚举，一律放行。
func Allows(values []string, v string) bool {
	if len(values) == 0 {
		return true
	}
	return containsValue(values, v)
}

// containsValue 判断集合是否显式包含该取值；空集合返回 false。
func containsValue(values []string, v string) bool {
	for _, value := range values {
		if value == v {
			return true
		}
	}
	return false
}

// Supports 判断布尔能力是否明确为 true。nil 表示未声明，一律放行。
func Supports(v *bool) bool {
	return v == nil || *v
}

// Truthy 报告布尔能力是否明确为 true；nil 视为 false（展示口径）。
func Truthy(v *bool) bool {
	return v != nil && *v
}

// Summarize 把声明翻译成展示用的能力标签与模态列表。
func Summarize(spec Spec) Summary {
	var capabilities []string
	add := func(name string, on bool) {
		if on {
			capabilities = append(capabilities, name)
		}
	}

	add(SummaryFunctionCalling, Truthy(spec.Tools.Supported))
	add(SummaryStreaming, Truthy(spec.Streaming.Supported))
	add(SummaryVision, Truthy(spec.Modalities.Input.Image.Supported))
	add(SummaryJSONMode, Truthy(spec.Structured.JSONMode))
	add(SummaryStructuredOut, Truthy(spec.Structured.JSONSchema))
	add(SummaryReasoning, Truthy(spec.Reasoning.Supported))
	add(SummaryWebSearch, containsValue(spec.Tools.Builtin, "web_search"))
	add(SummaryCodeExec, containsValue(spec.Tools.Builtin, "code_execution") || containsValue(spec.Tools.Builtin, "code_interpreter"))
	add(SummarySystemPrompt, spec.Messages.SystemRole != "" && spec.Messages.SystemRole != "none")
	sort.Strings(capabilities)

	var inputs []string
	if Truthy(spec.Modalities.Input.Text) {
		inputs = append(inputs, "text")
	}
	for _, modality := range []struct {
		name string
		spec ModalitySupport
	}{
		{"image", spec.Modalities.Input.Image},
		{"audio", spec.Modalities.Input.Audio},
		{"video", spec.Modalities.Input.Video},
		{"file", spec.Modalities.Input.File},
	} {
		if Truthy(modality.spec.Supported) {
			inputs = append(inputs, modality.name)
		}
	}

	outputs := append([]string{}, spec.Modalities.Output...)
	if len(outputs) == 0 && Truthy(spec.Modalities.Input.Text) {
		outputs = []string{"text"}
	}
	sort.Strings(outputs)

	return Summary{
		Capabilities:     capabilities,
		InputModalities:  inputs,
		OutputModalities: outputs,
		MaxOutputTokens:  spec.Context.MaxOutputTokens,
	}
}

// Summary 是模型广场直接消费的能力摘要。
type Summary struct {
	Capabilities     []string `json:"capabilities,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
	MaxOutputTokens  *int64   `json:"max_output_tokens,omitempty"`
}

// 能力标签沿用前端 PricingModel.capabilities 已有的词汇。
const (
	SummaryFunctionCalling = "function_calling"
	SummaryStreaming       = "streaming"
	SummaryVision          = "vision"
	SummaryJSONMode        = "json_mode"
	SummaryStructuredOut   = "structured_output"
	SummaryReasoning       = "reasoning"
	SummaryWebSearch       = "web_search"
	SummaryCodeExec        = "code_interpreter"
	SummarySystemPrompt    = "system_prompt"
)
