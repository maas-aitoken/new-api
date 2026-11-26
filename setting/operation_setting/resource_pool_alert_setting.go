package operation_setting

import (
	"maps"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	// ResourcePoolAlertOptionPrefix prefixes every option key of this setting.
	ResourcePoolAlertOptionPrefix = "resource_pool_alert."

	DefaultResourcePoolAlertThreshold   = 80.0
	DefaultResourcePoolAlertNotifyStart = "09:00"
	DefaultResourcePoolAlertNotifyEnd   = "21:00"
)

// ResourcePoolAlertRule is the water level threshold of one provider.
type ResourcePoolAlertRule struct {
	Enabled   bool    `json:"enabled"`
	Threshold float64 `json:"threshold"`
}

// ResourcePoolAlertState is one target that was reported as above its
// threshold and has not been reported as recovered yet.
type ResourcePoolAlertState struct {
	Provider string  `json:"provider"`
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	Level    float64 `json:"level"`
	Since    int64   `json:"since"`
}

// ResourcePoolAlertSetting configures the hourly water level check of the
// resource pools and its WeCom group bot notifications. State is written by
// the check itself, not by the console.
type ResourcePoolAlertSetting struct {
	Enabled bool `json:"enabled"`
	// WebhookURL is the full WeCom group bot URL, key included.
	WebhookURL string `json:"webhook_url"`
	// Mentions lists mobile numbers, user IDs or @all, separated by commas.
	Mentions string `json:"mentions"`
	// NotifyStart / NotifyEnd bound the daily period ("HH:MM", server time
	// zone) in which the check runs. Equal values mean all day.
	NotifyStart string                           `json:"notify_start"`
	NotifyEnd   string                           `json:"notify_end"`
	Rules       map[string]ResourcePoolAlertRule `json:"rules"`
	// ClaudeSourceEnabled also checks every Claude channel on its own. Off
	// by default: Claude is checked as a whole and per account group.
	ClaudeSourceEnabled bool                              `json:"claude_source_enabled"`
	State               map[string]ResourcePoolAlertState `json:"state"`
}

var resourcePoolAlertSetting = ResourcePoolAlertSetting{
	NotifyStart: DefaultResourcePoolAlertNotifyStart,
	NotifyEnd:   DefaultResourcePoolAlertNotifyEnd,
	Rules:       map[string]ResourcePoolAlertRule{},
	State:       map[string]ResourcePoolAlertState{},
}

func init() {
	config.GlobalConfig.Register("resource_pool_alert", &resourcePoolAlertSetting)
}

// GetResourcePoolAlertSetting returns a copy of the setting. The option sync
// replaces the maps under OptionMapRWMutex, so the copy is taken under it.
func GetResourcePoolAlertSetting() ResourcePoolAlertSetting {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	setting := resourcePoolAlertSetting
	setting.Rules = maps.Clone(setting.Rules)
	setting.State = maps.Clone(setting.State)
	if _, ok := ParseResourcePoolAlertClock(setting.NotifyStart); !ok {
		setting.NotifyStart = DefaultResourcePoolAlertNotifyStart
	}
	if _, ok := ParseResourcePoolAlertClock(setting.NotifyEnd); !ok {
		setting.NotifyEnd = DefaultResourcePoolAlertNotifyEnd
	}
	return setting
}

// Rule returns the provider's rule, with the default threshold when none or
// an invalid one is stored.
func (s ResourcePoolAlertSetting) Rule(provider string) ResourcePoolAlertRule {
	rule := s.Rules[provider]
	if rule.Threshold <= 0 || rule.Threshold > 100 {
		rule.Threshold = DefaultResourcePoolAlertThreshold
	}
	return rule
}

// InNotifyWindow reports whether now falls in the daily notification period.
// Both ends are inclusive, and a start after the end crosses midnight.
func (s ResourcePoolAlertSetting) InNotifyWindow(now time.Time) bool {
	start, startOk := ParseResourcePoolAlertClock(s.NotifyStart)
	end, endOk := ParseResourcePoolAlertClock(s.NotifyEnd)
	if !startOk || !endOk || start == end {
		return true
	}
	minute := now.Hour()*60 + now.Minute()
	if start < end {
		return minute >= start && minute <= end
	}
	return minute >= start || minute <= end
}

// ParseResourcePoolAlertClock parses "HH:MM" into minutes since midnight.
func ParseResourcePoolAlertClock(value string) (int, bool) {
	at, err := time.Parse("15:04", value)
	if err != nil {
		return 0, false
	}
	return at.Hour()*60 + at.Minute(), true
}
