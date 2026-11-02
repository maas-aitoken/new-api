package autorouter

import (
	"maps"
	"slices"
)

// Why a model the ranking names cannot serve this request. The codes are stable
// because the admin console translates them.
const (
	ReasonTokenLimit      = "token_limit"      // the token's model whitelist does not allow it
	ReasonGroupDisabled   = "group_disabled"   // the group has not enabled it
	ReasonNoChannel       = "no_channel"       // no enabled channel serves it on this path
	ReasonUnpriced        = "unpriced"         // no price is configured, so billing would fail
	ReasonCapability      = "capability"       // every channel's declared capabilities fall short of the request
	ReasonPerCall         = "per_call"         // billed per call, which cannot be compared with per-token prices
	ReasonClientReasoning = "client_reasoning" // the client's own reasoning settings rule it out
	ReasonUnavailable     = "unavailable"      // not servable, the reason is not known
)

// How the classification of a request was obtained.
const (
	JudgmentLive          = "live"           // jev was called
	JudgmentLiveFailed    = "live_failed"    // jev was called and failed
	JudgmentCache         = "cache"          // the decision was cached
	JudgmentRecentFailure = "recent_failure" // jev failed moments ago and is not retried yet
	JudgmentSession       = "session"        // a sub-agent task without text, routed by its session
	JudgmentAuxiliary     = "auxiliary"      // a client background request, never classified
	JudgmentNoInstruction = "no_instruction" // nothing readable to classify
	JudgmentUnroutable    = "unroutable"     // no model could serve it, so jev was not called
)

// Why a decision is not trusted.
const (
	TrustNoDecision    = "no_decision"
	TrustNoDifficulty  = "no_difficulty"
	TrustLowConfidence = "low_confidence"
)

// Why an adaptive request has no judged weight.
const (
	ModeNoAnswer = "no_answer" // no decision, or the decision carries no usable mode answer
)

// What one ranking tried.
const (
	OutcomeCompared       = "compared"        // picked by quality and price
	OutcomeFirstAvailable = "first_available" // the first servable model in list order
	OutcomeNone           = "none"            // no servable model
	OutcomeInherited      = "inherited"       // the previous route was kept
)

const (
	maxSkipped        = 8
	maxReasonExamples = 5
)

// Trace is how one request got to its model: what was asked, how it was classified,
// which rankings were tried and why a better-ranked model was not picked. SelectRoute
// fills the routing steps and the caller fills the rest.
type Trace struct {
	Request      RequestTrace  `json:"request"`
	Judgment     string        `json:"judgment"`
	Unroutable   *Unroutable   `json:"unroutable,omitempty"`
	Trust        *Trust        `json:"trust,omitempty"`
	Mode         *ModeTrace    `json:"mode,omitempty"`
	Continuation *Continuation `json:"continuation,omitempty"`
	Effort       *EffortTrace  `json:"effort,omitempty"`
	Ladder       []LadderStep  `json:"ladder,omitempty"`
}

// ModeTrace is how an adaptive request got its cost weight. It is set only for an
// adaptive request, and the weight matters only to a request that ran a quality and
// price comparison: one that kept the previous model never used it.
type ModeTrace struct {
	// Reason is empty when the weight was judged, ModeNoAnswer when it fell back to
	// the balanced weight.
	Reason string `json:"reason,omitempty"`
	// Shares is each preference's share of the judged probability.
	Shares map[string]float64 `json:"shares,omitempty"`
	// Position is the expected position on the capability (0), balanced (1), cost (2) scale.
	Position float64 `json:"position"`
	// Weight is the cost weight this request used.
	Weight float64 `json:"weight"`
	// Tiers are the platform's weights at the time, which the weight is read from.
	Tiers TierWeights `json:"tiers"`
}

// RequestTrace holds what the instruction looked like, never its text.
type RequestTrace struct {
	Protocol      Protocol `json:"protocol,omitempty"`
	Auxiliary     bool     `json:"auxiliary,omitempty"`
	Agent         bool     `json:"agent,omitempty"`
	HumanTurns    int      `json:"human_turns"`
	Session       bool     `json:"session,omitempty"`
	TextRunes     int      `json:"text_runes"`
	PreviousRunes int      `json:"previous_runes,omitempty"`
	// Truncated reports that the text sent to jev was shorter than the instruction.
	Truncated bool `json:"truncated,omitempty"`
}

// Unroutable counts, by reason, the models of every ranking when none could serve.
type Unroutable struct {
	Reasons []ReasonCount `json:"reasons"`
}

type ReasonCount struct {
	Reason   string   `json:"reason"`
	Count    int      `json:"count"`
	Examples []string `json:"examples,omitempty"`
}

// Trust says whether the classification could pick a ranking, and from what.
type Trust struct {
	// Reason is empty when the decision is trusted.
	Reason    string  `json:"reason,omitempty"`
	Threshold float64 `json:"threshold"`
	// Classes is the probability of each class, summed over its task types; set only
	// when the task type alone was not enough.
	Classes map[string]float64 `json:"classes,omitempty"`
	// Profiles are the rankings tried, most specific first.
	Profiles []string `json:"profiles,omitempty"`
}

// Continuation is the check whether the request carries on the previous task.
type Continuation struct {
	Previous   string  `json:"previous"`
	TaskChange float64 `json:"task_change"`
	Threshold  float64 `json:"threshold"`
	// Continued is false both when the task changed and when the previous model can
	// no longer serve; the task change against the threshold tells which.
	Continued bool `json:"continued"`
}

// EffortTrace is how difficulty became the reasoning effort. The effort a model
// finally runs at is in the route, after the model's own declaration narrows it.
type EffortTrace struct {
	Difficulty int      `json:"difficulty"`
	Base       string   `json:"base"`
	Benefit    *float64 `json:"benefit,omitempty"`
	Threshold  float64  `json:"threshold"`
	Upgraded   bool     `json:"upgraded,omitempty"`
	// Floored means the previous effort was higher, so a continuation kept it.
	Floored bool `json:"floored,omitempty"`
}

// LadderStep is one ranking tried in order.
type LadderStep struct {
	// Profile is empty for the models kept from the previous route.
	Profile string `json:"profile,omitempty"`
	Models  int    `json:"models,omitempty"`
	Outcome string `json:"outcome"`
	Model   string `json:"model,omitempty"`
	// Skipped are the models that scored high enough to matter but could not be
	// picked, best score first.
	Skipped     []Skipped `json:"skipped,omitempty"`
	SkippedMore int       `json:"skipped_more,omitempty"`
}

// Skipped is a model passed over by the quality and price comparison.
type Skipped struct {
	Model  string  `json:"model"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// skipReason says why model is not servable; with no way to ask, it is unknown.
// A model the lookup finds servable was ruled out by the client's own reasoning fields.
func skipReason(why func(string) string, model string) string {
	if why == nil {
		return ReasonUnavailable
	}
	if reason := why(model); reason != "" {
		return reason
	}
	return ReasonClientReasoning
}

// ExplainUnroutable counts, by reason, every model of the defaults and the task
// lists, for a request none of them can serve.
func (p Policy) ExplainUnroutable(why func(string) string) *Unroutable {
	ordered := slices.Clone(p.DefaultModels)
	for _, profile := range slices.Sorted(maps.Keys(p.TaskModels)) {
		ordered = append(ordered, p.TaskModels[profile]...)
	}
	counts := map[string]*ReasonCount{}
	seen := map[string]bool{}
	for _, model := range ordered {
		if seen[model] {
			continue
		}
		seen[model] = true
		reason := skipReason(why, model)
		if counts[reason] == nil {
			counts[reason] = &ReasonCount{Reason: reason}
		}
		counts[reason].Count++
		if len(counts[reason].Examples) < maxReasonExamples {
			counts[reason].Examples = append(counts[reason].Examples, model)
		}
	}
	reasons := make([]ReasonCount, 0, len(counts))
	for _, reason := range slices.Sorted(maps.Keys(counts)) {
		reasons = append(reasons, *counts[reason])
	}
	slices.SortStableFunc(reasons, func(a, b ReasonCount) int { return b.Count - a.Count })
	return &Unroutable{Reasons: reasons}
}
