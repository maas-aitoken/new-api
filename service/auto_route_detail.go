package service

import (
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
)

const autoRouteBandPath = "result.pick.band"

// autoRouteTruncation 说明日志为了控制体积省略了什么。数值始终针对完整选路过程，
// 保留的候选和概率只是诊断摘录，不能在子集上重新归一化。
type autoRouteTruncation struct {
	Collections map[string]autoRouteCollectionTruncation `json:"collections,omitempty"`
	Fields      []string                                 `json:"fields,omitempty"`
	Strings     []string                                 `json:"strings,omitempty"`
	Reason      string                                   `json:"reason,omitempty"`
}

type autoRouteCollectionTruncation struct {
	Total              int      `json:"total"`
	Kept               int      `json:"kept"`
	OmittedProbability *float64 `json:"omitted_probability,omitempty"`
}

func (t *autoRouteTruncation) recordCollection(path string, total, kept int) autoRouteCollectionTruncation {
	if t.Collections == nil {
		t.Collections = make(map[string]autoRouteCollectionTruncation)
	}
	if previous, found := t.Collections[path]; found {
		total = previous.Total
	}
	total = max(total, kept)
	entry := autoRouteCollectionTruncation{Total: total, Kept: kept}
	if total == kept {
		delete(t.Collections, path)
	} else {
		t.Collections[path] = entry
	}
	return entry
}

func (t *autoRouteTruncation) markField(path string) {
	if !slices.Contains(t.Fields, path) {
		t.Fields = append(t.Fields, path)
	}
}

func (t *autoRouteTruncation) markString(path string) {
	if !slices.Contains(t.Strings, path) {
		t.Strings = append(t.Strings, path)
	}
}

func marshalAutoRouteDetailWithinLimit(detail autoRouteDetail) (string, bool, error) {
	encoded, err := common.Marshal(detail)
	if err != nil {
		return "", false, err
	}
	if len(encoded) > model.AutoRouteLogDetailMax {
		return "", false, nil
	}
	return string(encoded), true, nil
}

// encodeAutoRouteDetail 保留完整选路结果；确需压缩时只修改日志副本，并明确记录省略内容。
func encodeAutoRouteDetail(result *autorouter.Result, resolveErr error, langfuseTraceURL string) string {
	detail := autoRouteDetail{Result: result, Trace: result.Trace, LangfuseTraceURL: langfuseTraceURL}
	if resolveErr != nil {
		detail.Error = resolveErr.Error()
	}
	encoded, fits, err := marshalAutoRouteDetailWithinLimit(detail)
	if err != nil {
		return encodeAutoRouteDetailEncodingError(detail)
	}
	if fits {
		return encoded
	}

	working := cloneAutoRouteDetail(detail)
	working.Truncated = &autoRouteTruncation{}
	var fullBand []autorouter.Candidate
	if working.Result != nil && working.Result.Pick != nil {
		fullBand = slices.Clone(working.Result.Pick.Band)
	}
	encoded, fits, err = fitAutoRouteBand(working, fullBand)
	if err != nil {
		return encodeAutoRouteDetailEncodingError(detail)
	}
	if fits {
		return encoded
	}

	for level := range autoRouteCollectionLimits {
		compactAutoRouteCollections(&working, level)
		encoded, fits, err = fitAutoRouteBand(working, fullBand)
		if err != nil {
			return encodeAutoRouteDetailEncodingError(detail)
		}
		if fits {
			return encoded
		}
	}

	truncateAutoRouteDetailStrings(&working)
	encoded, fits, err = marshalAutoRouteDetailWithinLimit(working)
	if err != nil {
		return encodeAutoRouteDetailEncodingError(detail)
	}
	if fits {
		return encoded
	}

	minimal := minimalAutoRouteDetail(working)
	encoded, fits, err = marshalAutoRouteDetailWithinLimit(minimal)
	if err != nil {
		return encodeAutoRouteDetailEncodingError(detail)
	}
	if fits {
		return encoded
	}
	return `{"truncated":{"reason":"size_limit","fields":["result","trace"]}}`

}

// cloneAutoRouteDetail 复制所有会被压缩的集合，避免日志编码改写请求后续使用的 Result。
func cloneAutoRouteDetail(detail autoRouteDetail) autoRouteDetail {
	clone := detail
	if detail.Result != nil {
		result := *detail.Result
		if detail.Result.Pick != nil {
			pick := *detail.Result.Pick
			pick.Band = slices.Clone(detail.Result.Pick.Band)
			if detail.Result.Pick.Preview != nil {
				preview := *detail.Result.Pick.Preview
				pick.Preview = &preview
			}
			result.Pick = &pick
		}
		if detail.Result.Decision != nil {
			decision := *detail.Result.Decision
			decision.TaskTypeProbabilities = maps.Clone(decision.TaskTypeProbabilities)
			result.Decision = &decision
		}
		if detail.Result.Jev != nil {
			jev := *detail.Result.Jev
			result.Jev = &jev
		}
		result.Trace = nil
		clone.Result = &result
	}
	if detail.Trace != nil {
		trace := *detail.Trace
		if detail.Trace.Validity != nil {
			validity := *detail.Trace.Validity
			validity.Profiles = slices.Clone(validity.Profiles)
			trace.Validity = &validity
		}
		if detail.Trace.Continuation != nil {
			continuation := *detail.Trace.Continuation
			trace.Continuation = &continuation
		}
		if detail.Trace.Effort != nil {
			effort := *detail.Trace.Effort
			trace.Effort = &effort
		}
		if detail.Trace.Unroutable != nil {
			unroutable := *detail.Trace.Unroutable
			unroutable.Reasons = slices.Clone(unroutable.Reasons)
			for i := range unroutable.Reasons {
				unroutable.Reasons[i].Examples = slices.Clone(unroutable.Reasons[i].Examples)
			}
			trace.Unroutable = &unroutable
		}
		trace.Ladder = slices.Clone(detail.Trace.Ladder)
		for i := range trace.Ladder {
			trace.Ladder[i].Skipped = slices.Clone(trace.Ladder[i].Skipped)
		}
		clone.Trace = &trace
	}
	return clone
}

// fitAutoRouteBand 用二分找当前其余详情下能保留的最大候选数。每次编码都包含截断元数据。
func fitAutoRouteBand(detail autoRouteDetail, fullBand []autorouter.Candidate) (string, bool, error) {
	if detail.Result == nil || detail.Result.Pick == nil {
		return marshalAutoRouteDetailWithinLimit(detail)
	}
	if len(fullBand) == 0 {
		detail.Result.Pick.Band = make([]autorouter.Candidate, 0)
		return marshalAutoRouteDetailWithinLimit(detail)
	}

	setKeep := func(keep int) {
		detail.Result.Pick.Band = compactAutoRouteBand(fullBand, detail.Result.Pick.Chosen, keep)
		entry := detail.Truncated.recordCollection(autoRouteBandPath, len(fullBand), keep)
		totalProbability := 0.0
		for _, candidate := range fullBand {
			totalProbability += candidate.Probability
		}
		if totalProbability > 0 {
			keptProbability := 0.0
			for _, candidate := range detail.Result.Pick.Band {
				keptProbability += candidate.Probability
			}
			omitted := min(max(totalProbability-keptProbability, 0), 1)
			entry.OmittedProbability = &omitted
			if entry.Total != entry.Kept {
				detail.Truncated.Collections[autoRouteBandPath] = entry
			}
		}
	}

	low, high := 1, len(fullBand)
	bestKeep := 0
	bestEncoded := ""
	for low <= high {
		keep := low + (high-low)/2
		setKeep(keep)
		encoded, fits, err := marshalAutoRouteDetailWithinLimit(detail)
		if err != nil {
			return "", false, err
		}
		if fits {
			bestKeep, bestEncoded = keep, encoded
			low = keep + 1
		} else {
			high = keep - 1
		}
	}
	if bestKeep > 0 {
		setKeep(bestKeep)
		return bestEncoded, true, nil
	}
	setKeep(1)
	return "", false, nil
}

// compactAutoRouteBand 先保核心证据，再按 utility 补足；输出仍保持原榜单相对顺序。
func compactAutoRouteBand(band []autorouter.Candidate, chosen string, keep int) []autorouter.Candidate {
	if keep >= len(band) {
		return slices.Clone(band)
	}
	if keep <= 0 {
		return make([]autorouter.Candidate, 0)
	}
	selected := make([]bool, len(band))
	remaining := keep
	add := func(index int) {
		if index < 0 || index >= len(band) || selected[index] || remaining == 0 {
			return
		}
		selected[index] = true
		remaining--
	}

	chosenIndex, highestUtility, lowestCost, firstCostMetadata := -1, 0, -1, -1
	for i, candidate := range band {
		if candidate.Model == chosen && chosenIndex < 0 {
			chosenIndex = i
		}
		if candidate.Utility > band[highestUtility].Utility {
			highestUtility = i
		}
		cost := candidate.Price
		if candidate.CostMultiplier > 0 {
			cost = candidate.Cost
			if firstCostMetadata < 0 {
				firstCostMetadata = i
			}
		}
		if cost > 0 {
			lowest := math.Inf(1)
			if lowestCost >= 0 {
				lowest = band[lowestCost].Price
				if band[lowestCost].CostMultiplier > 0 {
					lowest = band[lowestCost].Cost
				}
			}
			if cost < lowest {
				lowestCost = i
			}
		}
	}
	for _, index := range []int{chosenIndex, highestUtility, 0, lowestCost, firstCostMetadata} {
		add(index)
	}

	order := make([]int, len(band))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		switch {
		case band[a].Utility > band[b].Utility:
			return -1
		case band[a].Utility < band[b].Utility:
			return 1
		default:
			return 0
		}
	})
	for _, index := range order {
		add(index)
	}
	compacted := make([]autorouter.Candidate, 0, keep)
	for i, candidate := range band {
		if selected[i] {
			compacted = append(compacted, candidate)
		}
	}
	return compacted
}

var autoRouteCollectionLimits = []struct {
	probabilities int
	ladder        int
	skipped       int
	reasons       int
	examples      int
}{
	{probabilities: 16, ladder: 8, skipped: 8, reasons: 8, examples: 5},
	{probabilities: 8, ladder: 3, skipped: 3, reasons: 3, examples: 2},
	{probabilities: 3, ladder: 3, skipped: 1, reasons: 3, examples: 1},
	{probabilities: 1, ladder: 1, skipped: 0, reasons: 1, examples: 0},
}

func compactAutoRouteCollections(detail *autoRouteDetail, level int) {
	limits := autoRouteCollectionLimits[level]
	if detail.Result != nil && detail.Result.Decision != nil {
		decision := detail.Result.Decision
		priority := []string{decision.TaskType}
		if detail.Trace != nil && detail.Trace.Validity != nil {
			priority = append(priority, detail.Trace.Validity.Profiles...)
		}
		decision.TaskTypeProbabilities = compactAutoRouteProbabilityMap(decision.TaskTypeProbabilities, limits.probabilities, priority, "result.decision.task_type_probabilities", detail.Truncated)
	}
	if detail.Trace == nil {
		return
	}
	compactAutoRouteLadder(detail, limits)
	if detail.Trace.Unroutable != nil {
		unroutable := detail.Trace.Unroutable
		totalExamples := 0
		for _, reason := range unroutable.Reasons {
			totalExamples += len(reason.Examples)
		}
		totalReasons := len(unroutable.Reasons)
		keptReasons := min(limits.reasons, totalReasons)
		unroutable.Reasons = slices.Clone(unroutable.Reasons[:keptReasons])
		detail.Truncated.recordCollection("trace.unroutable.reasons", totalReasons, keptReasons)
		keptExamples := 0
		for i := range unroutable.Reasons {
			unroutable.Reasons[i].Examples = slices.Clone(unroutable.Reasons[i].Examples[:min(limits.examples, len(unroutable.Reasons[i].Examples))])
			keptExamples += len(unroutable.Reasons[i].Examples)
		}
		detail.Truncated.recordCollection("trace.unroutable.examples", totalExamples, keptExamples)
	}
}

func compactAutoRouteProbabilityMap(values map[string]float64, limit int, priority []string, path string, truncated *autoRouteTruncation) map[string]float64 {
	if values == nil {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int {
		switch {
		case values[a] > values[b]:
			return -1
		case values[a] < values[b]:
			return 1
		default:
			return strings.Compare(a, b)
		}
	})
	selected := make(map[string]bool, min(limit, len(values)))
	for _, key := range priority {
		if _, found := values[key]; found && len(selected) < limit {
			selected[key] = true
		}
	}
	for _, key := range keys {
		if len(selected) >= limit {
			break
		}
		selected[key] = true
	}
	compacted := make(map[string]float64, len(selected))
	for key, value := range values {
		if selected[key] {
			compacted[key] = value
		}
	}
	truncated.recordCollection(path, len(values), len(compacted))
	return compacted
}

func compactAutoRouteLadder(detail *autoRouteDetail, limits struct {
	probabilities int
	ladder        int
	skipped       int
	reasons       int
	examples      int
}) {
	trace := detail.Trace
	totalSkipped := 0
	for _, step := range trace.Ladder {
		totalSkipped += len(step.Skipped)
	}
	keep := min(limits.ladder, len(trace.Ladder))
	selected := make([]bool, len(trace.Ladder))
	remaining := keep
	add := func(index int) {
		if index < 0 || index >= len(trace.Ladder) || selected[index] || remaining == 0 {
			return
		}
		selected[index] = true
		remaining--
	}
	compared := -1
	chosen := ""
	if detail.Result != nil && detail.Result.Pick != nil {
		chosen = detail.Result.Pick.Chosen
	}
	for i, step := range trace.Ladder {
		if step.Outcome == autorouter.OutcomeCompared && (chosen == "" || step.Model == chosen) {
			compared = i
			break
		}
	}
	add(compared)
	add(0)
	add(len(trace.Ladder) - 1)
	for i := range trace.Ladder {
		add(i)
	}
	ladder := make([]autorouter.LadderStep, 0, keep)
	keptSkipped := 0
	for i, step := range trace.Ladder {
		if !selected[i] {
			continue
		}
		kept := min(limits.skipped, len(step.Skipped))
		step.SkippedMore += len(step.Skipped) - kept
		step.Skipped = slices.Clone(step.Skipped[:kept])
		keptSkipped += len(step.Skipped)
		ladder = append(ladder, step)
	}
	detail.Truncated.recordCollection("trace.ladder", len(trace.Ladder), len(ladder))
	detail.Truncated.recordCollection("trace.ladder.skipped", totalSkipped, keptSkipped)
	trace.Ladder = ladder
}

func truncateAutoRouteField(value *string, path string, limit int, truncated *autoRouteTruncation) {
	cleaned := strings.ToValidUTF8(*value, "")
	if cleaned == *value && len(cleaned) <= limit {
		return
	}
	*value = autoRouteUTF8Prefix(cleaned, limit)
	truncated.markString(path)
}

func truncateAutoRouteDetailStrings(detail *autoRouteDetail) {
	truncate := func(value *string, path string, limit int) {
		truncateAutoRouteField(value, path, limit, detail.Truncated)
	}
	detail.Truncated.Reason = "size_limit"
	truncate(&detail.Error, "error", 1024)
	if detail.Result != nil {
		result := detail.Result
		truncate(&result.ResolvedModel, "result.resolved_model", 256)
		truncate(&result.Route.Profile, "result.profile", 256)
		truncate(&result.Route.Model, "result.model", 256)
		truncate(&result.Route.Effort, "result.effort", 64)
		truncate(&result.AppliedEffort, "result.applied_effort", 64)
		if result.Decision != nil {
			truncate(&result.Decision.TaskType, "result.decision.task_type", 256)
		}
		if result.Pick != nil {
			truncate(&result.Pick.Chosen, "result.pick.chosen", 256)
			for i := range result.Pick.Band {
				candidate := &result.Pick.Band[i]
				truncate(&candidate.Model, "result.pick.band.model", 256)
				truncate(&candidate.CostSource, "result.pick.band.cost_source", 64)
				truncate(&candidate.Preview, "result.pick.band.preview", 128)
			}
		}
		if result.Jev != nil {
			truncate(&result.Jev.Endpoint, "result.jev.endpoint", 512)
			truncate(&result.Jev.Error, "result.jev.error", 1024)
		}
	}
	if detail.Trace != nil {
		trace := detail.Trace
		truncate(&trace.Judgment, "trace.judgment", 128)
		if trace.Validity != nil {
			truncate(&trace.Validity.Error, "trace.validity.error", 128)
			for i := range trace.Validity.Profiles {
				truncate(&trace.Validity.Profiles[i], "trace.validity.profiles", 256)
			}
		}
		if trace.Continuation != nil {
			truncate(&trace.Continuation.Previous, "trace.continuation.previous", 256)
		}
		if trace.Effort != nil {
			truncate(&trace.Effort.Base, "trace.effort.base", 64)
		}
		if trace.Unroutable != nil {
			for i := range trace.Unroutable.Reasons {
				reason := &trace.Unroutable.Reasons[i]
				truncate(&reason.Reason, "trace.unroutable.reasons.reason", 128)
				for j := range reason.Examples {
					truncate(&reason.Examples[j], "trace.unroutable.reasons.examples", 256)
				}
			}
		}
		for i := range trace.Ladder {
			step := &trace.Ladder[i]
			truncate(&step.Profile, "trace.ladder.profile", 256)
			truncate(&step.Model, "trace.ladder.model", 256)
			for j := range step.Skipped {
				truncate(&step.Skipped[j].Model, "trace.ladder.skipped.model", 256)
				truncate(&step.Skipped[j].Reason, "trace.ladder.skipped.reason", 128)
			}
		}
	}
}

func autoRouteUTF8Prefix(value string, limit int) string {
	value = strings.ToValidUTF8(value, "")
	if len(value) <= limit {
		return value
	}
	end := 0
	for index := range value {
		if index > limit {
			break
		}
		end = index
	}
	return value[:end]
}

// minimalAutoRouteDetail 是极端输入下的最后降级，仍保留核心路由、判断和真实错误。
func minimalAutoRouteDetail(detail autoRouteDetail) autoRouteDetail {
	minimal := autoRouteDetail{Error: detail.Error, Truncated: detail.Truncated, LangfuseTraceURL: detail.LangfuseTraceURL}
	if minimal.Truncated == nil {
		minimal.Truncated = &autoRouteTruncation{}
	}
	minimal.Truncated.Reason = "size_limit"
	truncateAutoRouteField(&minimal.Error, "error", 512, minimal.Truncated)
	if detail.Result != nil {
		result := detail.Result
		if result.Pick != nil {
			minimal.Truncated.markField("result.pick")
		}
		if result.Decision != nil {
			minimal.Truncated.markField("result.decision")
		}
		if result.Jev != nil {
			minimal.Truncated.markField("result.jev")
		}
		minimal.Result = &autorouter.Result{
			Route: autorouter.Route{
				Profile: result.Profile,
				Model:   result.Model,
				Effort:  result.Effort,
			},
			ResolvedModel:  result.ResolvedModel,
			Source:         result.Source,
			Preference:     result.Preference,
			EffortInBody:   result.EffortInBody,
			EffortOverride: result.EffortOverride,
			AppliedEffort:  result.AppliedEffort,
			EffortAction:   result.EffortAction,
		}
		truncateAutoRouteField(&minimal.Result.Profile, "result.profile", 128, minimal.Truncated)
		truncateAutoRouteField(&minimal.Result.Model, "result.model", 128, minimal.Truncated)
		truncateAutoRouteField(&minimal.Result.Effort, "result.effort", 32, minimal.Truncated)
		truncateAutoRouteField(&minimal.Result.ResolvedModel, "result.resolved_model", 128, minimal.Truncated)
		truncateAutoRouteField(&minimal.Result.AppliedEffort, "result.applied_effort", 32, minimal.Truncated)
	}
	if detail.Trace != nil {
		trace := detail.Trace
		for _, omitted := range []struct {
			path    string
			present bool
		}{
			{"trace.unroutable", trace.Unroutable != nil},
			{"trace.validity", trace.Validity != nil},
			{"trace.continuation", trace.Continuation != nil},
			{"trace.effort", trace.Effort != nil},
			{"trace.ladder", len(trace.Ladder) > 0},
		} {
			if omitted.present {
				minimal.Truncated.markField(omitted.path)
			}
		}
		minimal.Trace = &autorouter.Trace{Request: trace.Request, Judgment: trace.Judgment}
		truncateAutoRouteField(&minimal.Trace.Judgment, "trace.judgment", 64, minimal.Truncated)
	}
	return minimal
}

// encodeAutoRouteDetailEncodingError 去掉可能含非有限浮点数的部分，明确说明编码失败。
func encodeAutoRouteDetailEncodingError(detail autoRouteDetail) string {
	working := cloneAutoRouteDetail(detail)
	working.Truncated = &autoRouteTruncation{Reason: "encoding_error"}
	if working.Result != nil {
		if working.Result.Pick != nil {
			working.Truncated.markField("result.pick")
			working.Result.Pick = nil
		}
		if working.Result.Decision != nil {
			working.Truncated.markField("result.decision")
			working.Result.Decision = nil
		}
	}
	if working.Trace != nil {
		if working.Trace.Continuation != nil {
			working.Truncated.markField("trace.continuation")
			working.Trace.Continuation = nil
		}
		if working.Trace.Effort != nil {
			working.Truncated.markField("trace.effort")
			working.Trace.Effort = nil
		}
		if len(working.Trace.Ladder) > 0 {
			working.Truncated.markField("trace.ladder")
			working.Trace.Ladder = nil
		}
	}
	truncateAutoRouteDetailStrings(&working)
	working.Truncated.Reason = "encoding_error"
	if encoded, fits, err := marshalAutoRouteDetailWithinLimit(working); err == nil && fits {
		return encoded
	}
	minimal := minimalAutoRouteDetail(working)
	minimal.Truncated.Reason = "encoding_error"
	if encoded, fits, err := marshalAutoRouteDetailWithinLimit(minimal); err == nil && fits {
		return encoded
	}
	return `{"truncated":{"reason":"encoding_error","fields":["result","trace"]}}`
}
