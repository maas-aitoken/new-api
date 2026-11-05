package autorouter

import "slices"

// AutoModel is the model name a client sends to let the gateway pick one.
const AutoModel = "auto"

// effortLadder lists reasoning efforts in ascending strength. A model supports
// an effort when the effort-suffixed name (model + "-" + effort) is usable;
// the channel adaptors turn that suffix into the provider's reasoning field.
var effortLadder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// Efforts lists the reasoning efforts a route can carry, weakest first.
func Efforts() []string { return slices.Clone(effortLadder) }

// Policy holds the tunable routing rules: the task type picks the model, the
// difficulty picks the reasoning effort.
type Policy struct {
	// TaskModels maps a task type or task class to its models in preference
	// order.
	TaskModels map[string][]string
	// DefaultModels serve a task type with no available model in TaskModels
	// and a request with no usable classification.
	DefaultModels []string
	// DifficultyEfforts maps difficulty rungs (1-based) to efforts; rungs past
	// the end use the last entry.
	DifficultyEfforts []string
	DefaultEffort     string
	// UpgradeBenefit is the larger_model_benefit at or above which the effort
	// goes one rung up: above the effort of a kept previous model, else above the
	// difficulty's effort. A difficulty whose effort already reaches that rung
	// counts as the raise. It applies even when the task type is unknown.
	UpgradeBenefit float64
	// ContinuationTaskChange is the task_change below which the request
	// continues the previous task: the model stays, so the prompt cache
	// survives, and the effort may go up but never down.
	ContinuationTaskChange float64
	// TaskScores maps a task type to the quality score of its models. A decision
	// that routes by a ranking picks among its scored models by quality and price; models
	// without a score are only a fallback in TaskModels order. The scores under
	// DefaultProfile rank DefaultModels the same way.
	TaskScores map[string]map[string]float64
	// Preference names the choice this request's key made between capability and
	// price, and CostWeight is the most quality score that choice lets a model give
	// up for being cheaper. It bounds how far the pick can fall below the best
	// score; zero means the best-scored model always wins.
	Preference string
	CostWeight float64
	// Tiers are the platform's weights of the three fixed preferences. An adaptive
	// request replaces CostWeight with a weight read from them by its mode answer.
	Tiers TierWeights
}

// DefaultProfile is the ranking DefaultModels follow when no task-specific
// ranking applies.
const DefaultProfile = "general"

// Route is a routing choice before it is resolved to a usable model name.
type Route struct {
	// Profile is the ranking the model came from: a task type, its class, or
	// empty for the default models.
	Profile string `json:"profile,omitempty"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

// Fallback names the level of Profile: leaf, class or default.
func (r Route) Fallback() string {
	switch {
	case r.Profile == "":
		return "default"
	case TaskClass(r.Profile) == "":
		return "class"
	default:
		return "leaf"
	}
}

// validateDecision checks a decision and returns the rankings it routes by,
// most specific first: the task type jev chose, which is its most probable
// one, then that task type's class. A valid decision has a difficulty and a
// task type among those jev was offered; one whose task type is unknown is
// valid and routes by no ranking. validity.Error says why a decision is not
// valid, and is DecisionMissing when there is none.
func validateDecision(decision *Decision) (profiles []string, validity Validity) {
	if decision == nil {
		validity.Error = DecisionMissing
		return nil, validity
	}
	if decision.Difficulty <= 0 {
		validity.Error = DecisionNoDifficulty
		return nil, validity
	}
	if _, offered := taskTypeCriteria[decision.TaskType]; !offered {
		validity.Error = DecisionInvalidTaskType
		return nil, validity
	}
	if decision.TaskType == TaskTypeUnknown {
		return nil, validity
	}
	validity.Profiles = []string{decision.TaskType, TaskClass(decision.TaskType)}
	return validity.Profiles, validity
}

// SelectRoute maps a jev decision to a route. decision is nil when jev failed;
// previous is the route of the previous instruction, nil when unknown.
// inherited reports that the decision was not used and the route came from
// previous or the defaults. ok is false when no model is available at all.
//
// A valid decision whose task type is unknown routes by no ranking, so it
// keeps the previous route or takes the defaults, like a request without a
// valid decision. A larger model benefit at or above UpgradeBenefit still
// sets its effort: the model is chosen that way, the effort goes one rung up
// as UpgradeBenefit describes, and inherited is false because the effort came
// from the decision.
//
// candidates.Available judges a model against the request exactly as the client
// sent it; the inherited fallbacks use it because they leave the client's
// reasoning fields alone. candidates.Overridable judges a model with the
// client's reasoning fields set aside, because a decision that sets the effort
// picks it itself and rewrites them. pick is set when the model was chosen by
// quality and price among several candidates.
//
// trace, when not nil, receives how the route came about: whether the decision was
// valid, whether the request continued the previous task, how difficulty became an
// effort, and each ranking tried.
func SelectRoute(decision *Decision, previous *Route, policy Policy, candidates Candidates, trace *Trace) (route Route, inherited, ok bool, pick *Pick) {
	if trace == nil {
		trace = &Trace{}
	}
	if policy.Preference == PreferenceAdaptive {
		// mode 问题与任务类型无关：任务类型只决定走哪个榜单，unknown 或没有合法判断时成本倾向照样可用。
		var probabilities map[string]float64
		if decision != nil {
			probabilities = decision.ModeProbabilities
		}
		mode := policy.Tiers.Blend(probabilities)
		policy.CostWeight = mode.Weight
		trace.Mode = &mode
	}
	available, overridable := candidates.Available, candidates.Overridable
	profiles, validity := validateDecision(decision)
	trace.Validity = &validity
	upgrade := decision != nil && decision.LargerModelBenefit != nil && *decision.LargerModelBenefit >= policy.UpgradeBenefit
	// 任务类型是 unknown 时没有榜单可选，但大模型收益够高说明这条消息本身需要更强的推理
	// （多是对上一轮结果不满、要求重做），档位仍由这次判断来定。
	effortOnly := validity.Error == "" && decision.TaskType == TaskTypeUnknown && upgrade
	if profiles == nil && !effortOnly {
		if previous != nil && ModelAvailable(previous.Model, available) {
			trace.Ladder = append(trace.Ladder, LadderStep{Profile: previous.Profile, Outcome: OutcomeInherited, Model: previous.Model})
			return *previous, true, true, nil
		}
		model, picked, found := policy.choose(DefaultProfile, policy.DefaultModels, available, false, policy.DefaultEffort, candidates, trace)
		return Route{Model: model, Effort: policy.DefaultEffort}, true, found, picked
	}

	effort := policy.DefaultEffort
	if len(policy.DifficultyEfforts) > 0 {
		effort = policy.DifficultyEfforts[min(decision.Difficulty, len(policy.DifficultyEfforts))-1]
	}
	trace.Effort = &EffortTrace{Difficulty: decision.Difficulty, Base: effort, Benefit: decision.LargerModelBenefit, Threshold: policy.UpgradeBenefit}

	// 沿用上一轮的模型：任务类型是 unknown 时只要上一轮的模型还能用；任务类型是二级类时还要求同任务延续。
	keep := false
	if previous != nil {
		switch {
		case effortOnly:
			keep = ModelAvailable(previous.Model, overridable)
			if keep {
				trace.Ladder = append(trace.Ladder, LadderStep{Profile: previous.Profile, Outcome: OutcomeInherited, Model: previous.Model})
			}
		case decision.TaskChange != nil:
			keep = *decision.TaskChange < policy.ContinuationTaskChange && ModelAvailable(previous.Model, overridable)
			trace.Continuation = &Continuation{Previous: previous.Model, TaskChange: *decision.TaskChange, Threshold: policy.ContinuationTaskChange, Continued: keep}
		}
	}
	if keep && slices.Index(effortLadder, previous.Effort) > slices.Index(effortLadder, effort) {
		effort = previous.Effort
		trace.Effort.Floored = true
	}
	// 提升按上一轮算：沿用上一轮的模型时要比上一轮的档位高一档，难度给的档位已经到了就不再加；
	// 没有沿用时比难度给的档位高一档。
	if upgrade {
		raiseFrom := effort
		if keep {
			raiseFrom = previous.Effort
		}
		if rank := slices.Index(effortLadder, raiseFrom); rank >= 0 && rank < len(effortLadder)-1 && rank+1 > slices.Index(effortLadder, effort) {
			effort = effortLadder[rank+1]
			trace.Effort.Upgraded = true
		}
	}
	if keep {
		return Route{Profile: previous.Profile, Model: previous.Model, Effort: effort}, false, true, nil
	}

	for _, profile := range profiles {
		if model, picked, found := policy.choose(profile, policy.TaskModels[profile], overridable, true, effort, candidates, trace); found {
			return Route{Profile: profile, Model: model, Effort: effort}, false, true, picked
		}
	}
	model, picked, found := policy.choose(DefaultProfile, policy.DefaultModels, overridable, true, effort, candidates, trace)
	return Route{Model: model, Effort: effort}, false, found, picked
}

// choose picks a model from one ranking's list. With scores and prices it
// compares quality and price under the policy's preference; otherwise, or when no
// scored model is servable, it takes the first available model in list order.
// picked is set only for the comparison. Every call adds a step to trace.
// ignoreClientReasoning 表示 available 撇开了客户端的推理字段（档位由判断定下时走 overridable），
// effort 是这一步的期望档位，两者一起交给 Candidates.Costs，让主机按同一口径找出服务请求的模型名和渠道。
func (p Policy) choose(profile string, models []string, available func(string) bool, ignoreClientReasoning bool, effort string, candidates Candidates, trace *Trace) (string, *Pick, bool) {
	step := LadderStep{Profile: profile, Models: len(models), Outcome: OutcomeNone}
	var picked *Pick
	model, found := "", false
	if scores := p.TaskScores[profile]; len(scores) > 0 && candidates.Price != nil {
		servable := func(name string) bool { return ModelAvailable(name, available) }
		var costs func([]string) map[string]Cost
		if candidates.Costs != nil {
			costs = func(names []string) map[string]Cost { return candidates.Costs(names, effort, ignoreClientReasoning) }
		}
		pick, ok := pickByQualityCost(models, scores, p.CostWeight, servable, candidates.Price, costs, candidates.Why)
		step.Skipped, step.SkippedMore = pick.skipped, pick.skippedMore
		if ok {
			picked, model, found = &pick, pick.Chosen, true
			step.Outcome = OutcomeCompared
		}
	}
	if !found {
		if model, found = firstAvailable(models, available); found {
			step.Outcome = OutcomeFirstAvailable
		}
	}
	step.Model = model
	trace.Ladder = append(trace.Ladder, step)
	return model, picked, found
}

func firstAvailable(models []string, available func(model string) bool) (string, bool) {
	for _, model := range models {
		if ModelAvailable(model, available) {
			return model, true
		}
	}
	return "", false
}

// ModelAvailable reports whether model is usable bare or in any effort variant.
func ModelAvailable(model string, available func(model string) bool) bool {
	_, ok := ResolveVariant(model, "", available)
	return ok
}

// AnyModelAvailable reports whether the defaults or any task list holds a
// usable model. Without one no classification can lead to a route, so the
// caller rejects the request before paying for the classifier call.
func (p Policy) AnyModelAvailable(available func(model string) bool) bool {
	if _, found := firstAvailable(p.DefaultModels, available); found {
		return true
	}
	for _, models := range p.TaskModels {
		if _, found := firstAvailable(models, available); found {
			return true
		}
	}
	return false
}

// ResolveVariant returns the usable name of model at effort: the exact
// effort-suffixed name, else the bare name (the provider's default effort),
// else the nearest supported effort, preferring the stronger one on a tie so a
// missing variant never silently weakens reasoning. An effort outside the
// ladder searches from medium.
func ResolveVariant(model, effort string, available func(model string) bool) (string, bool) {
	if model == "" {
		return "", false
	}
	if effort != "" && available(model+"-"+effort) {
		return model + "-" + effort, true
	}
	if available(model) {
		return model, true
	}
	start := slices.Index(effortLadder, effort)
	if start < 0 {
		start = slices.Index(effortLadder, "medium")
		// 下面的循环从相邻档位找起，不会探测 medium 本身。
		if available(model + "-medium") {
			return model + "-medium", true
		}
	}
	for distance := 1; distance < len(effortLadder); distance++ {
		for _, rank := range []int{start + distance, start - distance} {
			if rank >= 0 && rank < len(effortLadder) && available(model+"-"+effortLadder[rank]) {
				return model + "-" + effortLadder[rank], true
			}
		}
	}
	return "", false
}

// Where a route came from.
const (
	SourceJev     = "jev"     // jev ran and its decision set the route, or at least its effort
	SourceCache   = "cache"   // that decision came from the cache; the route is recomputed
	SourceInherit = "inherit" // the task type was unknown, or there was no valid decision; previous route kept
	SourceDefault = "default" // the same, with no previous route
)

// Result is the routing outcome of one request.
type Result struct {
	ResolvedModel string `json:"resolved_model"`
	Route
	Source   string    `json:"source"`
	Decision *Decision `json:"decision,omitempty"`
	// Preference is the capability/price preference this request ran under: the
	// key's own, else the platform default.
	Preference string `json:"preference,omitempty"`
	// EffortInBody 表示档位写进请求体而不是拼在模型名后缀上：只有模型声明了
	// 推理维度，并且裸模型名本身可用时才这样做，其余沿用后缀。
	EffortInBody bool `json:"effort_in_body,omitempty"`
	// EffortOverride 表示这条路由的档位由 jev 的判断定下，可以覆盖客户端自己写的推理字段：
	// 任务类型是二级类，或任务类型是 unknown 但大模型收益达到提档阈值。其余情况（任务类型
	// 是 unknown、没有合法判断、辅助请求）为 false，不碰客户端字段。
	EffortOverride bool `json:"effort_override,omitempty"`
	// Pick 是模型按质量和价格在多个候选里选出时的比较过程；沿用上一轮模型、
	// 兜底和按榜单顺序取第一个时为 nil。
	Pick *Pick `json:"pick,omitempty"`
	// AppliedEffort 和 EffortAction 是按模型的推理声明收敛后真正写进请求体的结果。
	AppliedEffort string       `json:"applied_effort,omitempty"`
	EffortAction  EffortAction `json:"effort_action,omitempty"`
	// Jev is set only when this request called jev, cache hits leave it nil.
	Jev *JevCall `json:"jev,omitempty"`
	// Trace is how the route came about, for the routing log. The consume log's
	// summary leaves it out, so it never makes every log row bigger.
	Trace *Trace `json:"-"`
}

type JevCall struct {
	Endpoint  string `json:"endpoint"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
	// Response is the raw jev response body.
	Response []byte `json:"-"`
}
