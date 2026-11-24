package model

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/capability"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// modelNameRuleExactMigratedKey 记录非精确模型规则（前缀/包含/后缀）已完成一次性展开迁移。
// 迁移会把旧规则命中的具体渠道模型与已定价模型拆成各自独立的完全匹配条目（name_rule=0），
// 为空能力的已有精确条目回填原本继承到的规则能力，并清理无渠道提供的纯规则词干行。
const modelNameRuleExactMigratedKey = "ModelNameRuleExactMigratedAt"

var errModelNameRuleExactMigratedReadOnly = errors.New(modelNameRuleExactMigratedKey + " is managed by the server")

var modelPricingOptionKeysForMigration = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
	"billing_setting.billing_mode",
	"billing_setting.billing_expr",
}

func legacyRuleMatchesName(ruleName string, rule int, concreteName string) bool {
	switch rule {
	case NameRulePrefix:
		return strings.HasPrefix(concreteName, ruleName)
	case NameRuleSuffix:
		return strings.HasSuffix(concreteName, ruleName)
	case NameRuleContains:
		return strings.Contains(concreteName, ruleName)
	default:
		return concreteName == ruleName
	}
}

// legacyResolveMetadataForMigration 保留迁移前的元数据最长匹配优先级（精确 → 前缀 → 后缀 → 包含），
// 只用于把历史非精确规则展开到具体模型名。
func legacyResolveMetadataForMigration(records []Model, names []string) map[string]*Model {
	resolved := make(map[string]*Model, len(names))
	for i := range records {
		if records[i].NameRule == NameRuleExact {
			resolved[records[i].ModelName] = &records[i]
		}
	}
	for _, rule := range []int{NameRulePrefix, NameRuleSuffix, NameRuleContains} {
		matchedLength := make(map[string]int, len(names))
		best := make(map[string]*Model, len(names))
		for i := range records {
			metadata := &records[i]
			if metadata.NameRule != rule {
				continue
			}
			for _, name := range names {
				if _, exists := resolved[name]; exists {
					continue
				}
				if !legacyRuleMatchesName(metadata.ModelName, metadata.NameRule, name) {
					continue
				}
				if length, ok := matchedLength[name]; ok && length >= len(metadata.ModelName) {
					continue
				}
				matchedLength[name] = len(metadata.ModelName)
				best[name] = metadata
			}
		}
		for name, metadata := range best {
			resolved[name] = metadata
		}
	}
	return resolved
}

// legacyResolveCapabilityForMigration 保留迁移前的能力声明最长匹配优先级，跳过空或非法能力声明。
// 这样已有精确元数据行（例如官方同步创建、自身 capabilities 为空）在取消规则归并时也能拿到原本生效的能力声明。
func legacyResolveCapabilityForMigration(records []Model, names []string) map[string]*Model {
	valid := make([]Model, 0, len(records))
	for i := range records {
		raw := strings.TrimSpace(records[i].Capabilities)
		if raw == "" {
			continue
		}
		if _, problems := capability.Parse(raw); len(problems) > 0 {
			continue
		}
		valid = append(valid, records[i])
	}
	return legacyResolveMetadataForMigration(valid, names)
}

func collectConcreteModelNamesForMigration(tx *gorm.DB) (map[string]struct{}, error) {
	concrete := make(map[string]struct{})
	addName := func(raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			concrete[trimmed] = struct{}{}
		}
	}

	var channels []struct {
		Models string
	}
	if err := lockForUpdate(tx).Model(&Channel{}).Select("models").Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, ch := range channels {
		for part := range strings.SplitSeq(ch.Models, ",") {
			addName(part)
		}
	}

	var pricingOptions []Option
	if err := tx.Model(&Option{}).Where(commonKeyCol+" IN ?", modelPricingOptionKeysForMigration).Find(&pricingOptions).Error; err != nil {
		return nil, err
	}
	for _, opt := range pricingOptions {
		if strings.TrimSpace(opt.Value) == "" {
			continue
		}
		entries := make(map[string]any)
		if err := common.UnmarshalJsonStr(opt.Value, &entries); err != nil {
			continue
		}
		for modelName := range entries {
			addName(modelName)
		}
	}

	var narrowedModels []string
	if err := tx.Model(&ChannelModelCapability{}).Distinct("model").Pluck("model", &narrowedModels).Error; err != nil {
		return nil, err
	}
	for _, modelName := range narrowedModels {
		addName(modelName)
	}

	return concrete, nil
}

// MigrateModelNameRulesToExact 将历史前缀/包含/后缀模型规则一次性展开为具体模型的独立完全匹配条目，
// 只在主节点启动时执行一次。
func MigrateModelNameRulesToExact() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	var migrated int64
	if err := DB.Model(&Option{}).Where(&Option{Key: modelNameRuleExactMigratedKey}).Count(&migrated).Error; err != nil {
		return err
	}
	if migrated > 0 {
		return nil
	}

	var createdCount, convertedCount, backfilledCount, deletedCount int
	err := metadataTransaction(func(tx *gorm.DB) error {
		var check int64
		if err := tx.Model(&Option{}).Where(&Option{Key: modelNameRuleExactMigratedKey}).Count(&check).Error; err != nil {
			return err
		}
		if check > 0 {
			return nil
		}

		var allModels []Model
		if err := lockForUpdate(tx).Order("id").Find(&allModels).Error; err != nil {
			return err
		}

		hasRuleRows := slices.ContainsFunc(allModels, func(m Model) bool {
			return m.NameRule != NameRuleExact
		})
		if !hasRuleRows {
			marker := Option{Key: modelNameRuleExactMigratedKey, Value: strconv.FormatInt(time.Now().Unix(), 10)}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error
		}

		concreteSet, err := collectConcreteModelNamesForMigration(tx)
		if err != nil {
			return err
		}

		candidateSet := make(map[string]struct{}, len(concreteSet)+len(allModels))
		for name := range concreteSet {
			candidateSet[name] = struct{}{}
		}
		byName := make(map[string]*Model, len(allModels))
		for i := range allModels {
			byName[allModels[i].ModelName] = &allModels[i]
			if allModels[i].NameRule == NameRuleExact {
				candidateSet[allModels[i].ModelName] = struct{}{}
			}
		}

		candidateNames := make([]string, 0, len(candidateSet))
		for name := range candidateSet {
			candidateNames = append(candidateNames, name)
		}
		slices.Sort(candidateNames)

		resolvedMeta := legacyResolveMetadataForMigration(allModels, candidateNames)
		resolvedCap := legacyResolveCapabilityForMigration(allModels, candidateNames)
		now := common.GetTimestamp()

		// 1. 已有精确行但自身没有写 capabilities 时，回填原本从模糊规则继承到的能力声明。
		for i := range allModels {
			row := &allModels[i]
			if row.NameRule != NameRuleExact || strings.TrimSpace(row.Capabilities) != "" {
				continue
			}
			capSource := resolvedCap[row.ModelName]
			if capSource == nil || capSource.Id == row.Id {
				continue
			}
			updates := map[string]any{
				"capabilities": capSource.Capabilities,
				"updated_time": now,
			}
			if row.ContextLength == 0 && capSource.ContextLength > 0 {
				updates["context_length"] = capSource.ContextLength
			}
			if err := tx.Model(&Model{}).Where("id = ?", row.Id).Updates(updates).Error; err != nil {
				return err
			}
			backfilledCount++
		}

		// 2. 处理原有的非精确规则行：若该名称本身就是实际在用/已定价的具体模型则原地转为完全匹配，否则收集删除。
		staleRuleIDs := make([]int, 0)
		for i := range allModels {
			row := &allModels[i]
			if row.NameRule == NameRuleExact {
				continue
			}
			if _, usedAsConcrete := concreteSet[row.ModelName]; usedAsConcrete {
				updates := map[string]any{
					"name_rule":    NameRuleExact,
					"updated_time": now,
				}
				if strings.TrimSpace(row.Capabilities) == "" {
					if capSource := resolvedCap[row.ModelName]; capSource != nil && capSource.Id != row.Id {
						updates["capabilities"] = capSource.Capabilities
						if row.ContextLength == 0 && capSource.ContextLength > 0 {
							updates["context_length"] = capSource.ContextLength
						}
					}
				}
				if err := tx.Model(&Model{}).Where("id = ?", row.Id).Updates(updates).Error; err != nil {
					return err
				}
				convertedCount++
				continue
			}
			staleRuleIDs = append(staleRuleIDs, row.Id)
		}

		// 3. 为尚无独立记录但原先命中非精确规则的具体模型复制生成独立完全匹配条目。
		concreteNames := make([]string, 0, len(concreteSet))
		for name := range concreteSet {
			concreteNames = append(concreteNames, name)
		}
		slices.Sort(concreteNames)
		for _, name := range concreteNames {
			if _, exists := byName[name]; exists {
				continue
			}
			metaSource := resolvedMeta[name]
			capSource := resolvedCap[name]
			if metaSource == nil && capSource == nil {
				continue
			}
			created := Model{
				ModelName:    name,
				NameRule:     NameRuleExact,
				Status:       1,
				SyncOfficial: 0,
				CreatedTime:  now,
				UpdatedTime:  now,
			}
			if metaSource != nil {
				created.Description = metaSource.Description
				created.Icon = metaSource.Icon
				created.Tags = metaSource.Tags
				created.VendorID = metaSource.VendorID
				created.Endpoints = metaSource.Endpoints
				created.Capabilities = metaSource.Capabilities
				created.ContextLength = metaSource.ContextLength
				created.ParameterCount = metaSource.ParameterCount
				created.ReleaseDate = metaSource.ReleaseDate
				created.Status = metaSource.Status
			}
			if strings.TrimSpace(created.Capabilities) == "" && capSource != nil {
				created.Capabilities = capSource.Capabilities
				if created.ContextLength == 0 && capSource.ContextLength > 0 {
					created.ContextLength = capSource.ContextLength
				}
			}
			desiredStatus := created.Status
			if err := tx.Create(&created).Error; err != nil {
				return err
			}
			// Model 上的 gorm:"default:1" 会在零值时写入 1，这里显式写回真实 status 与 sync_official=0。
			if err := tx.Model(&Model{}).Where("id = ?", created.Id).Updates(map[string]any{
				"status":        desiredStatus,
				"sync_official": 0,
				"name_rule":     NameRuleExact,
			}).Error; err != nil {
				return err
			}
			createdCount++
		}

		// 4. 删除不再对应任何具体模型的纯模糊规则行。
		if len(staleRuleIDs) > 0 {
			if err := tx.Where("id IN ?", staleRuleIDs).Delete(&Model{}).Error; err != nil {
				return err
			}
			deletedCount = len(staleRuleIDs)
		}

		marker := Option{Key: modelNameRuleExactMigratedKey, Value: strconv.FormatInt(time.Now().Unix(), 10)}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error
	})
	if err != nil {
		return err
	}

	if createdCount > 0 || convertedCount > 0 || backfilledCount > 0 || deletedCount > 0 {
		_ = InitCapabilityCache()
		InvalidatePricingCache()
		common.SysLog(common.LogText(
			"model name rule migration finished: created=%d converted=%d backfilled=%d deleted=%d",
			createdCount, convertedCount, backfilledCount, deletedCount,
		))
	}
	return nil
}
