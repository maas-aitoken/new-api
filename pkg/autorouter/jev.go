package autorouter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// QuestionsVersion changes whenever the question set or state shape changes,
// so results from different templates are never compared or cached together.
// v2: state carries the previous human instruction next to the current one.
// v3: state is an object with separate current_task/previous_task fields.
// v4: task types are <class>.<leaf> under the code/data/general classes.
// v5: general.search covers live fact lookups apart from general.research.
// v6: the mode question judges whether the request weighs capability or cost.
const QuestionsVersion = "autorouter_v6"

// TaxonomyVersion changes whenever the task types change, so a stored ranking
// keyed by older task types is never used for the new ones.
const TaxonomyVersion = "3"

// TaskTypeUnknown means the instruction cannot be classified on its own
// ("继续", "ok", a choice among options from earlier context).
const TaskTypeUnknown = "unknown"

// taskTypeCriteria are the leaf task types jev chooses among. A task type is
// <class>.<leaf>; the class is not asked, its probability is the sum of its
// leaves'. Each description follows how the leaderboards behind the task
// type describe the tasks they measure.
var taskTypeCriteria = map[string]string{
	"code.build":        "编写或修改程序逻辑与配置：实现功能、按问题报告修复缺陷、审查代码，保证改动后程序行为正确",
	"code.frontend":     "按需求开发或修改用户界面：网页、App、小程序、桌面客户端的页面、UI 组件、样式、交互与渲染性能，以用户看到的外观与操作体验为准",
	"code.ops":          "在终端或服务器上执行命令，完成部署、环境配置、服务运维与线上故障排查",
	"data.analysis":     "对给定的表格或数据求出确定的答案：统计、筛选、去重、关联、识别趋势，或在 CSV、JSON、Markdown 等格式间转换",
	"data.business":     "运用业务与管理知识做判断：分析经营或运营数据、比较方案、制定计划，给出可执行的商业决策建议",
	"general.search":    "联网查询一个具体问题的当前答案：天气、新闻、价格、版本发布、某个事实或链接，简短作答并给出出处",
	"general.research":  "围绕一个开放主题广泛检索多方资料，比较、核实并综合，产出带引用的调研结论或报告",
	"general.browser":   "在网页应用里逐步点击、填写、核对，完成跨多个页面的操作任务，如汇总多页记录、批量修改订单、合并重复工单",
	"general.writing":   "产出或加工文字内容：撰写文案、文章、邮件、故事、诗歌等，或对给定文本总结、改写、润色、翻译",
	"general.design":    "制作或改版成品文件：Word 文档、PDF、幻灯片的版式、排版、配色、图表与品牌样式",
	"general.knowledge": "解释概念、原理或某领域的专业事实，说明某件事是什么、为什么或如何运作",
	"general.chat":      "日常对话与生活类求助：寒暄、闲聊、家常做法、个人化的推荐或经验分享",
	TaskTypeUnknown:     "离开前文完全无法判断要做什么：如“继续”“ok”“好的”、对前文方案的确认或选择、只用“这个”“上面”指代前文且没有说明动作",
}

// TaskClass returns the class of a task type: "code" for "code.build", empty
// for unknown.
func TaskClass(taskType string) string {
	class, _, found := strings.Cut(taskType, ".")
	if !found {
		return ""
	}
	return class
}

// TaskTypes lists the leaf task types in sorted order, without unknown.
func TaskTypes() []string {
	var types []string
	for taskType := range taskTypeCriteria {
		if taskType != TaskTypeUnknown {
			types = append(types, taskType)
		}
	}
	slices.Sort(types)
	return types
}

type jevQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// questions is the routing question set. It sees only the instruction, so
// every question is phrased to be answerable from that text alone.
var questions = map[string]jevQuestion{
	"task_type": {
		Type:         "choice",
		Instructions: "只根据这条用户消息本身判断用户要完成的任务类型，不要猜测消息之外的上下文。只要能判断出类型就选具体类型，即使缺少细节。",
		Criteria:     taskTypeCriteria,
	},
	"difficulty": {
		Type:         "score",
		Instructions: "只根据这条消息描述的工作范围判断完成整个请求的复杂度，不要根据消息长度判断。",
		Criteria: []string{
			"简单：一句话就能回答，或者是单一、明确的小改动",
			"中等：需要少量推理，或一两步操作",
			"复杂：涉及多个文件、多个步骤或多个约束",
			"很复杂：跨系统、链路长、需要调研和反复验证，或者失败风险高",
		},
	},
	"task_change": {
		Type:         "noul",
		Instructions: "这条消息是否开启了一个与之前对话无关的新任务？",
		Criteria: map[string]string{
			"true":  "提出了新的、独立的目标，不依赖之前的对话也能理解要做什么",
			"false": "是对之前任务的延续：继续、确认、补充要求、追问、纠正或要求检查之前的结果",
		},
	},
	"larger_model_benefit": {
		Type:         "noul",
		Instructions: "换用能力更强的模型，是否能明显改善这条请求的结果？",
		Criteria: map[string]string{
			"true":  "需要深度推理、严谨的长链路分析或高精度输出；或者用户对之前的结果表达了不满、指出了错误、反复要求修正",
			"false": "常规能力的模型就能做好，更强的模型带来的提升很小",
		},
	},
	"mode": {
		Type:         "choice",
		Instructions: "只根据这条用户消息本身判断：用户更看重结果的质量与可靠，还是更看重省钱和速度。",
		Criteria: map[string]string{
			PreferenceCapability: "明确强调结果必须正确、严谨、完整，或事关重大：线上事故、生产环境改动、正式提交的材料、涉及资金或合规，希望用最好的模型",
			PreferenceBalanced:   "没有表现出对质量或成本的特别要求，或者两方面都兼顾",
			PreferenceCost:       "明确表示要省钱、省时间、尽快出结果，或说明只是随手问问、粗略看看、先出个草稿，不需要很精确",
		},
	},
}

// BuildState builds the classification state from the current human
// instruction and the one before it. Online routing and offline experiments
// must share it, otherwise thresholds tuned offline do not transfer.
//
// The instructions go in separate fields so user text that happens to contain
// a field name cannot blur the boundary between them. maxRunes is the content
// budget of both together. previous is capped at previousMaxRunes — task_change
// only needs its topic — and current gets whatever previous leaves unused. A
// first turn has no previous_task field.
func BuildState(current, previous string, maxRunes, previousMaxRunes int) map[string]string {
	if previous == "" {
		return map[string]string{"current_task": truncateMiddle(current, maxRunes)}
	}
	// The truncation marker is not content and does not count toward the budget.
	previousUsed := len([]rune(previous))
	if previousMaxRunes > 0 {
		previousUsed = min(previousUsed, previousMaxRunes)
	}
	currentBudget := 0 // unlimited
	if maxRunes > 0 {
		currentBudget = max(maxRunes-previousUsed, 1)
	}
	return map[string]string{
		"current_task":  truncateMiddle(current, currentBudget),
		"previous_task": truncateMiddle(previous, previousMaxRunes),
	}
}

// truncateMiddle keeps the head and tail of long text because users often
// paste content first and ask the question last. maxRunes <= 0 keeps all.
func truncateMiddle(text string, maxRunes int) string {
	runes := []rune(text)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return text
	}
	head := maxRunes * 3 / 5
	tail := maxRunes - head
	return string(runes[:head]) + "\n[...]\n" + string(runes[len(runes)-tail:])
}

// JevClient calls a jev decisions endpoint: the self-hosted /v1/systemone or
// OpenRouter's /api/alpha/decisions, which take the same state/questions body.
type JevClient struct {
	Endpoint   string
	Model      string // required by OpenRouter, ignored by the self-hosted endpoint
	APIKey     string
	HTTPClient *http.Client
}

type JevAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}

type JevResponse struct {
	Model   string               `json:"model"`
	Answers map[string]JevAnswer `json:"answers"`
	Usage   map[string]any       `json:"usage,omitempty"`
	// LatencyMs is the server-side inference time reported by the
	// self-hosted endpoint; OpenRouter does not report it.
	LatencyMs *float64 `json:"latency_ms,omitempty"`

	// RequestBody and ResponseBody are the exact bytes on the wire, kept so
	// experiments can be audited and replayed.
	RequestBody  []byte `json:"-"`
	ResponseBody []byte `json:"-"`
}

// Decision is the routing view of a jev response.
type Decision struct {
	TaskType           string   `json:"task_type"`
	TaskTypeConfidence *float64 `json:"task_type_confidence,omitempty"`
	// TaskTypeProbability is the probability of the chosen task type.
	TaskTypeProbability float64 `json:"task_type_probability"`
	// TaskTypeProbabilities holds every task type's probability; a class's
	// probability is the sum over its task types.
	TaskTypeProbabilities map[string]float64 `json:"task_type_probabilities,omitempty"`
	// ModeProbabilities is the probability of each preference the mode question
	// offers; only an adaptive request reads it.
	ModeProbabilities map[string]float64 `json:"mode_probabilities,omitempty"`
	// Difficulty is the most likely rung, 1-based; DifficultyScore is jev's
	// probability-weighted position, 0-based.
	Difficulty         int      `json:"difficulty"`
	DifficultyScore    *float64 `json:"difficulty_score,omitempty"`
	TaskChange         *float64 `json:"task_change"`
	LargerModelBenefit *float64 `json:"larger_model_benefit"`
}

// Decision converts the response to its routing view. Without a previous
// instruction there is nothing to continue, so task_change is 1 by rule and
// the model's answer to it is ignored.
func (r *JevResponse) Decision(hasPrevious bool) Decision {
	taskType := r.Answers["task_type"]
	difficulty := r.Answers["difficulty"]
	decision := Decision{
		TaskType:              taskType.Choice,
		TaskTypeConfidence:    taskType.Confidence,
		TaskTypeProbability:   taskType.Probabilities[taskType.Choice],
		TaskTypeProbabilities: taskType.Probabilities,
		ModeProbabilities:     r.Answers["mode"].Probabilities,
		DifficultyScore:       difficulty.Score,
		TaskChange:            r.Answers["task_change"].Noul,
		LargerModelBenefit:    r.Answers["larger_model_benefit"].Noul,
	}
	bestRung, bestProbability := -1, -1.0
	for key, probability := range difficulty.Probabilities {
		rung, err := strconv.Atoi(key)
		if err == nil && probability > bestProbability {
			bestRung, bestProbability = rung, probability
		}
	}
	if bestRung >= 0 {
		decision.Difficulty = bestRung + 1
	}
	if !hasPrevious {
		firstTurn := 1.0
		decision.TaskChange = &firstTurn
	}
	return decision
}

func (c *JevClient) Classify(ctx context.Context, state map[string]string) (*JevResponse, error) {
	if c.Endpoint == "" {
		return nil, errors.New("jev endpoint is not configured")
	}
	payload := map[string]any{"state": state, "questions": questions}
	if c.Model != "" {
		payload["model"] = c.Model
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jev returned %d: %.512s", resp.StatusCode, respBody)
	}
	var result JevResponse
	if err := common.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	result.RequestBody = body
	result.ResponseBody = respBody
	return &result, nil
}
