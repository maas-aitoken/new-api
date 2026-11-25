package service

import (
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
	"github.com/gin-gonic/gin"
)

const ginKeyAutoFailedModels = "auto_failed_models"

// ResolveAutoRetryModel selects another model within the current request after
// an auto-routed attempt failed. Failed models are removed from every ranking,
// including the default fallback, by their ranking entry. Matching the names a
// ranking entry resolves to cannot tell a model whose name ends in an effort,
// such as qwen3.7-max, from an effort variant of another model.
func ResolveAutoRetryModel(c *gin.Context, candidates AutoCandidates) (*autorouter.Result, bool) {
	if !common.GetContextKeyBool(c, constant.ContextKeyAutoRouted) {
		return nil, false
	}
	failed := failedAutoModels(c)
	if len(failed) == 0 {
		return nil, false
	}
	available := func(name string) bool { return candidates.Serves(name, false) }
	overridable := func(name string) bool { return candidates.Serves(name, true) }
	// Policy shares its ranking slices with the global setting, so filter copies.
	policy := autorouter_setting.GetAutoRouterSetting().Policy(common.GetContextKeyString(c, constant.ContextKeyTokenAutoPreference))
	hasFailed := func(entry string) bool { return slices.Contains(failed, entry) }
	policy.DefaultModels = slices.DeleteFunc(slices.Clone(policy.DefaultModels), hasFailed)
	taskModels := make(map[string][]string, len(policy.TaskModels))
	for task, entries := range policy.TaskModels {
		taskModels[task] = slices.DeleteFunc(slices.Clone(entries), hasFailed)
	}
	policy.TaskModels = taskModels
	routing := autorouter.Candidates{
		Available: available, Overridable: overridable, Price: candidates.Price,
		Why: candidates.Why, Costs: candidates.Costs, Metrics: candidates.Metrics,
	}
	if routing.Metrics == nil {
		routing.Metrics = autoModelMetricsStore.Snapshot
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

// RememberAutoRetrySuccess replaces the optimistically cached route with the
// route that actually answered. Cache failures remain best-effort after success.
func RememberAutoRetrySuccess(c *gin.Context) {
	if !common.GetContextKeyBool(c, constant.ContextKeyAutoRouted) {
		return
	}
	result := GetAutoRouterResult(c)
	if result == nil {
		return
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return
	}
	body, err := storage.Bytes()
	if err != nil {
		return
	}
	rememberAutoRoute(c, autorouter.ExtractInstruction(body), result)
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
