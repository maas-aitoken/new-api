package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"gorm.io/gorm"
)

// CapacityPool 是资源池页上的一个容量池：同一 provider 下若干来源（可只取部分
// 上游分组）的账号，加上单账号速率、阈值、配速和保底额度。绑到它的
// <渠道, 上游模型> 共用一个计数桶，额度由 service.capacity_pool 每分钟按账号状态算出。
//
// 可调参数整体放在 Config（JSON 文本）里，以后加字段不改表结构。
type CapacityPool struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string `json:"name" gorm:"type:varchar(128);not null"`
	Provider    string `json:"provider" gorm:"type:varchar(32);index;not null"`
	Config      string `json:"-" gorm:"type:text"`
	CreatedTime int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime int64  `json:"updated_time" gorm:"bigint"`
}

func (CapacityPool) TableName() string {
	return "capacity_pools"
}

// CapacityPoolConfig 是容量池的可调参数。
type CapacityPoolConfig struct {
	// SourceIds 是账号来源（resource_pool_sources.id），必须属于池的 provider。
	SourceIds []int64 `json:"source_ids"`
	// Groups 只取这些上游分组的账号，空表示来源里的全部账号。
	Groups []string `json:"groups,omitempty"`
	// PlanRates 按套餐（codex2api 的 plan_type、Kiro 的 subscription_type）填单账号速率，
	// 某一项为 0 表示回落到 DefaultRate。
	PlanRates map[string]dto.ModelLimit `json:"plan_rates,omitempty"`
	// DefaultRate 是没列出的套餐用的单账号速率。某一项仍为 0 时，Kiro 的 RPM
	// 用账号自己报的 rpm_limit，其它情况这一项池子不限。
	DefaultRate dto.ModelLimit `json:"default_rate"`
	// ThresholdPercent 是额度用到多少就不再计入可用，默认 95。
	ThresholdPercent float64 `json:"threshold_percent"`
	// PacingEnabled 打开配速：按"剩余额度 ÷ 距重置时间"再压一道上限。
	PacingEnabled bool `json:"pacing_enabled"`
	// BurstFactor 是配速上限的放宽系数，默认 2，允许短时突发。
	BurstFactor float64 `json:"burst_factor"`
	// Floor 是保底额度：动态额度过期（拉取持续失败）后调度器按它准入，0 表示不限。
	Floor dto.ModelLimit `json:"floor"`
	// GraceSeconds 是动态额度的有效期，拉取失败期间沿用上次结果的时长，默认 600。
	GraceSeconds int `json:"grace_seconds"`
}

const (
	defaultCapacityPoolThreshold   = 95
	defaultCapacityPoolBurstFactor = 2
	defaultCapacityPoolGrace       = 600
	maxCapacityPoolBurstFactor     = 100
	maxCapacityPoolGraceSeconds    = 86400
	maxCapacityPoolNameLength      = 128
)

var (
	// ErrCapacityPoolNotFound 是容量池 id 不存在时的错误。
	ErrCapacityPoolNotFound = errors.New("capacity pool not found")
	// ErrModelLimitManagedByCapacityPool 是给已绑容量池的 <渠道, 模型> 改自己额度时的错误；
	// 文案是前端语言包里的键。
	ErrModelLimitManagedByCapacityPool = errors.New("This model's limits follow a capacity pool. Change them on the Resource Pools page.")
)

// Normalize 补上没填的默认值，并去掉分组里的空白和重复。
func (c *CapacityPoolConfig) Normalize() {
	if c.ThresholdPercent == 0 {
		c.ThresholdPercent = defaultCapacityPoolThreshold
	}
	if c.BurstFactor == 0 {
		c.BurstFactor = defaultCapacityPoolBurstFactor
	}
	if c.GraceSeconds == 0 {
		c.GraceSeconds = defaultCapacityPoolGrace
	}
	groups := make([]string, 0, len(c.Groups))
	for _, group := range c.Groups {
		group = strings.TrimSpace(group)
		if group != "" && !slices.Contains(groups, group) {
			groups = append(groups, group)
		}
	}
	slices.Sort(groups)
	c.Groups = groups
	if len(c.PlanRates) == 0 {
		c.PlanRates = nil
	}
}

// Validate 检查取值范围；来源是否属于 provider、账号范围是否与别的池重叠在保存时查。
func (c *CapacityPoolConfig) Validate() error {
	if len(c.SourceIds) == 0 {
		return errors.New("capacity pool needs at least one source")
	}
	if c.ThresholdPercent <= 0 || c.ThresholdPercent > 100 {
		return fmt.Errorf("threshold_percent must be in (0, 100], got %g", c.ThresholdPercent)
	}
	if c.BurstFactor <= 0 || c.BurstFactor > maxCapacityPoolBurstFactor {
		return fmt.Errorf("burst_factor must be in (0, %d], got %g", maxCapacityPoolBurstFactor, c.BurstFactor)
	}
	if c.GraceSeconds < 60 || c.GraceSeconds > maxCapacityPoolGraceSeconds {
		return fmt.Errorf("grace_seconds must be between 60 and %d, got %d", maxCapacityPoolGraceSeconds, c.GraceSeconds)
	}
	for plan, rate := range c.PlanRates {
		if strings.TrimSpace(plan) == "" {
			return errors.New("plan_rates has a blank plan name")
		}
		if err := rate.Validate(); err != nil {
			return fmt.Errorf("plan_rates for plan %s: %w", plan, err)
		}
	}
	if err := c.DefaultRate.Validate(); err != nil {
		return fmt.Errorf("default_rate: %w", err)
	}
	if err := c.Floor.Validate(); err != nil {
		return fmt.Errorf("floor: %w", err)
	}
	return nil
}

// Overlaps 表示两个池会不会取到同一批账号：共用某个来源，且至少一方取全部分组
// 或分组有交集。重叠会让同一账号被两个池各算一遍。
func (c *CapacityPoolConfig) Overlaps(other *CapacityPoolConfig) bool {
	for _, source := range c.SourceIds {
		if !slices.Contains(other.SourceIds, source) {
			continue
		}
		if len(c.Groups) == 0 || len(other.Groups) == 0 {
			return true
		}
		if slices.ContainsFunc(c.Groups, func(group string) bool { return slices.Contains(other.Groups, group) }) {
			return true
		}
	}
	return false
}

// ParseConfig 解析容量池的配置并补默认值；解析失败时返回错误，不带出半成品。
func (p *CapacityPool) ParseConfig() (CapacityPoolConfig, error) {
	var config CapacityPoolConfig
	if strings.TrimSpace(p.Config) != "" {
		if err := common.UnmarshalJsonStr(p.Config, &config); err != nil {
			return CapacityPoolConfig{}, fmt.Errorf("capacity pool #%d config: %w", p.Id, err)
		}
	}
	config.Normalize()
	return config, nil
}

// GetCapacityPools 列出某个 provider 的容量池，provider 为空时列出全部。
func GetCapacityPools(provider string) ([]*CapacityPool, error) {
	query := DB.Model(&CapacityPool{})
	if provider != "" {
		query = query.Where("provider = ?", provider)
	}
	var pools []*CapacityPool
	err := query.Order("provider asc, id asc").Find(&pools).Error
	return pools, err
}

// GetCapacityPoolById 读取一个容量池，不存在时返回 ErrCapacityPoolNotFound。
func GetCapacityPoolById(id int) (*CapacityPool, error) {
	var pool CapacityPool
	err := DB.Where("id = ?", id).First(&pool).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCapacityPoolNotFound
	}
	if err != nil {
		return nil, err
	}
	return &pool, nil
}

// SaveCapacityPool 新建（Id 为 0）或更新一个容量池。来源必须属于池的 provider，
// 账号范围不能与同 provider 的其它池重叠。provider 建好后不能改，免得已绑的成员
// 换了一批完全不同的账号。
func SaveCapacityPool(pool *CapacityPool, config CapacityPoolConfig) error {
	pool.Name = strings.TrimSpace(pool.Name)
	if pool.Name == "" {
		return errors.New("capacity pool name is required")
	}
	if utf8.RuneCountInString(pool.Name) > maxCapacityPoolNameLength {
		return fmt.Errorf("capacity pool name must be at most %d characters", maxCapacityPoolNameLength)
	}
	if !slices.Contains(ResourcePoolProviders, pool.Provider) {
		return fmt.Errorf("unsupported provider %q", pool.Provider)
	}
	config.Normalize()
	if err := config.Validate(); err != nil {
		return err
	}
	sources, err := GetResourcePoolSources(pool.Provider)
	if err != nil {
		return err
	}
	for _, sourceID := range config.SourceIds {
		owned := slices.ContainsFunc(sources, func(source *ResourcePoolSource) bool { return source.Id == sourceID })
		if !owned {
			return fmt.Errorf("source %d does not belong to provider %s", sourceID, pool.Provider)
		}
	}
	others, err := GetCapacityPools(pool.Provider)
	if err != nil {
		return err
	}
	for _, other := range others {
		if other.Id == pool.Id {
			continue
		}
		otherConfig, parseErr := other.ParseConfig()
		if parseErr != nil {
			continue
		}
		if config.Overlaps(&otherConfig) {
			return fmt.Errorf("accounts of this pool overlap with capacity pool %q", other.Name)
		}
	}
	encoded, err := common.Marshal(config)
	if err != nil {
		return err
	}
	pool.Config = string(encoded)
	now := common.GetTimestamp()
	pool.UpdatedTime = now
	if pool.Id == 0 {
		pool.CreatedTime = now
		return DB.Create(pool).Error
	}
	existing, err := GetCapacityPoolById(pool.Id)
	if err != nil {
		return err
	}
	if existing.Provider != pool.Provider {
		return errors.New("the provider of a capacity pool cannot be changed")
	}
	pool.CreatedTime = existing.CreatedTime
	return DB.Model(&CapacityPool{}).Where("id = ?", pool.Id).Updates(map[string]any{
		"name":         pool.Name,
		"config":       pool.Config,
		"updated_time": pool.UpdatedTime,
	}).Error
}

// channelBindingRow 是读写渠道上 capacity_pools 绑定时用到的几列。
type channelBindingRow struct {
	Id           int
	Name         string
	Models       string
	ModelMapping *string
	Setting      *string
}

func (r channelBindingRow) parseSetting() (dto.ChannelSettings, error) {
	setting := dto.ChannelSettings{}
	if r.Setting != nil && strings.TrimSpace(*r.Setting) != "" {
		if err := common.UnmarshalJsonStr(*r.Setting, &setting); err != nil {
			return setting, fmt.Errorf("channel #%d has an invalid setting: %w", r.Id, err)
		}
	}
	return setting, nil
}

func (r channelBindingRow) modelMapping() string {
	if r.ModelMapping == nil {
		return ""
	}
	return *r.ModelMapping
}

// channelsBoundToCapacityPools 读出 setting 里带 capacity_pools 的渠道。
// 用 LIKE 粗筛，三种数据库都支持，再由调用方按解析结果精确判断。
func channelsBoundToCapacityPools(tx *gorm.DB) ([]channelBindingRow, error) {
	var rows []channelBindingRow
	err := tx.Model(&Channel{}).
		Select("id", "name", "models", "model_mapping", "setting").
		Where("setting LIKE ?", "%capacity_pools%").
		Order("id asc").
		Scan(&rows).Error
	return rows, err
}

func writeChannelSetting(tx *gorm.DB, channelID int, setting dto.ChannelSettings) error {
	encoded, err := common.Marshal(setting)
	if err != nil {
		return err
	}
	return tx.Model(&Channel{}).Where("id = ?", channelID).Update("setting", string(encoded)).Error
}

// DeleteCapacityPool 删除容量池，并在同一个事务里清掉所有渠道上指向它的绑定，
// 成员回到各自的 model_limits。
func DeleteCapacityPool(id int) error {
	cleared := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&CapacityPool{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrCapacityPoolNotFound
		}
		rows, err := channelsBoundToCapacityPools(tx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			setting, err := row.parseSetting()
			if err != nil {
				continue
			}
			changed := false
			for upstream, poolID := range setting.CapacityPools {
				if poolID == id {
					delete(setting.CapacityPools, upstream)
					changed = true
				}
			}
			if !changed {
				continue
			}
			if len(setting.CapacityPools) == 0 {
				setting.CapacityPools = nil
			}
			if err := writeChannelSetting(tx, row.Id, setting); err != nil {
				return err
			}
			cleared = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if cleared {
		InitChannelCache()
	}
	return nil
}

// CapacityPoolMember 是容量池里的一个 <渠道, 上游模型>。Models 是渠道上经
// model_mapping 落到这个上游模型的公开名；为空（Stale）表示改过映射后
// 已没有公开名指向它，这条绑定不会命中任何请求，可以移除。
type CapacityPoolMember struct {
	ChannelId     int      `json:"channel_id"`
	ChannelName   string   `json:"channel_name"`
	UpstreamModel string   `json:"upstream_model"`
	Models        []string `json:"models"`
	Stale         bool     `json:"stale"`
}

// GetCapacityPoolMembers 按容量池 id 分组返回所有成员。
func GetCapacityPoolMembers() (map[int][]CapacityPoolMember, error) {
	rows, err := channelsBoundToCapacityPools(DB)
	if err != nil {
		return nil, err
	}
	members := make(map[int][]CapacityPoolMember)
	for _, row := range rows {
		setting, err := row.parseSetting()
		if err != nil || len(setting.CapacityPools) == 0 {
			continue
		}
		publicByUpstream := make(map[string][]string)
		for name := range strings.SplitSeq(row.Models, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			upstream, _, _ := ResolveMappedModel(row.modelMapping(), name)
			publicByUpstream[upstream] = append(publicByUpstream[upstream], name)
		}
		upstreams := make([]string, 0, len(setting.CapacityPools))
		for upstream := range setting.CapacityPools {
			upstreams = append(upstreams, upstream)
		}
		slices.Sort(upstreams)
		for _, upstream := range upstreams {
			poolID := setting.CapacityPools[upstream]
			public := publicByUpstream[upstream]
			members[poolID] = append(members[poolID], CapacityPoolMember{
				ChannelId:     row.Id,
				ChannelName:   row.Name,
				UpstreamModel: upstream,
				Models:        public,
				Stale:         len(public) == 0,
			})
		}
	}
	return members, nil
}

// AddCapacityPoolMembers 把渠道的若干公开模型加进容量池：按 model_mapping 链尾的
// 上游名绑定，映射到同一上游的别名只占一条。publicModels 为空表示渠道的全部模型。
// 已绑在别的池上的上游模型会报错，要先从那个池移除；已在本池的跳过。
// 返回本次新绑定的上游模型。
func AddCapacityPoolMembers(poolID, channelID int, publicModels []string) ([]string, error) {
	if _, err := GetCapacityPoolById(poolID); err != nil {
		return nil, err
	}
	added := make([]string, 0)
	err := DB.Transaction(func(tx *gorm.DB) error {
		var row channelBindingRow
		if err := lockForUpdate(tx).Model(&Channel{}).
			Select("id", "name", "models", "model_mapping", "setting").
			Where("id = ?", channelID).
			Take(&row).Error; err != nil {
			return fmt.Errorf("channel #%d not found", channelID)
		}
		served := make([]string, 0)
		for name := range strings.SplitSeq(row.Models, ",") {
			if name = strings.TrimSpace(name); name != "" {
				served = append(served, name)
			}
		}
		if len(publicModels) == 0 {
			publicModels = served
		}
		setting, err := row.parseSetting()
		if err != nil {
			return err
		}
		for _, public := range publicModels {
			public = strings.TrimSpace(public)
			if !slices.Contains(served, public) {
				return fmt.Errorf("channel #%d does not serve model %q", channelID, public)
			}
			upstream, _, err := ResolveMappedModel(row.modelMapping(), public)
			if err != nil {
				return fmt.Errorf("channel #%d model_mapping: %w", channelID, err)
			}
			current := setting.CapacityPoolFor(upstream)
			if current == poolID {
				continue
			}
			if current != 0 {
				if _, err := GetCapacityPoolById(current); err == nil {
					return fmt.Errorf("upstream model %q of channel #%d is already in capacity pool #%d", upstream, channelID, current)
				}
				// 指向已不存在的池的残留绑定，直接改绑。
			}
			if setting.CapacityPools == nil {
				setting.CapacityPools = make(map[string]int)
			}
			setting.CapacityPools[upstream] = poolID
			added = append(added, upstream)
		}
		if len(added) == 0 {
			return nil
		}
		return writeChannelSetting(tx, channelID, setting)
	})
	if err != nil {
		return nil, err
	}
	if len(added) > 0 {
		InitChannelCache()
	}
	return added, nil
}

// RemoveCapacityPoolMember 从容量池移除一个 <渠道, 上游模型>，它回到自己的 model_limits。
func RemoveCapacityPoolMember(poolID, channelID int, upstreamModel string) error {
	err := DB.Transaction(func(tx *gorm.DB) error {
		var row channelBindingRow
		if err := lockForUpdate(tx).Model(&Channel{}).
			Select("id", "name", "models", "model_mapping", "setting").
			Where("id = ?", channelID).
			Take(&row).Error; err != nil {
			return fmt.Errorf("channel #%d not found", channelID)
		}
		setting, err := row.parseSetting()
		if err != nil {
			return err
		}
		if setting.CapacityPoolFor(upstreamModel) != poolID {
			return fmt.Errorf("upstream model %q of channel #%d is not in capacity pool #%d", upstreamModel, channelID, poolID)
		}
		delete(setting.CapacityPools, upstreamModel)
		if len(setting.CapacityPools) == 0 {
			setting.CapacityPools = nil
		}
		return writeChannelSetting(tx, channelID, setting)
	})
	if err != nil {
		return err
	}
	InitChannelCache()
	return nil
}

// GetCapacityPoolFloors 返回每个容量池的保底额度，供调度器 Catalog 给成员填 Limits。
// 配置解析失败的池按不限处理。
func GetCapacityPoolFloors() (map[int]dto.ModelLimit, error) {
	pools, err := GetCapacityPools("")
	if err != nil {
		return nil, err
	}
	floors := make(map[int]dto.ModelLimit, len(pools))
	for _, pool := range pools {
		config, err := pool.ParseConfig()
		if err != nil {
			floors[pool.Id] = dto.ModelLimit{}
			continue
		}
		floors[pool.Id] = config.Floor
	}
	return floors, nil
}
