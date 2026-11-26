package service

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const (
	resourcePoolAlertKindSource = "source"
	resourcePoolAlertKindGroup  = "group"
	// resourcePoolAlertKindPool is a provider's whole pool, every source together.
	resourcePoolAlertKindPool   = "pool"
	resourcePoolAlertMaxMention = 512
)

var resourcePoolAlertProviderLabels = map[string]string{
	model.ResourcePoolClaude: "Claude",
	model.ResourcePoolGPT:    "GPT",
	model.ResourcePoolGemini: "Gemini",
	model.ResourcePoolGrok:   "Grok",
}

// ResourcePoolAlertConfig is what the console reads and writes. WebhookURL is
// masked on the way out; an empty value on the way in keeps the stored one.
type ResourcePoolAlertConfig struct {
	Enabled           bool                                               `json:"enabled"`
	WebhookURL        string                                             `json:"webhook_url"`
	WebhookConfigured bool                                               `json:"webhook_configured"`
	Mentions          string                                             `json:"mentions"`
	NotifyStart       string                                             `json:"notify_start"`
	NotifyEnd         string                                             `json:"notify_end"`
	Rules             map[string]operation_setting.ResourcePoolAlertRule `json:"rules"`
	// ClaudeSourceEnabled also checks every Claude channel on its own, next
	// to the whole pool and the account groups.
	ClaudeSourceEnabled bool `json:"claude_source_enabled"`
	// Alerting lists the targets currently reported as above their threshold.
	Alerting []operation_setting.ResourcePoolAlertState `json:"alerting"`
	// UTCOffset is the server time zone the notification period is read in.
	UTCOffset string `json:"utc_offset"`
}

// GetResourcePoolAlertConfig returns the stored configuration for the console.
func GetResourcePoolAlertConfig() ResourcePoolAlertConfig {
	setting := operation_setting.GetResourcePoolAlertSetting()
	config := ResourcePoolAlertConfig{
		Enabled:             setting.Enabled,
		WebhookURL:          MaskWeComGroupBotURL(setting.WebhookURL),
		WebhookConfigured:   setting.WebhookURL != "",
		Mentions:            setting.Mentions,
		NotifyStart:         setting.NotifyStart,
		NotifyEnd:           setting.NotifyEnd,
		Rules:               make(map[string]operation_setting.ResourcePoolAlertRule, len(model.ResourcePoolProviders)),
		ClaudeSourceEnabled: setting.ClaudeSourceEnabled,
		Alerting:            make([]operation_setting.ResourcePoolAlertState, 0, len(setting.State)),
		UTCOffset:           time.Now().Format("-07:00"),
	}
	for _, provider := range model.ResourcePoolProviders {
		config.Rules[provider] = setting.Rule(provider)
	}
	for _, key := range slices.Sorted(maps.Keys(setting.State)) {
		config.Alerting = append(config.Alerting, setting.State[key])
	}
	return config
}

// resolveResourcePoolAlertWebhook returns the URL to use for a request: the
// submitted one, or the stored one when the console left the field empty or
// echoed the mask back.
func resolveResourcePoolAlertWebhook(submitted string) (string, error) {
	submitted = strings.TrimSpace(submitted)
	if submitted == "" || strings.Contains(submitted, "key=****") {
		return operation_setting.GetResourcePoolAlertSetting().WebhookURL, nil
	}
	if ValidateWeComGroupBotURL(submitted) != nil {
		return "", common.NewMessage("Enter a valid WeCom group bot webhook URL")
	}
	return submitted, nil
}

// SaveResourcePoolAlertConfig validates and stores the configuration. While
// alerts stay on, the alert state is left alone and the next check reconciles
// it with the new rules. Turning alerts off clears it, so turning them back on
// reports every target that is above its threshold at that point.
func SaveResourcePoolAlertConfig(config ResourcePoolAlertConfig) error {
	webhookURL, err := resolveResourcePoolAlertWebhook(config.WebhookURL)
	if err != nil {
		return err
	}
	if config.Enabled && webhookURL == "" {
		return common.NewMessage("Set the webhook URL before enabling alerts")
	}
	for _, clock := range []string{config.NotifyStart, config.NotifyEnd} {
		if _, ok := operation_setting.ParseResourcePoolAlertClock(clock); !ok {
			return common.NewMessage("Enter a time in HH:MM format")
		}
	}
	mentions := strings.TrimSpace(config.Mentions)
	if len(mentions) > resourcePoolAlertMaxMention {
		return common.NewMessage("Value is too large")
	}
	rules := make(map[string]operation_setting.ResourcePoolAlertRule, len(model.ResourcePoolProviders))
	for _, provider := range model.ResourcePoolProviders {
		rule, ok := config.Rules[provider]
		if !ok {
			rule = operation_setting.GetResourcePoolAlertSetting().Rule(provider)
		}
		if rule.Threshold <= 0 || rule.Threshold > 100 {
			return common.NewMessage("Enter a percentage between 0 and 100")
		}
		rules[provider] = rule
	}
	rulesJSON, err := common.Marshal(rules)
	if err != nil {
		return err
	}
	prefix := operation_setting.ResourcePoolAlertOptionPrefix
	values := map[string]string{
		prefix + "enabled":               strconv.FormatBool(config.Enabled),
		prefix + "webhook_url":           webhookURL,
		prefix + "mentions":              mentions,
		prefix + "notify_start":          config.NotifyStart,
		prefix + "notify_end":            config.NotifyEnd,
		prefix + "rules":                 string(rulesJSON),
		prefix + "claude_source_enabled": strconv.FormatBool(config.ClaudeSourceEnabled),
	}
	if !config.Enabled {
		values[prefix+"state"] = "{}"
	}
	return model.UpdateOptionsBulk(values)
}

// SendResourcePoolAlertTest posts a test message so the administrator can
// verify the webhook before relying on it. An empty URL uses the stored one.
func SendResourcePoolAlertTest(ctx context.Context, submittedURL string, mentions string) error {
	webhookURL, err := resolveResourcePoolAlertWebhook(submittedURL)
	if err != nil {
		return err
	}
	if webhookURL == "" {
		return common.NewMessage("Set the webhook URL before enabling alerts")
	}
	content := i18n.Translate("", i18n.MsgResourcePoolAlertTest, map[string]any{"Site": common.SystemName})
	return SendWeComGroupBotText(ctx, webhookURL, content, mentions)
}

// ResourcePoolAlertDue reports whether the hourly check should run at now:
// alerts are on, a webhook and at least one provider rule are set, and now is
// inside the notification period. Nothing is checked outside the period, so
// a level that crossed its threshold overnight is reported by the first check
// of the next period if it still holds.
func ResourcePoolAlertDue(now time.Time) bool {
	setting := operation_setting.GetResourcePoolAlertSetting()
	if !setting.Enabled || setting.WebhookURL == "" || !setting.InNotifyWindow(now) {
		return false
	}
	return slices.ContainsFunc(model.ResourcePoolProviders, func(provider string) bool {
		return setting.Rule(provider).Enabled
	})
}

// ResourcePoolAlertInterval makes the check due once per wall-clock hour: the
// scheduler runs a task when its last run is older than the interval, and the
// time elapsed in the current hour makes that "the last run was before this
// hour began". A restart or a failed poll therefore does not shift later runs.
func ResourcePoolAlertInterval(now time.Time) time.Duration {
	return time.Duration(now.Minute())*time.Minute + time.Duration(now.Second()+1)*time.Second
}

func resourcePoolAlertKey(provider string, kind string, id string) string {
	return provider + "/" + kind + "/" + id
}

// resourcePoolLevel is the water level of one account set, 0–100, with the
// same definition the resource pool page uses. Quota pools (Claude) divide
// total used credits by the total limit, disabled accounts included. Window
// pools (GPT / Gemini / Grok) average the 7d usage of the accounts whose
// numbers are current. ok is false when no account reports usage.
func resourcePoolLevel(accounts []*model.ResourcePoolAccount, windowed bool, now time.Time) (level float64, ok bool) {
	if windowed {
		sum, live := 0.0, 0
		for _, account := range accounts {
			view := capacityAccountOf(account, true)
			stale := view.status == "error" || view.status == "unauthorized" || view.status == "banned"
			if !view.enabled || stale || view.windowExpired(now) {
				continue
			}
			sum += view.used
			live++
		}
		if live == 0 {
			return 0, false
		}
		return sum / float64(live), true
	}
	if len(accounts) == 0 {
		return 0, false
	}
	used, limit, percent := 0.0, 0.0, 0.0
	for _, account := range accounts {
		used += account.UsageCurrent
		limit += account.UsageLimit
		percent += account.UsagePercentage
	}
	level = percent / float64(len(accounts))
	if limit > 0 {
		level = used / limit * 100
	}
	return min(max(level, 0), 100), true
}

// resourcePoolWaterLevels lists the targets of one provider that report a
// water level. GPT / Gemini / Grok have one target per source that polled
// successfully. Claude has the whole pool, every account group and, when
// claudeSources is set, every source. unknown reports whether a target that
// is missing from the result is missing because its data could not be polled
// (as opposed to the target being gone, having no accounts left or no longer
// being monitored). The pool and the groups span sources, so one failed
// source makes all of them unknown.
func resourcePoolWaterLevels(provider string, result *ResourcePoolResult, claudeSources bool, now time.Time) (
	levels map[string]operation_setting.ResourcePoolAlertState,
	unknown func(key string, state operation_setting.ResourcePoolAlertState) bool,
) {
	windowed := provider != model.ResourcePoolClaude
	perSource := windowed || claudeSources
	bySource := make(map[int64][]*model.ResourcePoolAccount)
	byGroup := make(map[string][]*model.ResourcePoolAccount)
	for _, account := range result.Accounts {
		bySource[account.SourceId] = append(bySource[account.SourceId], account)
		if !windowed {
			group := resourcePoolAccountGroup(account, account.SourceGroup)
			byGroup[group] = append(byGroup[group], account)
		}
	}

	levels = make(map[string]operation_setting.ResourcePoolAlertState)
	failed := make(map[string]bool)
	for _, source := range result.Sources {
		key := resourcePoolAlertKey(provider, resourcePoolAlertKindSource, strconv.FormatInt(source.SourceId, 10))
		if source.Status == "failed" {
			failed[key] = true
		}
		if source.Status != "success" || !perSource {
			continue
		}
		if level, ok := resourcePoolLevel(bySource[source.SourceId], windowed, now); ok {
			levels[key] = operation_setting.ResourcePoolAlertState{
				Provider: provider,
				Kind:     resourcePoolAlertKindSource,
				Name:     cmp.Or(source.SourceName, source.SourceEndpoint),
				Level:    level,
			}
		}
	}
	if !windowed && len(failed) == 0 {
		if level, ok := resourcePoolLevel(result.Accounts, false, now); ok {
			levels[resourcePoolAlertKey(provider, resourcePoolAlertKindPool, "all")] = operation_setting.ResourcePoolAlertState{
				Provider: provider,
				Kind:     resourcePoolAlertKindPool,
				Level:    level,
			}
		}
		for group, accounts := range byGroup {
			level, _ := resourcePoolLevel(accounts, false, now)
			levels[resourcePoolAlertKey(provider, resourcePoolAlertKindGroup, group)] = operation_setting.ResourcePoolAlertState{
				Provider: provider,
				Kind:     resourcePoolAlertKindGroup,
				Name:     group,
				Level:    level,
			}
		}
	}
	unknown = func(key string, state operation_setting.ResourcePoolAlertState) bool {
		if state.Kind == resourcePoolAlertKindSource {
			return perSource && failed[key]
		}
		return len(failed) > 0
	}
	return levels, unknown
}

// resourcePoolAlertChange is one target that crossed its threshold.
type resourcePoolAlertChange struct {
	operation_setting.ResourcePoolAlertState
	Threshold float64 `json:"threshold"`
}

// CheckResourcePoolWaterLevels compares every monitored target with its
// provider's threshold and notifies the WeCom group bot about the targets
// that went above it and the ones that came back below it. A target stays
// silent while it remains above the threshold. The alert state is saved only
// after the notification went out, so a failed send is retried by the next
// check.
func CheckResourcePoolWaterLevels(ctx context.Context) (map[string]any, error) {
	setting := operation_setting.GetResourcePoolAlertSetting()
	now := time.Now()
	state := setting.State
	if state == nil {
		state = make(map[string]operation_setting.ResourcePoolAlertState)
	}
	var exceeded, recovered []resourcePoolAlertChange
	var failedProviders []string
	checked, dropped := 0, 0

	for _, provider := range model.ResourcePoolProviders {
		rule := setting.Rule(provider)
		var levels map[string]operation_setting.ResourcePoolAlertState
		unknown := func(string, operation_setting.ResourcePoolAlertState) bool { return false }
		if rule.Enabled {
			result, err := GetResourcePoolUsage(provider, true)
			if err != nil {
				// Without a poll nothing is known; keep the provider's state.
				failedProviders = append(failedProviders, provider)
				continue
			}
			levels, unknown = resourcePoolWaterLevels(provider, result, setting.ClaudeSourceEnabled, now)
		}
		checked += len(levels)
		for _, key := range slices.Sorted(maps.Keys(levels)) {
			target := levels[key]
			_, alerting := state[key]
			switch {
			case target.Level >= rule.Threshold && !alerting:
				target.Since = now.Unix()
				state[key] = target
				exceeded = append(exceeded, resourcePoolAlertChange{target, rule.Threshold})
			case target.Level < rule.Threshold && alerting:
				delete(state, key)
				recovered = append(recovered, resourcePoolAlertChange{target, rule.Threshold})
			}
		}
		// A target that is gone (source removed, no accounts left, rule or
		// Claude channel monitoring switched off) is dropped without a recovery notice; one that could
		// not be polled is kept.
		for key, target := range state {
			if _, ok := levels[key]; ok || target.Provider != provider || unknown(key, target) {
				continue
			}
			delete(state, key)
			dropped++
		}
	}

	summary := map[string]any{
		"checked":   checked,
		"alerting":  len(state),
		"exceeded":  exceeded,
		"recovered": recovered,
	}
	if len(failedProviders) > 0 {
		summary["failed_providers"] = failedProviders
	}
	if len(exceeded)+len(recovered) > 0 {
		for _, content := range resourcePoolAlertMessages(exceeded, recovered, now) {
			if err := SendWeComGroupBotText(ctx, setting.WebhookURL, content, setting.Mentions); err != nil {
				return summary, err
			}
		}
	}
	if len(exceeded)+len(recovered)+dropped > 0 {
		stateJSON, err := common.Marshal(state)
		if err != nil {
			return summary, err
		}
		if err := model.UpdateOption(operation_setting.ResourcePoolAlertOptionPrefix+"state", string(stateJSON)); err != nil {
			return summary, err
		}
	}
	return summary, nil
}

// resourcePoolAlertMessages renders the notification, split so that every
// message stays within the WeCom text limit.
func resourcePoolAlertMessages(exceeded []resourcePoolAlertChange, recovered []resourcePoolAlertChange, now time.Time) []string {
	header := i18n.Translate("", i18n.MsgResourcePoolAlertHeader, map[string]any{
		"Site": common.SystemName,
		"Time": now.Format(time.DateTime),
	})
	line := func(key string, change resourcePoolAlertChange) string {
		targetKey := i18n.MsgResourcePoolAlertTargetSource
		switch {
		case change.Kind == resourcePoolAlertKindPool:
			targetKey = i18n.MsgResourcePoolAlertTargetPool
		case change.Kind == resourcePoolAlertKindGroup:
			targetKey = i18n.MsgResourcePoolAlertTargetGroup
		case change.Provider != model.ResourcePoolClaude:
			targetKey = i18n.MsgResourcePoolAlertTargetWindow
		}
		target := i18n.Translate("", targetKey, map[string]any{
			"Provider": cmp.Or(resourcePoolAlertProviderLabels[change.Provider], change.Provider),
			"Name":     cmp.Or(change.Name, "-"),
		})
		return i18n.Translate("", key, map[string]any{
			"Target":    target,
			"Level":     fmt.Sprintf("%.1f", change.Level),
			"Threshold": strconv.FormatFloat(change.Threshold, 'f', -1, 64),
		})
	}
	lines := make([]string, 0, len(exceeded)+len(recovered))
	for _, change := range exceeded {
		lines = append(lines, line(i18n.MsgResourcePoolAlertExceeded, change))
	}
	for _, change := range recovered {
		lines = append(lines, line(i18n.MsgResourcePoolAlertRecovered, change))
	}

	messages := []string{header}
	for _, text := range lines {
		last := len(messages) - 1
		if len(messages[last])+1+len(text) > weComGroupBotTextLimit && messages[last] != header {
			messages = append(messages, header)
			last++
		}
		messages[last] += "\n" + text
	}
	return messages
}
