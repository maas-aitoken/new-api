package capability

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Format 是入站协议。
type Format string

const (
	FormatAuto            Format = "auto"
	FormatOpenAIChat      Format = "openai_chat"
	FormatOpenAIResponses Format = "openai_responses"
	FormatAnthropic       Format = "anthropic"
	FormatGemini          Format = "gemini"
)

// Valid 报告 format 是否是服务认识的值。
func (f Format) Valid() bool {
	switch f {
	case "", FormatAuto, FormatOpenAIChat, FormatOpenAIResponses, FormatAnthropic, FormatGemini:
		return true
	default:
		return false
	}
}

// Needs 是一次请求对硬约束能力的全部诉求。零值表示"请求没提这个维度"。
type Needs struct {
	Stream          bool     `json:"stream"`
	MaxOutputTokens *int64   `json:"max_output_tokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	// EstimatedInputTokens 不由 Extract 填写：它取决于候选模型的 tokenizer 家族，
	// 由门控逐个候选模型估算后填进副本再判定（见 service.RoutingInputTokens）。
	EstimatedInputTokens int64 `json:"estimated_input_tokens"`

	// 采样参数。值是客户端显式给出的值；nil 表示请求没提这个参数，
	// StopCount 为 0 表示请求没带停止序列。
	TopP             *float64 `json:"top_p,omitempty"`
	TopK             *int64   `json:"top_k,omitempty"`
	StopCount        int      `json:"stop_count,omitempty"`
	Completions      *int     `json:"completions,omitempty"`
	Seed             *int64   `json:"seed,omitempty"`
	Logprobs         *bool    `json:"logprobs,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`

	InputModalities []string `json:"input_modalities,omitempty"` // text | image | audio | video | file
	Mimes           []string `json:"mimes,omitempty"`
	Sources         []string `json:"sources,omitempty"` // url | base64 | file_id
	// MediaCounts 与 MediaMaxSizeMB 按模态记录，用于对齐 max_count / max_size_mb。
	MediaCounts    map[string]int `json:"media_counts,omitempty"`
	MediaMaxSizeMB map[string]int `json:"media_max_size_mb,omitempty"`

	ToolsCount        int      `json:"tools_count"`
	ToolChoiceMode    string   `json:"tool_choice_mode,omitempty"` // auto | none | required | named
	ParallelToolCalls *bool    `json:"parallel_tool_calls,omitempty"`
	BuiltinTools      []string `json:"builtin_tools,omitempty"`

	JSONMode   bool `json:"json_mode"`
	JSONSchema bool `json:"json_schema"`

	ReasoningEnabled *bool  `json:"reasoning_enabled,omitempty"`
	ReasoningEffort  string `json:"reasoning_effort,omitempty"`
	ReasoningBudget  *int64 `json:"reasoning_budget,omitempty"`

	AssistantPrefill bool `json:"assistant_prefill"`
	SystemMessages   int  `json:"system_messages"`
}

// Extract 按 format 解析 body。format 为空或 auto 时用顶层键嗅探。
func Extract(format Format, body []byte) (*Needs, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("request body is empty")
	}
	if !format.Valid() {
		return nil, fmt.Errorf("unknown request format %q", format)
	}
	if format == "" || format == FormatAuto {
		detected, err := sniff(body)
		if err != nil {
			return nil, err
		}
		format = detected
	}
	collector := newCollector()
	switch format {
	case FormatOpenAIChat:
		if err := parseChat(collector, body); err != nil {
			return nil, err
		}
	case FormatOpenAIResponses:
		if err := parseResponses(collector, body); err != nil {
			return nil, err
		}
	case FormatAnthropic:
		if err := parseAnthropic(collector, body); err != nil {
			return nil, err
		}
	case FormatGemini:
		if err := parseGemini(collector, body); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown request format %q", format)
	}
	return collector.finish(), nil
}

// sniff 只靠顶层键判断协议。Anthropic 与 Chat 都带 messages，
// 因此先看 Anthropic 独有的 anthropic_version / system / thinking / output_format。
func sniff(body []byte) (Format, error) {
	var top map[string]common.RawMessage
	if err := common.Unmarshal(body, &top); err != nil {
		return "", fmt.Errorf("request body is not valid JSON: %w", err)
	}
	if _, ok := top["contents"]; ok {
		return FormatGemini, nil
	}
	if _, ok := top["input"]; ok {
		return FormatOpenAIResponses, nil
	}
	if _, ok := top["anthropic_version"]; ok {
		return FormatAnthropic, nil
	}
	if _, ok := top["system"]; ok {
		return FormatAnthropic, nil
	}
	if _, ok := top["thinking"]; ok {
		return FormatAnthropic, nil
	}
	if _, ok := top["output_format"]; ok {
		return FormatAnthropic, nil
	}
	if _, ok := top["messages"]; ok {
		return FormatOpenAIChat, nil
	}
	return "", fmt.Errorf("unable to detect request format: no messages, input or contents")
}

// collector 边解析边去重。
type collector struct {
	needs      Needs
	modalities map[string]struct{}
	mimes      map[string]struct{}
	sources    map[string]struct{}
	builtin    map[string]struct{}
	counts     map[string]int
	maxSizeMB  map[string]int
}

func newCollector() *collector {
	return &collector{
		modalities: map[string]struct{}{},
		mimes:      map[string]struct{}{},
		sources:    map[string]struct{}{},
		builtin:    map[string]struct{}{},
		counts:     map[string]int{},
		maxSizeMB:  map[string]int{},
	}
}

func (c *collector) modality(name string) {
	if name == "" {
		return
	}
	c.modalities[name] = struct{}{}
}

// media 登记一个媒体项。sizeBytes 为 0 表示体积未知（例如只给了 URL）。
func (c *collector) media(modality, mime, source string, sizeBytes int64) {
	c.modality(modality)
	if modality != "" {
		c.counts[modality]++
	}
	if mime != "" {
		c.mimes[mime] = struct{}{}
	}
	if source != "" {
		c.sources[source] = struct{}{}
	}
	if sizeBytes > 0 && modality != "" {
		// 向上取整到 MB：判定的目的是"放不放得下"，宁可高估。
		mb := int((sizeBytes + (1 << 20) - 1) >> 20)
		if mb > c.maxSizeMB[modality] {
			c.maxSizeMB[modality] = mb
		}
	}
}

func (c *collector) builtinTool(name string) {
	if name == "" {
		return
	}
	c.builtin[name] = struct{}{}
}

// maxOutput 记录客户端要求的输出上限。同时出现多个字段时取最大值。
func (c *collector) maxOutput(v *int64) {
	if v == nil || *v <= 0 {
		return
	}
	if c.needs.MaxOutputTokens == nil || *v > *c.needs.MaxOutputTokens {
		c.needs.MaxOutputTokens = v
	}
}

func (c *collector) finish() *Needs {
	n := c.needs
	if len(c.modalities) > 0 {
		n.InputModalities = sortedKeys(c.modalities)
	}
	if len(c.mimes) > 0 {
		n.Mimes = sortedKeys(c.mimes)
	}
	if len(c.sources) > 0 {
		n.Sources = sortedKeys(c.sources)
	}
	if len(c.builtin) > 0 {
		n.BuiltinTools = sortedKeys(c.builtin)
	}
	if len(c.counts) > 0 {
		n.MediaCounts = c.counts
	}
	if len(c.maxSizeMB) > 0 {
		n.MediaMaxSizeMB = c.maxSizeMB
	}
	return &n
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mediaFromURL 从 URL 里判断 MIME 与传入方式。
func mediaFromURL(raw string) (mime, source string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.HasPrefix(raw, "data:") {
		rest := raw[len("data:"):]
		header := rest
		if idx := strings.IndexByte(rest, ','); idx >= 0 {
			header = rest[:idx]
		}
		if idx := strings.IndexByte(header, ';'); idx >= 0 {
			header = header[:idx]
		}
		return strings.TrimSpace(header), "base64"
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return "", "url"
	}
	return "", ""
}

// jsonStringOrObject 处理既可能是字符串、也可能是 {"url": "..."} 的字段。
func jsonStringOrObject(raw common.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := common.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		URL string `json:"url"`
	}
	if err := common.Unmarshal(raw, &obj); err == nil {
		return obj.URL
	}
	return ""
}

func isJSONNull(raw common.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

// stopSequenceCount 统计请求带的停止序列条数；OpenAI 允许单字符串或字符串数组。
func stopSequenceCount(raw common.RawMessage) int {
	if isJSONNull(raw) {
		return 0
	}
	var values []string
	if err := common.Unmarshal(raw, &values); err == nil {
		return len(values)
	}
	var single string
	if err := common.Unmarshal(raw, &single); err == nil && single != "" {
		return 1
	}
	return 0
}

// mediaURL 从 URL 形态的媒体引用里推断 MIME、传入方式与体积。
func (c *collector) mediaURL(modality, raw string) {
	mime, source := mediaFromURL(raw)
	c.media(modality, mime, source, dataURLSize(raw))
}

// base64Bytes 估算 base64 载荷解码后的字节数。只看长度，不做真正解码。
func base64Bytes(payload string) int64 {
	trimmed := strings.TrimRight(strings.TrimSpace(payload), "=")
	return int64(len(trimmed)) * 3 / 4
}

// dataURLSize 返回 data URL 解码后的字节数；不是 data URL 则返回 0。
func dataURLSize(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "data:") {
		return 0
	}
	idx := strings.IndexByte(raw, ',')
	if idx < 0 {
		return 0
	}
	return base64Bytes(raw[idx+1:])
}

// ----------------------------------------------------------------------------
// OpenAI Chat
// ----------------------------------------------------------------------------

type chatRequest struct {
	Stream              *bool               `json:"stream"`
	MaxTokens           *int64              `json:"max_tokens"`
	MaxCompletionTokens *int64              `json:"max_completion_tokens"`
	Temperature         *float64            `json:"temperature"`
	TopP                *float64            `json:"top_p"`
	TopK                *int64              `json:"top_k"`
	Stop                common.RawMessage   `json:"stop"`
	N                   *int                `json:"n"`
	Seed                *int64              `json:"seed"`
	Logprobs            *bool               `json:"logprobs"`
	PresencePenalty     *float64            `json:"presence_penalty"`
	FrequencyPenalty    *float64            `json:"frequency_penalty"`
	ParallelToolCalls   *bool               `json:"parallel_tool_calls"`
	Tools               []chatTool          `json:"tools"`
	Functions           []common.RawMessage `json:"functions"`
	ToolChoice          common.RawMessage   `json:"tool_choice"`
	ResponseFormat      *struct {
		Type string `json:"type"`
	} `json:"response_format"`
	ReasoningEffort string `json:"reasoning_effort"`
	Reasoning       *struct {
		Effort  string `json:"effort"`
		Enabled *bool  `json:"enabled"`
	} `json:"reasoning"`
	Messages []chatMessage `json:"messages"`
}

type chatTool struct {
	Type string `json:"type"`
}

type chatMessage struct {
	Role    string            `json:"role"`
	Content common.RawMessage `json:"content"`
}

type chatPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
	InputAudio common.RawMessage `json:"input_audio"`
	File       *struct {
		FileData string `json:"file_data"`
		FileID   string `json:"file_id"`
		Filename string `json:"filename"`
	} `json:"file"`
	VideoURL *struct {
		URL string `json:"url"`
	} `json:"video_url"`
}

func parseChat(c *collector, body []byte) error {
	var req chatRequest
	if err := common.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("failed to parse OpenAI Chat request: %w", err)
	}

	if req.Stream != nil {
		c.needs.Stream = *req.Stream
	}
	c.maxOutput(req.MaxTokens)
	c.maxOutput(req.MaxCompletionTokens)
	c.needs.Temperature = req.Temperature
	c.needs.TopP = req.TopP
	c.needs.TopK = req.TopK
	c.needs.StopCount = stopSequenceCount(req.Stop)
	c.needs.Completions = req.N
	c.needs.Seed = req.Seed
	c.needs.Logprobs = req.Logprobs
	c.needs.PresencePenalty = req.PresencePenalty
	c.needs.FrequencyPenalty = req.FrequencyPenalty
	c.needs.ParallelToolCalls = req.ParallelToolCalls

	for _, tool := range req.Tools {
		// type 为空或 function 是自定义函数；其余是供应商内置工具。
		if tool.Type == "" || tool.Type == "function" {
			c.needs.ToolsCount++
			continue
		}
		c.builtinTool(tool.Type)
	}
	c.needs.ToolsCount += len(req.Functions)
	c.needs.ToolChoiceMode = chatToolChoiceMode(req.ToolChoice)

	if req.ResponseFormat != nil {
		switch req.ResponseFormat.Type {
		case "json_object":
			c.needs.JSONMode = true
		case "json_schema":
			c.needs.JSONSchema = true
		}
	}

	effort := req.ReasoningEffort
	enabled := (*bool)(nil)
	if req.Reasoning != nil {
		if effort == "" {
			effort = req.Reasoning.Effort
		}
		enabled = req.Reasoning.Enabled
	}
	if effort != "" {
		c.needs.ReasoningEffort = effort
		if enabled == nil {
			on := effort != "none"
			enabled = &on
		}
	}
	c.needs.ReasoningEnabled = enabled

	for _, message := range req.Messages {
		switch message.Role {
		case "system", "developer":
			c.needs.SystemMessages++
		}
		if len(message.Content) == 0 || isJSONNull(message.Content) {
			continue
		}
		var text string
		if err := common.Unmarshal(message.Content, &text); err == nil {
			c.modality("text")
			continue
		}
		var parts []chatPart
		if err := common.Unmarshal(message.Content, &parts); err != nil {
			continue
		}
		for _, part := range parts {
			parseChatPart(c, part)
		}
	}
	// 预填：最后一条是 assistant，表示客户端提供了一个待续写的开头。
	if n := len(req.Messages); n > 0 && req.Messages[n-1].Role == "assistant" {
		c.needs.AssistantPrefill = true
	}
	return nil
}

func parseChatPart(c *collector, part chatPart) {
	switch part.Type {
	case "text":
		c.modality("text")
	case "image_url":
		if part.ImageURL != nil {
			c.mediaURL("image", part.ImageURL.URL)
			return
		}
		c.media("image", "", "", 0)
	case "input_audio":
		var audio struct {
			Data string `json:"data"`
		}
		size := int64(0)
		if len(part.InputAudio) > 0 && common.Unmarshal(part.InputAudio, &audio) == nil {
			size = base64Bytes(audio.Data)
		}
		c.media("audio", "", "", size)
	case "video_url":
		if part.VideoURL != nil {
			c.mediaURL("video", part.VideoURL.URL)
			return
		}
		c.media("video", "", "", 0)
	case "file":
		if part.File == nil {
			c.media("file", "", "", 0)
			return
		}
		switch {
		case part.File.FileData != "":
			c.mediaURL("file", part.File.FileData)
		case part.File.FileID != "":
			c.media("file", "", "file_id", 0)
		default:
			c.media("file", "", "", 0)
		}
	}
}

// chatToolChoiceMode 把 tool_choice 归一成 auto / none / required / named。
func chatToolChoiceMode(raw common.RawMessage) string {
	if isJSONNull(raw) {
		return ""
	}
	var mode string
	if err := common.Unmarshal(raw, &mode); err == nil {
		switch mode {
		case "auto", "none", "required":
			return mode
		default:
			return ""
		}
	}
	var obj struct {
		Type string `json:"type"`
	}
	if err := common.Unmarshal(raw, &obj); err == nil {
		switch obj.Type {
		case "function", "custom", "allowed_tools":
			return "named"
		}
	}
	return ""
}

// ----------------------------------------------------------------------------
// OpenAI Responses
// ----------------------------------------------------------------------------

type responsesRequest struct {
	Stream            *bool               `json:"stream"`
	MaxOutputTokens   *int64              `json:"max_output_tokens"`
	Temperature       *float64            `json:"temperature"`
	TopP              *float64            `json:"top_p"`
	ParallelToolCalls *bool               `json:"parallel_tool_calls"`
	Instructions      *string             `json:"instructions"`
	Tools             []responsesTool     `json:"tools"`
	ToolChoice        common.RawMessage   `json:"tool_choice"`
	Reasoning         *responsesReasoning `json:"reasoning"`
	Text              *struct {
		Format *struct {
			Type string `json:"type"`
		} `json:"format"`
	} `json:"text"`
	Input common.RawMessage `json:"input"`
}

type responsesReasoning struct {
	Effort string `json:"effort"`
}

type responsesTool struct {
	Type string `json:"type"`
}

type responsesItem struct {
	Type    string            `json:"type"`
	Role    string            `json:"role"`
	Content common.RawMessage `json:"content"`
}

type responsesPart struct {
	Type     string            `json:"type"`
	Text     string            `json:"text"`
	ImageURL common.RawMessage `json:"image_url"`
	FileID   string            `json:"file_id"`
	FileData string            `json:"file_data"`
	FileURL  string            `json:"file_url"`
}

func parseResponses(c *collector, body []byte) error {
	var req responsesRequest
	if err := common.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("failed to parse OpenAI Responses request: %w", err)
	}

	if req.Stream != nil {
		c.needs.Stream = *req.Stream
	}
	c.maxOutput(req.MaxOutputTokens)
	c.needs.Temperature = req.Temperature
	c.needs.TopP = req.TopP
	c.needs.ParallelToolCalls = req.ParallelToolCalls

	if req.Instructions != nil && *req.Instructions != "" {
		c.needs.SystemMessages++
	}

	for _, tool := range req.Tools {
		if tool.Type == "" || tool.Type == "function" || tool.Type == "custom" {
			c.needs.ToolsCount++
			continue
		}
		c.builtinTool(tool.Type)
	}
	c.needs.ToolChoiceMode = chatToolChoiceMode(req.ToolChoice)

	if req.Text != nil && req.Text.Format != nil {
		switch req.Text.Format.Type {
		case "json_object":
			c.needs.JSONMode = true
		case "json_schema":
			c.needs.JSONSchema = true
		}
	}

	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		c.needs.ReasoningEffort = req.Reasoning.Effort
		on := req.Reasoning.Effort != "none"
		c.needs.ReasoningEnabled = &on
	}

	if isJSONNull(req.Input) {
		return nil
	}
	var text string
	if err := common.Unmarshal(req.Input, &text); err == nil {
		c.modality("text")
		return nil
	}
	var items []responsesItem
	if err := common.Unmarshal(req.Input, &items); err != nil {
		return nil
	}
	for _, item := range items {
		if item.Role != "" {
			switch item.Role {
			case "system", "developer":
				c.needs.SystemMessages++
			}
		}
		if len(item.Content) == 0 || isJSONNull(item.Content) {
			continue
		}
		var text string
		if err := common.Unmarshal(item.Content, &text); err == nil {
			c.modality("text")
			continue
		}
		var parts []responsesPart
		if err := common.Unmarshal(item.Content, &parts); err != nil {
			continue
		}
		for _, part := range parts {
			parseResponsesPart(c, part)
		}
	}
	// 预填：最后一条是 assistant 消息。
	if n := len(items); n > 0 && items[n-1].Type == "message" && items[n-1].Role == "assistant" {
		c.needs.AssistantPrefill = true
	}
	return nil
}

func parseResponsesPart(c *collector, part responsesPart) {
	switch part.Type {
	case "input_text", "output_text", "text":
		c.modality("text")
	case "input_image":
		if part.FileID != "" {
			c.media("image", "", "file_id", 0)
			return
		}
		c.mediaURL("image", jsonStringOrObject(part.ImageURL))
	case "input_file":
		switch {
		case part.FileData != "":
			c.mediaURL("file", part.FileData)
		case part.FileURL != "":
			c.media("file", "", "url", 0)
		case part.FileID != "":
			c.media("file", "", "file_id", 0)
		default:
			c.media("file", "", "", 0)
		}
	case "input_audio":
		c.media("audio", "", "", 0)
	case "input_video":
		c.media("video", "", "", 0)
	}
}

// ----------------------------------------------------------------------------
// Anthropic Messages
// ----------------------------------------------------------------------------

type claudeRequest struct {
	Stream        *bool             `json:"stream"`
	MaxTokens     *int64            `json:"max_tokens"`
	Temperature   *float64          `json:"temperature"`
	TopP          *float64          `json:"top_p"`
	TopK          *int64            `json:"top_k"`
	StopSequences []string          `json:"stop_sequences"`
	System        common.RawMessage `json:"system"`
	Messages      []claudeMessage   `json:"messages"`
	Tools         []claudeTool      `json:"tools"`
	ToolChoice    *claudeToolChoice `json:"tool_choice"`
	Thinking      *claudeThinking   `json:"thinking"`
	OutputConfig  *struct {
		Effort string `json:"effort"`
	} `json:"output_config"`
	OutputFormat common.RawMessage `json:"output_format"`
}

type claudeToolChoice struct {
	Type                   string `json:"type"`
	DisableParallelToolUse bool   `json:"disable_parallel_tool_use"`
}

type claudeThinking struct {
	Type         string `json:"type"`
	BudgetTokens *int64 `json:"budget_tokens"`
}

type claudeTool struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type claudeMessage struct {
	Role    string            `json:"role"`
	Content common.RawMessage `json:"content"`
}

type claudeBlock struct {
	Type    string            `json:"type"`
	Text    string            `json:"text"`
	Source  *claudeSource     `json:"source"`
	Content common.RawMessage `json:"content"` // tool_result 的内层内容
}

type claudeSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	URL       string `json:"url"`
	Data      string `json:"data"`
}

func parseAnthropic(c *collector, body []byte) error {
	var req claudeRequest
	if err := common.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("failed to parse Anthropic Messages request: %w", err)
	}

	if req.Stream != nil {
		c.needs.Stream = *req.Stream
	}
	c.maxOutput(req.MaxTokens)
	c.needs.Temperature = req.Temperature
	c.needs.TopP = req.TopP
	c.needs.TopK = req.TopK
	c.needs.StopCount = len(req.StopSequences)

	if !isJSONNull(req.System) {
		c.needs.SystemMessages++
		collectClaudeSystem(c, req.System)
	}

	for _, tool := range req.Tools {
		// 带 type 的是供应商托管工具，没有 type 的是自定义函数。
		if tool.Type != "" {
			c.builtinTool(tool.Name)
			continue
		}
		c.needs.ToolsCount++
	}

	if req.ToolChoice != nil {
		switch req.ToolChoice.Type {
		case "auto":
			c.needs.ToolChoiceMode = "auto"
		case "any":
			c.needs.ToolChoiceMode = "required"
		case "tool":
			c.needs.ToolChoiceMode = "named"
		case "none":
			c.needs.ToolChoiceMode = "none"
		}
		if req.ToolChoice.DisableParallelToolUse {
			off := false
			c.needs.ParallelToolCalls = &off
		}
	}

	if req.Thinking != nil {
		switch req.Thinking.Type {
		case "enabled", "adaptive":
			on := true
			c.needs.ReasoningEnabled = &on
			c.needs.ReasoningBudget = req.Thinking.BudgetTokens
		case "disabled":
			off := false
			c.needs.ReasoningEnabled = &off
		}
	}
	if req.OutputConfig != nil && req.OutputConfig.Effort != "" {
		c.needs.ReasoningEffort = req.OutputConfig.Effort
		if c.needs.ReasoningEnabled == nil {
			on := true
			c.needs.ReasoningEnabled = &on
		}
	}
	if !isJSONNull(req.OutputFormat) {
		c.needs.JSONSchema = true
	}

	for _, message := range req.Messages {
		if len(message.Content) == 0 || isJSONNull(message.Content) {
			continue
		}
		var text string
		if err := common.Unmarshal(message.Content, &text); err == nil {
			c.modality("text")
			continue
		}
		var blocks []claudeBlock
		if err := common.Unmarshal(message.Content, &blocks); err != nil {
			continue
		}
		for _, block := range blocks {
			parseClaudeBlock(c, block)
		}
	}
	// 预填：最后一条是 assistant。
	if n := len(req.Messages); n > 0 && req.Messages[n-1].Role == "assistant" {
		c.needs.AssistantPrefill = true
	}
	return nil
}

// collectClaudeSystem 的 system 既可能是字符串，也可能是文本块数组。
func collectClaudeSystem(c *collector, raw common.RawMessage) {
	var text string
	if err := common.Unmarshal(raw, &text); err == nil {
		c.modality("text")
		return
	}
	var blocks []claudeBlock
	if err := common.Unmarshal(raw, &blocks); err != nil {
		return
	}
	for _, block := range blocks {
		if block.Type == "text" {
			c.modality("text")
		}
	}
}

func parseClaudeBlock(c *collector, block claudeBlock) {
	switch block.Type {
	case "text":
		c.modality("text")
	case "image":
		c.claudeSourceMedia("image", block.Source)
	case "document":
		if block.Source != nil && block.Source.Type == "text" {
			c.modality("text")
			return
		}
		c.claudeSourceMedia("file", block.Source)
	case "tool_result":
		if isJSONNull(block.Content) {
			return
		}
		var text string
		if err := common.Unmarshal(block.Content, &text); err == nil {
			c.modality("text")
			return
		}
		var blocks []claudeBlock
		if err := common.Unmarshal(block.Content, &blocks); err != nil {
			return
		}
		for _, inner := range blocks {
			switch inner.Type {
			case "text":
				c.modality("text")
			case "image":
				c.claudeSourceMedia("image", inner.Source)
			}
		}
	case "thinking", "redacted_thinking":
		on := true
		if c.needs.ReasoningEnabled == nil {
			c.needs.ReasoningEnabled = &on
		}
	}
}

func (c *collector) claudeSourceMedia(modality string, source *claudeSource) {
	if source == nil {
		c.media(modality, "", "", 0)
		return
	}
	switch source.Type {
	case "base64":
		c.media(modality, source.MediaType, "base64", base64Bytes(source.Data))
	case "url":
		c.media(modality, source.MediaType, "url", 0)
	case "file":
		c.media(modality, source.MediaType, "file_id", 0)
	default:
		c.media(modality, source.MediaType, "", 0)
	}
}

// ----------------------------------------------------------------------------
// Gemini generateContent
// ----------------------------------------------------------------------------

type geminiRequest struct {
	Contents          []geminiContent                `json:"contents"`
	SystemInstruction common.RawMessage              `json:"systemInstruction"`
	GenerationConfig  *geminiGeneration              `json:"generationConfig"`
	Tools             []map[string]common.RawMessage `json:"tools"`
	ToolConfig        *struct {
		FunctionCallingConfig *struct {
			Mode                 string   `json:"mode"`
			AllowedFunctionNames []string `json:"allowedFunctionNames"`
		} `json:"functionCallingConfig"`
	} `json:"toolConfig"`
}

type geminiGeneration struct {
	MaxOutputTokens    *int64            `json:"maxOutputTokens"`
	Temperature        *float64          `json:"temperature"`
	TopP               *float64          `json:"topP"`
	TopK               *int64            `json:"topK"`
	StopSequences      []string          `json:"stopSequences"`
	CandidateCount     *int              `json:"candidateCount"`
	Seed               *int64            `json:"seed"`
	PresencePenalty    *float64          `json:"presencePenalty"`
	FrequencyPenalty   *float64          `json:"frequencyPenalty"`
	ResponseMimeType   string            `json:"responseMimeType"`
	ResponseSchema     common.RawMessage `json:"responseSchema"`
	ResponseJsonSchema common.RawMessage `json:"responseJsonSchema"`
	ThinkingConfig     *struct {
		ThinkingBudget  *int64 `json:"thinkingBudget"`
		ThinkingLevel   string `json:"thinkingLevel"`
		IncludeThoughts *bool  `json:"includeThoughts"`
	} `json:"thinkingConfig"`
}

type geminiContent struct {
	Role  string              `json:"role"`
	Parts []common.RawMessage `json:"parts"`
}

type geminiPart struct {
	Text       *string `json:"text"`
	InlineData *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
	FileData *struct {
		MimeType string `json:"mimeType"`
		FileURI  string `json:"fileUri"`
	} `json:"fileData"`
	FunctionCall     common.RawMessage `json:"functionCall"`
	FunctionResponse common.RawMessage `json:"functionResponse"`
}

func parseGemini(c *collector, body []byte) error {
	var req geminiRequest
	if err := common.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("failed to parse Gemini generateContent request: %w", err)
	}

	if !isJSONNull(req.SystemInstruction) {
		c.needs.SystemMessages++
		collectGeminiParts(c, req.SystemInstruction)
	}

	if gen := req.GenerationConfig; gen != nil {
		c.maxOutput(gen.MaxOutputTokens)
		c.needs.Temperature = gen.Temperature
		c.needs.TopP = gen.TopP
		c.needs.TopK = gen.TopK
		c.needs.StopCount = len(gen.StopSequences)
		c.needs.Completions = gen.CandidateCount
		c.needs.Seed = gen.Seed
		c.needs.PresencePenalty = gen.PresencePenalty
		c.needs.FrequencyPenalty = gen.FrequencyPenalty
		switch gen.ResponseMimeType {
		case "application/json":
			if isJSONNull(gen.ResponseSchema) && isJSONNull(gen.ResponseJsonSchema) {
				c.needs.JSONMode = true
			} else {
				c.needs.JSONSchema = true
			}
		}
		if gen.ThinkingConfig != nil {
			switch {
			case gen.ThinkingConfig.ThinkingBudget != nil:
				if *gen.ThinkingConfig.ThinkingBudget > 0 {
					c.needs.ReasoningBudget = gen.ThinkingConfig.ThinkingBudget
					on := true
					c.needs.ReasoningEnabled = &on
				} else {
					off := false
					c.needs.ReasoningEnabled = &off
				}
			case gen.ThinkingConfig.ThinkingLevel != "":
				c.needs.ReasoningEffort = normalizeGeminiLevel(gen.ThinkingConfig.ThinkingLevel)
				on := gen.ThinkingConfig.ThinkingLevel != "NONE"
				c.needs.ReasoningEnabled = &on
			}
		}
	}

	for _, tool := range req.Tools {
		for key, value := range tool {
			if key == "functionDeclarations" {
				var declarations []common.RawMessage
				if err := common.Unmarshal(value, &declarations); err == nil {
					c.needs.ToolsCount += len(declarations)
				}
				continue
			}
			c.builtinTool(geminiBuiltinName(key))
		}
	}

	if req.ToolConfig != nil && req.ToolConfig.FunctionCallingConfig != nil {
		config := req.ToolConfig.FunctionCallingConfig
		switch config.Mode {
		case "AUTO":
			c.needs.ToolChoiceMode = "auto"
		case "NONE":
			c.needs.ToolChoiceMode = "none"
		case "ANY":
			if len(config.AllowedFunctionNames) > 0 {
				c.needs.ToolChoiceMode = "named"
			} else {
				c.needs.ToolChoiceMode = "required"
			}
		}
	}

	for i, content := range req.Contents {
		for _, raw := range content.Parts {
			parseGeminiPart(c, raw)
		}
		if i == len(req.Contents)-1 && content.Role == "model" {
			c.needs.AssistantPrefill = true
		}
	}
	return nil
}

func collectGeminiParts(c *collector, raw common.RawMessage) {
	var instruction struct {
		Parts []common.RawMessage `json:"parts"`
	}
	if err := common.Unmarshal(raw, &instruction); err != nil {
		return
	}
	for _, part := range instruction.Parts {
		parseGeminiPart(c, part)
	}
}

func parseGeminiPart(c *collector, raw common.RawMessage) {
	var part geminiPart
	if err := common.Unmarshal(raw, &part); err != nil {
		return
	}
	if part.Text != nil {
		c.modality("text")
	}
	if part.InlineData != nil {
		m := modalityFromMime(part.InlineData.MimeType)
		c.media(m, part.InlineData.MimeType, "base64", base64Bytes(part.InlineData.Data))
	}
	if part.FileData != nil {
		m := modalityFromMime(part.FileData.MimeType)
		c.media(m, part.FileData.MimeType, "file_id", 0)
	}
}

// ModalityFromMime 把 MIME 归到五种输入模态之一，供判定侧复用。
func ModalityFromMime(mime string) string {
	return modalityFromMime(mime)
}

func modalityFromMime(mime string) string {
	switch {
	case mime == "":
		return "file"
	case len(mime) >= 6 && mime[:6] == "image/":
		return "image"
	case len(mime) >= 6 && mime[:6] == "audio/":
		return "audio"
	case len(mime) >= 6 && mime[:6] == "video/":
		return "video"
	default:
		return "file"
	}
}

func geminiBuiltinName(key string) string {
	switch key {
	case "googleSearch", "googleSearchRetrieval":
		return "web_search"
	case "codeExecution":
		return "code_execution"
	case "urlContext":
		return "url_context"
	default:
		return key
	}
}

func normalizeGeminiLevel(level string) string {
	switch level {
	case "NONE":
		return "none"
	case "LOW":
		return "low"
	case "MEDIUM":
		return "medium"
	case "HIGH":
		return "high"
	default:
		return ""
	}
}
