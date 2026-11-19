package service

import (
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
	"github.com/gin-gonic/gin"
)

const ginKeyAutoFailedModels = "auto_failed_models"

// ResolveAutoRetryModel selects another model within the current request after
// an auto-routed attempt failed. Failed models are excluded from every ranking,
// including the previous-route and default fallbacks.
func ResolveAutoRetryModel(c *gin.Context, candidates AutoCandidates) (*autorouter.Result, bool) {
	if !common.GetContextKeyBool(c, constant.ContextKeyAutoRouted) {
		return nil, false
	}
	failed := failedAutoModels(c)
	if len(failed) == 0 {
		return nil, false
	}
	excluded := func(name string) bool {
		for _, effort := range autorouter.Efforts() {
			name = strings.TrimSuffix(name, "-"+effort)
		}
		return slices.Contains(failed, name)
	}
	available := func(name string) bool {
		return !excluded(name) && candidates.Serves(name, false)
	}
	overridable := func(name string) bool {
		return !excluded(name) && candidates.Serves(name, true)
	}
	policy := autorouter_setting.GetAutoRouterSetting().Policy(common.GetContextKeyString(c, constant.ContextKeyTokenAutoPreference))
	routing := autorouter.Candidates{
		Available: available, Overridable: overridable, Price: candidates.Price,
		Why: candidates.Why, Costs: candidates.Costs, Metrics: candidates.Metrics, Random: candidates.Random,
	}
	if routing.Metrics == nil {
		routing.Metrics = autoModelMetricsStore.Snapshot
	}
	if routing.Random == nil {
		routing.Random = rand.Float64
	}
	initial := GetAutoRouterResult(c)
	var decision *autorouter.Decision
	if initial != nil {
		decision = initial.Decision
	}
	route, _, ok, pick := autorouter.SelectRoute(decision, nil, policy, routing, nil)
	if !ok {
		return nil, false
	}
	serves := available
	effortOverride := initial != nil && initial.EffortOverride
	if effortOverride {
		serves = overridable
	}
	resolved, effortInBody, ok := AutoServingModel(route.Model, route.Effort, serves)
	if !ok {
		return nil, false
	}
	result := &autorouter.Result{Route: route, ResolvedModel: resolved, EffortInBody: effortInBody, EffortOverride: effortOverride, Pick: pick, Decision: decision}
	if effortInBody {
		result.EffortAction = autorouter.EffortKeep
		if effortOverride {
			support := CapabilityGateFromContext(c).EffortSupport(resolved, candidates.Channels(resolved))
			result.AppliedEffort, result.EffortAction = support.Clamp(route.Effort)
			CapabilityGateFromContext(c).OverrideReasoning(result.EffortAction, result.AppliedEffort)
		}
	}
	c.Set(ginKeyAutoRouterResult, result)
	return result, true
}

// ExcludeAutoModel remembers the ranked base model for this request only.
// All its effort variants are excluded without affecting other requests.
func ExcludeAutoModel(c *gin.Context, modelName string) {
	if !common.GetContextKeyBool(c, constant.ContextKeyAutoRouted) || modelName == "" {
		return
	}
	if current := GetAutoRouterResult(c); current != nil && current.Route.Model != "" {
		modelName = current.Route.Model
	}
	failed := failedAutoModels(c)
	if !slices.Contains(failed, modelName) {
		c.Set(ginKeyAutoFailedModels, append(failed, modelName))
	}
}

func failedAutoModels(c *gin.Context) []string {
	failed, _ := c.Get(ginKeyAutoFailedModels)
	models, _ := failed.([]string)
	return models
}
