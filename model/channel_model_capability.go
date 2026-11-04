package model

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service/capability"

	"gorm.io/gorm"
)

// ChannelModelCapability 是某个渠道对某个模型的能力收窄。
// 键是渠道声明的公开模型名（管理员在渠道 Models 里勾的名字），不是映射后的上游名。
type ChannelModelCapability struct {
	ChannelId    int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false"`
	Model        string `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	Capabilities string `json:"capabilities" gorm:"type:text"`
	UpdatedTime  int64  `json:"updated_time" gorm:"bigint"`
}

func (ChannelModelCapability) TableName() string {
	return "channel_model_capabilities"
}

type modelCapabilityRecord struct {
	id   int
	name string
	rule int
	raw  string
	spec capability.Spec
}

var capabilityCache struct {
	sync.RWMutex
	initialized bool
	exact       map[string]modelCapabilityRecord
	prefix      []modelCapabilityRecord
	suffix      []modelCapabilityRecord
	contains    []modelCapabilityRecord
	channel     map[int]map[string]capability.Spec
}

// InitCapabilityCache 重新加载模型级声明与渠道收窄。写入路径在提交事务后调用。
func InitCapabilityCache() error {
	if DB == nil {
		return nil
	}
	var models []Model
	if err := DB.Select("id", "model_name", "name_rule", "capabilities").Order("id").Find(&models).Error; err != nil {
		return err
	}
	exact := make(map[string]modelCapabilityRecord)
	var prefix, suffix, contains []modelCapabilityRecord
	for _, metadata := range models {
		spec, problems := capability.Parse(metadata.Capabilities)
		if len(problems) > 0 {
			common.SysError(fmt.Sprintf("model capability declaration ignored: model=%s id=%d: %s", metadata.ModelName, metadata.Id, strings.Join(problems, "; ")))
			continue
		}
		if strings.TrimSpace(metadata.Capabilities) == "" {
			continue
		}
		record := modelCapabilityRecord{
			id:   metadata.Id,
			name: metadata.ModelName,
			rule: metadata.NameRule,
			raw:  metadata.Capabilities,
			spec: spec,
		}
		switch metadata.NameRule {
		case NameRulePrefix:
			prefix = append(prefix, record)
		case NameRuleSuffix:
			suffix = append(suffix, record)
		case NameRuleContains:
			contains = append(contains, record)
		default:
			exact[metadata.ModelName] = record
		}
	}

	var rows []ChannelModelCapability
	if err := DB.Find(&rows).Error; err != nil {
		return err
	}
	channel := make(map[int]map[string]capability.Spec)
	for _, row := range rows {
		spec, problems := capability.Parse(row.Capabilities)
		if len(problems) > 0 {
			common.SysError(fmt.Sprintf("channel capability declaration ignored: channel=%d model=%s: %s", row.ChannelId, row.Model, strings.Join(problems, "; ")))
			continue
		}
		if channel[row.ChannelId] == nil {
			channel[row.ChannelId] = make(map[string]capability.Spec)
		}
		channel[row.ChannelId][row.Model] = spec
	}

	capabilityCache.Lock()
	capabilityCache.exact = exact
	capabilityCache.prefix = prefix
	capabilityCache.suffix = suffix
	capabilityCache.contains = contains
	capabilityCache.channel = channel
	capabilityCache.initialized = true
	capabilityCache.Unlock()
	return nil
}

// InvalidateCapabilityCache 清空解析结果；数据库被外部直接修改后可调用。
func InvalidateCapabilityCache() {
	capabilityCache.Lock()
	capabilityCache.initialized = false
	capabilityCache.exact = nil
	capabilityCache.prefix = nil
	capabilityCache.suffix = nil
	capabilityCache.contains = nil
	capabilityCache.channel = nil
	capabilityCache.Unlock()
}

// HasCapabilityDeclarations 报告是否至少有一条模型级能力声明。
// 没有声明时门控整体关闭，避免 default shadow 下白解析每个请求体。
func HasCapabilityDeclarations() bool {
	capabilityCache.RLock()
	defer capabilityCache.RUnlock()
	return capabilityCache.initialized &&
		(len(capabilityCache.exact) > 0 || len(capabilityCache.prefix) > 0 ||
			len(capabilityCache.suffix) > 0 || len(capabilityCache.contains) > 0)
}

// ResolveModelCapability 按 name_rule 的优先级（精确、前缀、后缀、包含）取模型级声明。
// 同一规则内以更长的匹配键优先，这样 claude-sonnet-4-5 的具体声明不会被
// claude-sonnet-4 的家族默认声明先命中。
func ResolveModelCapability(modelName string) (capability.Spec, bool) {
	record, ok := resolveModelCapabilityRecord(modelName)
	return record.spec, ok
}

// ResolveModelCapabilityDeclaration 返回命中的声明来源（模型行 ID 与原始 JSON）。
func ResolveModelCapabilityDeclaration(modelName string) (int, string, bool) {
	record, ok := resolveModelCapabilityRecord(modelName)
	return record.id, record.raw, ok
}

func resolveModelCapabilityRecord(modelName string) (modelCapabilityRecord, bool) {
	capabilityCache.RLock()
	defer capabilityCache.RUnlock()
	if !capabilityCache.initialized {
		return modelCapabilityRecord{}, false
	}
	return resolveCapabilityRecordIn(capabilityCache.exact, capabilityCache.prefix, capabilityCache.suffix, capabilityCache.contains, modelName)
}

// resolveCapabilityRecordIn 是纯解析：精确 → 前缀 → 后缀 → 包含，同一规则内最长键优先。
// 缓存解析和"保存前校验"共用它，避免两处规则漂移。
func resolveCapabilityRecordIn(exact map[string]modelCapabilityRecord, prefix, suffix, contains []modelCapabilityRecord, modelName string) (modelCapabilityRecord, bool) {
	if record, ok := exact[modelName]; ok {
		return record, true
	}
	for _, records := range [][]modelCapabilityRecord{prefix, suffix, contains} {
		var matched *modelCapabilityRecord
		for i := range records {
			if !metadataMatchesName(records[i].name, records[i].rule, modelName) {
				continue
			}
			if matched == nil || len(records[i].name) > len(matched.name) {
				matched = &records[i]
			}
		}
		if matched != nil {
			return *matched, true
		}
	}
	return modelCapabilityRecord{}, false
}

func metadataMatchesName(name string, rule int, modelName string) bool {
	switch rule {
	case NameRulePrefix:
		return strings.HasPrefix(modelName, name)
	case NameRuleSuffix:
		return strings.HasSuffix(modelName, name)
	case NameRuleContains:
		return strings.Contains(modelName, name)
	default:
		return modelName == name
	}
}

// GetChannelModelCapability 返回渠道层收窄声明；没有记录时 ok=false。
func GetChannelModelCapability(channelID int, modelName string) (capability.Spec, bool) {
	capabilityCache.RLock()
	defer capabilityCache.RUnlock()
	if !capabilityCache.initialized {
		return capability.Spec{}, false
	}
	spec, ok := capabilityCache.channel[channelID][modelName]
	return spec, ok
}

// EffectiveChannelCapability 返回模型层声明被渠道收窄后的生效能力。
// 没有渠道记录时直接返回模型层声明；模型层未声明时 ok=false。
func EffectiveChannelCapability(channelID int, modelName string) (capability.Spec, bool) {
	base, ok := ResolveModelCapability(modelName)
	if !ok {
		return capability.Spec{}, false
	}
	over, hasOver := GetChannelModelCapability(channelID, modelName)
	if !hasOver {
		return base, true
	}
	return capability.Merge(base, over), true
}

// ValidateModelCapabilities 校验模型级声明自身是否合法。
func ValidateModelCapabilities(raw string) error {
	_, problems := capability.Parse(raw)
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

// ValidateChannelModelCapabilities 校验渠道收窄：声明自身合法，且不越出模型层。
func ValidateChannelModelCapabilities(modelName string, raw string) error {
	spec, problems := capability.Parse(raw)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	base, ok := ResolveModelCapability(modelName)
	if !ok {
		return fmt.Errorf("model %q has no model-level capability declaration", modelName)
	}
	if violations := capability.CheckSubset(base, spec); len(violations) > 0 {
		return errors.New(strings.Join(violations, "; "))
	}
	return nil
}

// ValidateModelCapabilityNarrowings 校验"保存这条模型级声明之后"，所有以它为
// 声明来源的渠道收窄仍然落在它的值域内。
//
// 收窄只在保存收窄时校验一次子集关系；模型级后来被收紧时，存量收窄可能已经越界。
// 这里在保存模型级之前按"保存后"的声明归属重新解析，一旦越界就拒绝保存并列出
// 冲突的渠道与维度，避免运行期出现"渠道比模型级更宽"的配置。
func ValidateModelCapabilityNarrowings(pending Model) error {
	if strings.TrimSpace(pending.Capabilities) == "" {
		return nil
	}
	spec, problems := capability.Parse(pending.Capabilities)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}

	var narrowings []ChannelModelCapability
	if err := DB.Select("channel_id", "model", "capabilities").Order("channel_id, model").Find(&narrowings).Error; err != nil {
		return err
	}
	if len(narrowings) == 0 {
		return nil
	}

	var declarations []Model
	if err := DB.Select("id", "model_name", "name_rule", "capabilities").Order("id").Find(&declarations).Error; err != nil {
		return err
	}
	exact := make(map[string]modelCapabilityRecord)
	var prefix, suffix, contains []modelCapabilityRecord
	collect := func(record Model, parsed capability.Spec) {
		item := modelCapabilityRecord{id: record.Id, name: record.ModelName, rule: record.NameRule, raw: record.Capabilities, spec: parsed}
		switch record.NameRule {
		case NameRulePrefix:
			prefix = append(prefix, item)
		case NameRuleSuffix:
			suffix = append(suffix, item)
		case NameRuleContains:
			contains = append(contains, item)
		default:
			exact[record.ModelName] = item
		}
	}
	for _, declaration := range declarations {
		if declaration.Id == pending.Id || strings.TrimSpace(declaration.Capabilities) == "" {
			continue
		}
		parsed, declarationProblems := capability.Parse(declaration.Capabilities)
		if len(declarationProblems) > 0 {
			continue
		}
		collect(declaration, parsed)
	}
	collect(pending, spec)

	conflicts := make([]string, 0)
	for _, narrowing := range narrowings {
		record, ok := resolveCapabilityRecordIn(exact, prefix, suffix, contains, narrowing.Model)
		if !ok || record.id != pending.Id {
			continue
		}
		over, narrowingProblems := capability.Parse(narrowing.Capabilities)
		if len(narrowingProblems) > 0 {
			conflicts = append(conflicts, fmt.Sprintf("channel #%d / %s: stored narrowing is invalid: %s", narrowing.ChannelId, narrowing.Model, strings.Join(narrowingProblems, "; ")))
			continue
		}
		if violations := capability.CheckSubset(spec, over); len(violations) > 0 {
			conflicts = append(conflicts, fmt.Sprintf("channel #%d / %s: %s", narrowing.ChannelId, narrowing.Model, strings.Join(violations, "; ")))
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	if len(conflicts) > 8 {
		conflicts = append(conflicts[:8], fmt.Sprintf("and %d more", len(conflicts)-8))
	}
	return fmt.Errorf("model-level declaration would no longer cover existing channel narrowings: %s", strings.Join(conflicts, "; "))
}

// SetChannelModelCapability 保存一条渠道收窄并刷新缓存。
func SetChannelModelCapability(channelID int, modelName string, raw string) error {
	if channelID <= 0 || strings.TrimSpace(modelName) == "" {
		return errors.New("channel_id and model are required")
	}
	channel, err := GetChannelById(channelID, false)
	if err != nil || channel == nil {
		return fmt.Errorf("channel #%d not found", channelID)
	}
	if !slices.Contains(channel.GetModels(), modelName) {
		return fmt.Errorf("channel #%d does not serve model %q", channelID, modelName)
	}
	if err := ValidateChannelModelCapabilities(modelName, raw); err != nil {
		return err
	}
	row := ChannelModelCapability{
		ChannelId:    channelID,
		Model:        modelName,
		Capabilities: strings.TrimSpace(raw),
		UpdatedTime:  common.GetTimestamp(),
	}
	if err := DB.Save(&row).Error; err != nil {
		return err
	}
	return InitCapabilityCache()
}

// DeleteChannelModelCapability 删除一条渠道收窄并刷新缓存。
func DeleteChannelModelCapability(channelID int, modelName string) error {
	if channelID <= 0 || strings.TrimSpace(modelName) == "" {
		return errors.New("channel_id and model are required")
	}
	if err := DB.Where("channel_id = ? AND model = ?", channelID, modelName).
		Delete(&ChannelModelCapability{}).Error; err != nil {
		return err
	}
	return InitCapabilityCache()
}

// CapabilityTargetChannel 是某个模型的一条可配置渠道。
type CapabilityTargetChannel struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Type       int    `json:"type"`
	Configured bool   `json:"configured"`
	// CostMultiplier 是渠道对该模型的费用倍率，0 表示未配置（按 1 处理）。
	CostMultiplier float64 `json:"cost_multiplier"`
	// UpstreamModel 是该模型走完 model_mapping 链后真正发给上游的名字，
	// ModelLimit 按它存放：指向同一上游的别名共用一份额度。
	UpstreamModel string         `json:"upstream_model"`
	ModelLimit    dto.ModelLimit `json:"model_limit"`
}

// CapabilityTarget 是"模型 → 服务渠道"清单的一行。
type CapabilityTarget struct {
	ID              int    `json:"id"`
	ModelName       string `json:"model_name"`
	DeclarationName string `json:"declaration_name"`
	NameRule        int    `json:"name_rule"`
	Capabilities    string `json:"capabilities,omitempty"`
	// Declared 为 false 表示没有任何模型级声明命中该模型，只能配置渠道倍率。
	Declared bool                      `json:"declared"`
	Channels []CapabilityTargetChannel `json:"channels"`
}

// ChannelCostMultiplier 返回渠道对某个公开模型配置的费用倍率，未配置时返回 0。
// 倍率的键是 model_mapping 映射后的上游模型名，所以这里先解析映射。
func ChannelCostMultiplier(settingRaw, modelMappingRaw, publicModel string) float64 {
	setting := dto.ChannelSettings{}
	if strings.TrimSpace(settingRaw) != "" {
		if err := common.UnmarshalJsonStr(settingRaw, &setting); err != nil {
			return 0
		}
	}
	multiplier, ok := setting.CostMultiplierMap[resolveUpstreamModelName(modelMappingRaw, publicModel)]
	if !ok || multiplier <= 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return 0
	}
	return multiplier
}

// resolveUpstreamModelName 返回渠道实际向上游发送的模型名。
func resolveUpstreamModelName(modelMappingRaw, publicModel string) string {
	if strings.TrimSpace(modelMappingRaw) == "" {
		return publicModel
	}
	mapping := make(map[string]string)
	if err := common.UnmarshalJsonStr(modelMappingRaw, &mapping); err != nil {
		return publicModel
	}
	if upstream := strings.TrimSpace(mapping[publicModel]); upstream != "" {
		return upstream
	}
	return publicModel
}

// SetChannelModelCostMultiplier 写入渠道对一个公开模型的费用倍率。
// multiplier<=0 表示清除，回到未配置（按 1 处理）。倍率乘数只影响
// relay_attempts 记录的成本，不参与计费。
func SetChannelModelCostMultiplier(channelID int, publicModel string, multiplier float64) error {
	publicModel = strings.TrimSpace(publicModel)
	if channelID <= 0 || publicModel == "" {
		return errors.New("channel_id and model are required")
	}
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier < 0 {
		return errors.New("cost multiplier must be a positive number")
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var row struct {
			Models       string
			ModelMapping *string
			Setting      *string
		}
		if err := lockForUpdate(tx).Model(&Channel{}).
			Select("models", "model_mapping", "setting").
			Where("id = ?", channelID).
			Take(&row).Error; err != nil {
			return fmt.Errorf("channel #%d not found", channelID)
		}
		served := slices.ContainsFunc(strings.Split(row.Models, ","), func(model string) bool {
			return strings.TrimSpace(model) == publicModel
		})
		if !served {
			return fmt.Errorf("channel #%d does not serve model %q", channelID, publicModel)
		}
		setting := dto.ChannelSettings{}
		if row.Setting != nil && strings.TrimSpace(*row.Setting) != "" {
			if err := common.UnmarshalJsonStr(*row.Setting, &setting); err != nil {
				return fmt.Errorf("invalid channel setting: %w", err)
			}
		}
		modelMapping := ""
		if row.ModelMapping != nil {
			modelMapping = *row.ModelMapping
		}
		upstream := resolveUpstreamModelName(modelMapping, publicModel)
		if multiplier > 0 {
			if setting.CostMultiplierMap == nil {
				setting.CostMultiplierMap = make(map[string]float64)
			}
			setting.CostMultiplierMap[upstream] = multiplier
		} else {
			delete(setting.CostMultiplierMap, upstream)
			if len(setting.CostMultiplierMap) == 0 {
				setting.CostMultiplierMap = nil
			}
		}
		encoded, err := common.Marshal(setting)
		if err != nil {
			return err
		}
		raw := string(encoded)
		return tx.Model(&Channel{}).Where("id = ?", channelID).Update("setting", raw).Error
	})
	if err != nil {
		return err
	}
	InitChannelCache()
	return nil
}

// CapabilityTargetQuery 是能力设置页的筛选与分页参数。Limit<=0 表示不分页。
type CapabilityTargetQuery struct {
	Keyword   string
	ChannelID int
	Offset    int
	Limit     int
}

// GetCapabilityTargets 返回所有能解析到模型级声明的渠道模型及其服务渠道。
// 精确行即使暂无渠道也会列出；前缀/通配行则通过渠道声明的具体模型名展开，
// 这样管理员可以直接给 deepseek-v4-pro 这类具体模型配置渠道收窄。
// 第二个返回值是筛选后、分页前的总数。
func GetCapabilityTargets(query CapabilityTargetQuery) ([]CapabilityTarget, int, error) {
	var exactNames []string
	if err := DB.Model(&Model{}).Where("name_rule = ?", NameRuleExact).Pluck("model_name", &exactNames).Error; err != nil {
		return nil, 0, err
	}

	var rows []struct {
		Model        string
		ID           int
		Name         string
		Type         int
		ModelMapping *string
		Setting      *string
	}
	err := DB.Table("abilities").
		Select("abilities.model as model, channels.id as id, channels.name as name, channels.type as type, channels.model_mapping as model_mapping, channels.setting as setting").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where("abilities.enabled = ? AND channels.status = ?", true, common.ChannelStatusEnabled).
		Distinct().
		Order("abilities.model, channels.id").
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	channelsByModel := make(map[string][]CapabilityTargetChannel)
	names := make(map[string]struct{}, len(exactNames)+len(rows))
	for _, name := range exactNames {
		names[name] = struct{}{}
	}
	for _, row := range rows {
		names[row.Model] = struct{}{}
		modelMapping := ""
		if row.ModelMapping != nil {
			modelMapping = *row.ModelMapping
		}
		setting := ""
		if row.Setting != nil {
			setting = *row.Setting
		}
		limit, upstream := ChannelModelLimitOf(setting, modelMapping, row.Model)
		channelsByModel[row.Model] = append(channelsByModel[row.Model], CapabilityTargetChannel{
			ID:             row.ID,
			Name:           row.Name,
			Type:           row.Type,
			CostMultiplier: ChannelCostMultiplier(setting, modelMapping, row.Model),
			UpstreamModel:  upstream,
			ModelLimit:     limit,
		})
	}

	configuredRows, err := GetConfiguredModelCapabilityKeys()
	if err != nil {
		return nil, 0, err
	}
	modelNames := make([]string, 0, len(names))
	for name := range names {
		modelNames = append(modelNames, name)
	}
	sort.Strings(modelNames)

	keyword := strings.ToLower(strings.TrimSpace(query.Keyword))
	targets := make([]CapabilityTarget, 0, len(modelNames))
	for _, name := range modelNames {
		record, declared := resolveModelCapabilityRecord(name)
		if keyword != "" && !strings.Contains(strings.ToLower(name), keyword) {
			continue
		}
		channels := channelsByModel[name]
		if query.ChannelID > 0 && !slices.ContainsFunc(channels, func(channel CapabilityTargetChannel) bool {
			return channel.ID == query.ChannelID
		}) {
			continue
		}
		if channels == nil {
			channels = []CapabilityTargetChannel{}
		}
		for i := range channels {
			channels[i].Configured = configuredRows[ChannelModelCapabilityKey{ChannelId: channels[i].ID, Model: name}]
		}
		targets = append(targets, CapabilityTarget{
			ID:              record.id,
			ModelName:       name,
			DeclarationName: record.name,
			NameRule:        record.rule,
			Capabilities:    record.raw,
			Declared:        declared,
			Channels:        channels,
		})
	}

	total := len(targets)
	if query.Offset > 0 {
		if query.Offset >= len(targets) {
			targets = []CapabilityTarget{}
		} else {
			targets = targets[query.Offset:]
		}
	}
	if query.Limit > 0 && query.Limit < len(targets) {
		targets = targets[:query.Limit]
	}
	return targets, total, nil
}

// ChannelModelCapabilityKey 标识一条渠道收窄记录。
type ChannelModelCapabilityKey struct {
	ChannelId int
	Model     string
}

// GetConfiguredModelCapabilityKeys 返回已有收窄记录的键集合。
func GetConfiguredModelCapabilityKeys() (map[ChannelModelCapabilityKey]bool, error) {
	var rows []ChannelModelCapability
	if err := DB.Select("channel_id", "model").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[ChannelModelCapabilityKey]bool, len(rows))
	for _, row := range rows {
		out[ChannelModelCapabilityKey{ChannelId: row.ChannelId, Model: row.Model}] = true
	}
	return out, nil
}

// pruneChannelModelCapabilitiesLocked 删除渠道已不再服务的模型对应的收窄记录。
// 调用方负责在事务内执行，并在提交后调用 InitCapabilityCache。
func pruneChannelModelCapabilitiesLocked(tx *gorm.DB, channelID int, modelNames []string) error {
	keep := make(map[string]struct{}, len(modelNames))
	for _, name := range modelNames {
		keep[name] = struct{}{}
	}
	var rows []ChannelModelCapability
	if err := tx.Select("channel_id", "model").Where("channel_id = ?", channelID).Find(&rows).Error; err != nil {
		return err
	}
	stale := make([]string, 0)
	for _, row := range rows {
		if _, ok := keep[row.Model]; !ok {
			stale = append(stale, row.Model)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	return tx.Where("channel_id = ? AND model IN ?", channelID, stale).
		Delete(&ChannelModelCapability{}).Error
}
