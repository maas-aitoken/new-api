package model

import (
	"encoding/json"
	"errors"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// costMultiplierKeyMigratedKey 记录渠道 setting.cost_multiplier 的键已经从"映射后的上游名"
// 转换成"用户请求的模型名"。转换只能做一次：转换后的键再按旧规则读一遍会被当成上游名，
// 结构上分不出新旧，所以靠这个标记防止重跑。值是完成时间（Unix 秒），由服务端写入，
// 不进 OptionMap，选项接口也不能改它。
const costMultiplierKeyMigratedKey = "CostMultiplierKeyMigratedAt"

var errCostMultiplierKeyMigratedReadOnly = errors.New(costMultiplierKeyMigratedKey + " is managed by the server")

// MigrateCostMultiplierKeysToRequestModel 把各渠道 setting.cost_multiplier 的键从映射后的
// 上游名换成渠道模型列表里的请求名，只在主节点启动时跑一次。
//
// 每个请求名取能力页升级前显示的那个值：先按旧的展示规则（只解析一跳 model_mapping）找，
// 找不到再按旧的记录规则（沿 model_mapping 走到链尾）找。后者覆盖的是旧版渠道抽屉按链尾名
// 填的倍率：页面上看不到，但记录 cost_actual 时一直在生效，不能在转换时丢掉。
// 对不上任何请求名的旧键直接丢弃，它们既不显示也匹配不到请求。
// 所有渠道和完成标记在同一个事务里提交：失败就整体不改，下次启动重试。只改 setting 里的
// cost_multiplier，其余字段的值原样保留（JSON 键的顺序会变）；改过的渠道把改前改后的值
// 写进日志，回滚旧版时照日志恢复。
func MigrateCostMultiplierKeysToRequestModel() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	var migrated int64
	if err := DB.Model(&Option{}).Where(&Option{Key: costMultiplierKeyMigratedKey}).Count(&migrated).Error; err != nil {
		return err
	}
	if migrated > 0 {
		return nil
	}

	var changes []string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var rows []struct {
			Id           int
			Models       string
			ModelMapping *string
			Setting      *string
		}
		if err := lockForUpdate(tx).Model(&Channel{}).
			Select("id", "models", "model_mapping", "setting").
			Where("setting LIKE ?", "%cost_multiplier%").
			Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if row.Setting == nil {
				continue
			}
			setting := map[string]json.RawMessage{}
			if err := common.UnmarshalJsonStr(*row.Setting, &setting); err != nil {
				common.SysError(common.LogText("cost multiplier key migration skipped channel #%d: invalid setting: %v", row.Id, err))
				continue
			}
			raw, ok := setting["cost_multiplier"]
			if !ok {
				continue
			}
			old := map[string]float64{}
			if err := common.Unmarshal(raw, &old); err != nil {
				common.SysError(common.LogText("cost multiplier key migration skipped channel #%d: invalid cost_multiplier: %v", row.Id, err))
				continue
			}
			modelMappingRaw := ""
			if row.ModelMapping != nil {
				modelMappingRaw = *row.ModelMapping
			}
			// 旧展示规则只看一跳映射；映射解析失败时旧代码按请求名本身查。
			oneHop := map[string]string{}
			if strings.TrimSpace(modelMappingRaw) != "" {
				_ = common.UnmarshalJsonStr(modelMappingRaw, &oneHop)
			}
			converted := map[string]float64{}
			for _, model := range strings.Split(row.Models, ",") {
				if model == "" {
					continue
				}
				shownKey := model
				if target := strings.TrimSpace(oneHop[model]); target != "" {
					shownKey = target
				}
				if multiplier, found := old[shownKey]; found {
					converted[model] = multiplier
					continue
				}
				if recordedKey, _, mapErr := ResolveMappedModel(modelMappingRaw, model); mapErr == nil {
					if multiplier, found := old[recordedKey]; found {
						converted[model] = multiplier
					}
				}
			}
			if maps.Equal(old, converted) {
				continue
			}
			if len(converted) == 0 {
				delete(setting, "cost_multiplier")
			} else {
				encodedMultipliers, err := common.Marshal(converted)
				if err != nil {
					return err
				}
				setting["cost_multiplier"] = encodedMultipliers
			}
			encoded, err := common.Marshal(setting)
			if err != nil {
				return err
			}
			if err := tx.Model(&Channel{}).Where("id = ?", row.Id).Update("setting", string(encoded)).Error; err != nil {
				return err
			}
			before, _ := common.Marshal(old)
			after, _ := common.Marshal(converted)
			changes = append(changes, common.LogText("cost multiplier keys migrated for channel #%d: before=%s after=%s", row.Id, before, after))
		}
		marker := Option{Key: costMultiplierKeyMigratedKey, Value: strconv.FormatInt(time.Now().Unix(), 10)}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error
	})
	if err != nil {
		return err
	}
	for _, line := range changes {
		common.SysLog(line)
	}
	if len(changes) > 0 {
		common.SysLog(common.LogText("cost multiplier key migration finished: %d channels changed", len(changes)))
	}
	return nil
}
