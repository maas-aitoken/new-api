package operation_setting

import (
	"errors"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

// CapabilityGateSetting 是请求能力门控的运行时配置（全部来自系统设置）。
type CapabilityGateSetting struct {
	Mode               string `json:"mode"`
	MaxBodyBytes       int64  `json:"max_body_bytes"`
	EventRetentionDays int    `json:"event_retention_days"`
	// RecordOnlyDimensions 是"只记录不拦截"的维度：可以写维度组（params）
	// 或单个维度（tools.parallel），命中规则同 name_rule 的前缀语义。
	RecordOnlyDimensions string `json:"record_only_dimensions"`
	// RecordPassed 控制是否也记录通过的判定。开启后每个请求都会产生一条事件，
	// 只适合短期排查；默认关闭，只留违规留痕。
	RecordPassed bool `json:"record_passed"`
}

const (
	CapabilityGateModeOff     = "off"
	CapabilityGateModeShadow  = "shadow"
	CapabilityGateModeEnforce = "enforce"

	DefaultCapabilityGateMode               = CapabilityGateModeShadow
	DefaultCapabilityGateMaxBodyBytes       = int64(8 << 20)
	MinCapabilityGateMaxBodyBytes           = int64(64 << 10)
	MaxCapabilityGateMaxBodyBytes           = int64(64 << 20)
	DefaultCapabilityGateEventRetentionDays = 30
	MaxCapabilityGateEventRetentionDays     = 3650
	DefaultCapabilityGateRecordOnly         = "params,tools.parallel"
)

// CapabilityGateDimensionGroups 是可以在设置页切换"拦截/仅记录"的维度。
// 前七项是维度组，最后一项是单个维度。
var CapabilityGateDimensionGroups = []string{
	"context",
	"modalities",
	"tools",
	"structured_output",
	"reasoning",
	"messages",
	"streaming",
	"params",
	"tools.parallel",
}

var capabilityGateSetting = CapabilityGateSetting{
	Mode:                 DefaultCapabilityGateMode,
	MaxBodyBytes:         DefaultCapabilityGateMaxBodyBytes,
	EventRetentionDays:   DefaultCapabilityGateEventRetentionDays,
	RecordOnlyDimensions: DefaultCapabilityGateRecordOnly,
}

func init() {
	config.GlobalConfig.Register("capability_gate_setting", &capabilityGateSetting)
}

// NormalizeCapabilityGateMode 把任意写法收敛到 off/shadow/enforce；无法识别时返回空串。
func NormalizeCapabilityGateMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case CapabilityGateModeOff:
		return CapabilityGateModeOff
	case CapabilityGateModeShadow:
		return CapabilityGateModeShadow
	case CapabilityGateModeEnforce:
		return CapabilityGateModeEnforce
	default:
		return ""
	}
}

// NormalizeCapabilityGateRecordOnlyDimensions 校验并规范化"仅记录维度"清单。
// 只接受 CapabilityGateDimensionGroups 里的条目；空值表示所有维度都拦截。
func NormalizeCapabilityGateRecordOnlyDimensions(raw string) (string, error) {
	seen := make(map[string]struct{})
	items := make([]string, 0)
	for _, token := range strings.Split(raw, ",") {
		value := strings.ToLower(strings.TrimSpace(token))
		if value == "" {
			continue
		}
		if !slices.Contains(CapabilityGateDimensionGroups, value) {
			return "", errors.New("unknown capability gate dimension: " + value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		items = append(items, value)
	}
	return strings.Join(items, ","), nil
}

func GetCapabilityGateSetting() *CapabilityGateSetting {
	if mode := NormalizeCapabilityGateMode(capabilityGateSetting.Mode); mode != "" {
		capabilityGateSetting.Mode = mode
	} else {
		capabilityGateSetting.Mode = DefaultCapabilityGateMode
	}
	if capabilityGateSetting.MaxBodyBytes < MinCapabilityGateMaxBodyBytes {
		capabilityGateSetting.MaxBodyBytes = DefaultCapabilityGateMaxBodyBytes
	} else if capabilityGateSetting.MaxBodyBytes > MaxCapabilityGateMaxBodyBytes {
		capabilityGateSetting.MaxBodyBytes = MaxCapabilityGateMaxBodyBytes
	}
	if capabilityGateSetting.EventRetentionDays < 1 {
		capabilityGateSetting.EventRetentionDays = DefaultCapabilityGateEventRetentionDays
	} else if capabilityGateSetting.EventRetentionDays > MaxCapabilityGateEventRetentionDays {
		capabilityGateSetting.EventRetentionDays = MaxCapabilityGateEventRetentionDays
	}
	if normalized, err := NormalizeCapabilityGateRecordOnlyDimensions(capabilityGateSetting.RecordOnlyDimensions); err == nil {
		capabilityGateSetting.RecordOnlyDimensions = normalized
	} else {
		capabilityGateSetting.RecordOnlyDimensions = DefaultCapabilityGateRecordOnly
	}
	return &capabilityGateSetting
}

// CapabilityGateRecordOnlyDimensions 返回当前的"仅记录"清单。
func CapabilityGateRecordOnlyDimensions() []string {
	setting := GetCapabilityGateSetting()
	if setting.RecordOnlyDimensions == "" {
		return nil
	}
	return strings.Split(setting.RecordOnlyDimensions, ",")
}
