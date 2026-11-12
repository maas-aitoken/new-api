package model

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	NameRuleExact = iota
	NameRulePrefix
	NameRuleContains
	NameRuleSuffix
)

type ModelSquareState string

const (
	ModelSquareVisible     ModelSquareState = "visible"
	ModelSquareUnavailable ModelSquareState = "unavailable"
	ModelSquareHidden      ModelSquareState = "hidden"
	ModelSquarePartial     ModelSquareState = "partial"
)

type BoundChannel struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type int    `json:"type"`
}

type Model struct {
	Id                 int            `json:"id"`
	ModelName          string         `json:"model_name" gorm:"size:128;not null;uniqueIndex:uk_model_name_delete_at,priority:1"`
	Description        string         `json:"description,omitempty" gorm:"type:text"`
	Icon               string         `json:"icon,omitempty" gorm:"type:varchar(128)"`
	Tags               string         `json:"tags,omitempty" gorm:"type:varchar(255)"`
	VendorID           int            `json:"vendor_id,omitempty" gorm:"index"`
	Endpoints          string         `json:"endpoints,omitempty" gorm:"type:text"`
	Capabilities       string         `json:"capabilities,omitempty" gorm:"type:text"`
	SupportedEndpoints []string       `json:"supported_endpoints,omitempty" gorm:"-"`
	ContextLength      int64          `json:"context_length,omitempty" gorm:"bigint"`
	ParameterCount     string         `json:"parameter_count,omitempty" gorm:"type:varchar(64)"`
	ReleaseDate        string         `json:"release_date,omitempty" gorm:"type:varchar(32)"`
	Status             int            `json:"status" gorm:"default:1"`
	SyncOfficial       int            `json:"sync_official" gorm:"default:1"`
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	UpdatedTime        int64          `json:"updated_time" gorm:"bigint"`
	DeletedAt          gorm.DeletedAt `json:"-" gorm:"index;uniqueIndex:uk_model_name_delete_at,priority:2"`

	BoundChannels []BoundChannel `json:"bound_channels,omitempty" gorm:"-"`
	EnableGroups  []string       `json:"enable_groups,omitempty" gorm:"-"`
	QuotaTypes    []int          `json:"quota_types,omitempty" gorm:"-"`
	NameRule      int            `json:"name_rule" gorm:"default:0"`

	MatchedModels []string `json:"matched_models,omitempty" gorm:"-"`
	MatchedCount  int      `json:"matched_count,omitempty" gorm:"-"`

	HasMetadata            bool             `json:"has_metadata" gorm:"-"`
	ConfiguredChannelCount int              `json:"configured_channel_count" gorm:"-"`
	SquareState            ModelSquareState `json:"square_state" gorm:"-"`
}

// MatchesName applies a metadata rule to a concrete channel model name.
func (mi *Model) MatchesName(name string) bool {
	switch mi.NameRule {
	case NameRulePrefix:
		return strings.HasPrefix(name, mi.ModelName)
	case NameRuleSuffix:
		return strings.HasSuffix(name, mi.ModelName)
	case NameRuleContains:
		return strings.Contains(name, mi.ModelName)
	default:
		return name == mi.ModelName
	}
}

// resolveModelMetadata 保持与能力解析相同的优先级：精确 → 前缀 → 后缀 → 包含，
// 同一规则内以更长的匹配键优先。这样同一个模型名的元数据来源与能力声明来源一致。
func resolveModelMetadata(records []Model, names []string) map[string]*Model {
	resolved := make(map[string]*Model)
	for i := range records {
		if records[i].NameRule == NameRuleExact {
			resolved[records[i].ModelName] = &records[i]
		}
	}
	for _, rule := range []int{NameRulePrefix, NameRuleSuffix, NameRuleContains} {
		matchedLength := make(map[string]int, len(names))
		// 本轮规则的最长匹配先记在 best 里，等整轮扫描完再合并。
		// 直接写 resolved 会让先遍历到的短前缀占位，后续更长的前缀被
		// "已被解析" 的判断挡掉，导致 claude-opus-5-5 落到 claude-opus-5 上。
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
				if !metadata.MatchesName(name) {
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

// ResolveModelMetadataByName maps concrete model names to the declaration row
// that governs them (exact → prefix → suffix → contains, longest key first).
// Callers that start from live routes, such as the public model square, can use
// it to pick up metadata declared by a rule row instead of requiring an exact row.
func ResolveModelMetadataByName(names []string) (map[string]*Model, error) {
	var records []Model
	if err := DB.Find(&records).Error; err != nil {
		return nil, err
	}
	return resolveModelMetadata(records, names), nil
}

// FillModelSquareStates applies the same metadata policy as the public catalog
// to live routes. A rule row only reports and aggregates the models it actually
// governs (MatchedModels), so names with their own exact declaration are not
// counted twice across rows.
func FillModelSquareStates(rows []*Model, configured map[string][]int, connections []ModelConnection) error {
	var metadata []Model
	if err := DB.Find(&metadata).Error; err != nil {
		return err
	}
	nameSet := make(map[string]struct{}, len(configured))
	for name := range configured {
		nameSet[name] = struct{}{}
	}
	for _, row := range rows {
		if row != nil && row.NameRule == NameRuleExact {
			nameSet[row.ModelName] = struct{}{}
		}
	}
	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}
	resolved := resolveModelMetadata(metadata, names)
	available := make(map[string]bool)
	for _, connection := range connections {
		available[connection.Model] = true
	}
	states := make(map[string]ModelSquareState, len(names))
	for _, name := range names {
		state := ModelSquareUnavailable
		if policy := resolved[name]; policy != nil && policy.Status != 1 {
			state = ModelSquareHidden
		} else if available[name] {
			state = ModelSquareVisible
		}
		states[name] = state
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		if row.NameRule == NameRuleExact {
			row.SquareState = states[row.ModelName]
			continue
		}
		total, visible, hidden := 0, 0, 0
		governed := make([]string, 0)
		for name := range configured {
			// 只认这条规则真正兜底的模型：已有精确行或更具体的规则命中时，
			// 那个模型不归本行，渠道数也不再重复统计。
			if policy := resolved[name]; policy != nil && policy.Id == row.Id {
				governed = append(governed, name)
			}
		}
		sort.Strings(governed)
		row.MatchedModels = governed
		row.MatchedCount = len(governed)
		for _, name := range governed {
			total++
			switch states[name] {
			case ModelSquareVisible:
				visible++
			case ModelSquareHidden:
				hidden++
			}
		}
		row.SquareState = ModelSquareUnavailable
		switch {
		case total == 0:
			if row.Status != 1 {
				row.SquareState = ModelSquareHidden
			}
		case hidden == total:
			row.SquareState = ModelSquareHidden
		case visible == total:
			row.SquareState = ModelSquareVisible
		case visible > 0:
			row.SquareState = ModelSquarePartial
		}
	}
	return nil
}

// GetConfiguredModelChannels includes disabled channels and reads no credentials.
func GetConfiguredModelChannels() (map[string][]int, error) {
	var channels []Channel
	if err := DB.Select("id", "models").Find(&channels).Error; err != nil {
		return nil, err
	}
	configured := make(map[string][]int)
	for _, channel := range channels {
		for _, name := range normalizeLookupValues(channel.GetModels()) {
			configured[name] = append(configured[name], channel.Id)
		}
	}
	return configured, nil
}

// SearchModelsWithChannels augments metadata with concrete configured names.
// Synthetic rows never persist and never affect the public pricing catalog.
func SearchModelsWithChannels(keyword, vendor, status, syncOfficial string, offset, limit int) ([]*Model, int64, error) {
	records, _, err := SearchModels(keyword, vendor, status, syncOfficial, 0, -1)
	if err != nil {
		return nil, 0, err
	}
	_, filterStatus := parseModelStatusFilter(status)
	_, filterSync := parseModelSyncFilter(syncOfficial)
	if !filterStatus && !filterSync && (vendor == "" || vendor == "0") {
		records, err = appendChannelOnlyModels(records, keyword)
		if err != nil {
			return nil, 0, err
		}
	}
	total := len(records)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []*Model{}, int64(total), nil
	}
	end := total
	if limit >= 0 && limit < total-offset {
		end = offset + limit
	}
	return records[offset:end], int64(total), nil
}

// appendChannelOnlyModels adds one synthetic row per served model name that no
// declaration covers. A name matched by a rule row belongs to that row (see
// MatchedModels), so it never becomes a sibling row; when the keyword hits such
// a name, its rule row is kept in the result instead of returning nothing.
func appendChannelOnlyModels(records []*Model, keyword string) ([]*Model, error) {
	configured, err := GetConfiguredModelChannels()
	if err != nil {
		return nil, err
	}
	if len(configured) == 0 {
		return records, nil
	}
	var declarations []Model
	if err := DB.Order("id").Find(&declarations).Error; err != nil {
		return nil, err
	}
	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)
	resolved := resolveModelMetadata(declarations, names)

	listed := make(map[int]bool, len(records))
	for _, record := range records {
		if record != nil {
			listed[record.Id] = true
		}
	}
	lowerKeyword := strings.ToLower(keyword)
	for _, name := range names {
		if keyword != "" && !strings.Contains(strings.ToLower(name), lowerKeyword) {
			continue
		}
		declaration := resolved[name]
		if declaration == nil {
			records = append(records, &Model{ModelName: name, NameRule: NameRuleExact})
			continue
		}
		if listed[declaration.Id] {
			continue
		}
		listed[declaration.Id] = true
		records = append(records, declaration)
	}
	return records, nil
}

func (mi *Model) Insert() error {
	err := metadataTransaction(func(tx *gorm.DB) error {
		if err := validateModelVendor(tx, mi.VendorID); err != nil {
			return err
		}
		now := common.GetTimestamp()
		mi.CreatedTime, mi.UpdatedTime = now, now
		status, syncOfficial := mi.Status, mi.SyncOfficial
		if err := tx.Create(mi).Error; err != nil {
			return err
		}
		mi.Status, mi.SyncOfficial = status, syncOfficial
		return tx.Model(&Model{}).Where("id = ?", mi.Id).Updates(map[string]any{"status": status, "sync_official": syncOfficial}).Error
	})
	if err == nil {
		_ = InitCapabilityCache()
	}
	return err
}

func IsModelNameDuplicated(id int, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	var cnt int64
	err := DB.Model(&Model{}).Where("model_name = ? AND id <> ?", name, id).Count(&cnt).Error
	return cnt > 0, err
}

func (mi *Model) Update() error {
	err := metadataTransaction(func(tx *gorm.DB) error {
		if err := validateModelVendor(tx, mi.VendorID); err != nil {
			return err
		}
		mi.UpdatedTime = common.GetTimestamp()
		return tx.Model(&Model{}).Where("id = ?", mi.Id).
			Select("model_name", "description", "icon", "tags", "vendor_id", "endpoints", "capabilities", "context_length", "parameter_count", "release_date", "status", "sync_official", "name_rule", "updated_time").Updates(mi).Error
	})
	if err == nil {
		_ = InitCapabilityCache()
	}
	return err
}

func (mi *Model) Delete() error {
	_, err := DeleteModelMetadata([]int{mi.Id}, false, false)
	return err
}

type ModelDeleteResult struct {
	DeletedCount    int `json:"deleted_count"`
	UpdatedChannels int `json:"updated_channels"`
}

// DeleteModelMetadata optionally removes exact model names from every channel.
// Channel removal requires exact-match metadata records. Pricing removal
// clears the selected names without expanding metadata matching rules.
func DeleteModelMetadata(ids []int, removeFromChannels, removePricing bool) (ModelDeleteResult, error) {
	result := ModelDeleteResult{}
	if len(ids) == 0 || len(ids) > 1000 {
		return result, errors.New("select between 1 and 1000 models")
	}
	selected := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return result, errors.New("invalid model ID")
		}
		selected[id] = struct{}{}
	}
	modelIDs := make([]int, 0, len(selected))
	for id := range selected {
		modelIDs = append(modelIDs, id)
	}
	sort.Ints(modelIDs)
	names := make(map[string]struct{}, len(modelIDs))
	deleteRecords := func(tx *gorm.DB) error {
		var records []Model
		if err := lockForUpdate(tx).Where("id IN ?", modelIDs).Order("id").Find(&records).Error; err != nil {
			return err
		}
		if len(records) != len(modelIDs) {
			return errors.New("selected models changed; reload before deleting")
		}
		for _, record := range records {
			if removeFromChannels && record.NameRule != NameRuleExact {
				return errors.New("only exact-match models can be removed from channels")
			}
			names[record.ModelName] = struct{}{}
		}
		if removeFromChannels {
			var channels []Channel
			// Read only routing fields. Lock channels in a consistent order, then
			// update models and abilities in the same transaction as metadata.
			if err := lockForUpdate(tx).Select("id", "models", "status", "group", "priority", "weight", "tag").Order("id").Find(&channels).Error; err != nil {
				return err
			}
			for _, channel := range channels {
				models := channel.GetModels()
				remaining := make([]string, 0, len(models))
				for _, name := range models {
					if _, remove := names[strings.TrimSpace(name)]; !remove {
						remaining = append(remaining, name)
					}
				}
				if len(remaining) == len(models) {
					continue
				}
				channel.Models = strings.Join(remaining, ",")
				if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Update("models", channel.Models).Error; err != nil {
					return err
				}
				if err := channel.UpdateAbilities(tx); err != nil {
					return err
				}
				result.UpdatedChannels++
			}
		}
		if err := tx.Where("id IN ?", modelIDs).Delete(&Model{}).Error; err != nil {
			return err
		}
		result.DeletedCount = len(records)
		return nil
	}
	var err error
	if removePricing {
		// Use the pricing mutation path so both option persistence and runtime
		// publication stay serialized with ordinary pricing saves. All database
		// writes share one transaction; runtime prices publish only after commit.
		metadataMutationMu.Lock()
		defer metadataMutationMu.Unlock()
		err = mutateModelPricingOptions(func(tx *gorm.DB, values map[string]map[string]any) error {
			if err := lockMetadataMutation(tx); err != nil {
				return err
			}
			if err := deleteRecords(tx); err != nil {
				return err
			}
			for _, entries := range values {
				for name := range names {
					delete(entries, name)
				}
			}
			return nil
		})
	} else {
		err = metadataTransaction(deleteRecords)
	}
	if err != nil {
		return ModelDeleteResult{}, err
	}
	if result.UpdatedChannels > 0 {
		InitChannelCache()
	}
	_ = InitCapabilityCache()
	RefreshPricing()
	return result, nil
}

func GetVendorModelCounts() (map[int64]int64, error) {
	var stats []struct {
		VendorID int64
		Count    int64
	}
	if err := DB.Model(&Model{}).
		Select("vendor_id as vendor_id, count(*) as count").
		Group("vendor_id").
		Scan(&stats).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(stats))
	for _, s := range stats {
		m[s.VendorID] = s.Count
	}
	return m, nil
}

func GetAllModels(offset int, limit int) ([]*Model, error) {
	models, _, err := SearchModels("", "", "", "", offset, limit)
	return models, err
}

func GetBoundChannelsByModelsMap(modelNames []string) (map[string][]BoundChannel, error) {
	result := make(map[string][]BoundChannel)
	if len(modelNames) == 0 {
		return result, nil
	}
	type row struct {
		Model string
		ID    int
		Name  string
		Type  int
	}
	var rows []row
	err := excludeDisabledChannelModels(DB.Table("channels")).
		Select("abilities.model as model, channels.id as id, channels.name as name, channels.type as type").
		Joins("JOIN abilities ON abilities.channel_id = channels.id").
		Where("abilities.model IN ? AND abilities.enabled = ?", modelNames, true).
		Distinct().
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		result[r.Model] = append(result[r.Model], BoundChannel{ID: r.ID, Name: r.Name, Type: r.Type})
	}
	return result, nil
}

// ModelConnection describes an enabled route independently of catalog visibility or price.
type ModelConnection struct {
	AbilityWithChannel
	ChannelName string `json:"channel_name"`
}

func GetModelConnections() ([]ModelConnection, error) {
	var connections []ModelConnection
	err := excludeDisabledChannelModels(DB.Table("abilities")).
		Select("abilities.*, channels.type as channel_type, channels.name as channel_name").
		Joins("JOIN channels ON abilities.channel_id = channels.id").
		Where("abilities.enabled = ? AND channels.status = ?", true, common.ChannelStatusEnabled).
		Order("abilities.model, abilities.channel_id").
		Scan(&connections).Error
	return connections, err
}

func normalizeLookupValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func GetPreferredModelOwnerChannelTypes(modelNames []string, groups []string) (map[string]int, error) {
	result := make(map[string]int)
	modelNames = normalizeLookupValues(modelNames)
	if len(modelNames) == 0 {
		return result, nil
	}

	type row struct {
		Model       string
		ChannelType int
	}
	var rows []row

	query := excludeDisabledChannelModels(DB.Table("abilities")).
		Select("abilities.model as model, channels.type as channel_type").
		Joins("JOIN channels ON abilities.channel_id = channels.id").
		Where("abilities.model IN ? AND abilities.enabled = ? AND channels.status = ?", modelNames, true, common.ChannelStatusEnabled).
		Order("COALESCE(abilities.priority, 0) DESC").
		Order("abilities.weight DESC").
		Order("abilities.channel_id ASC")

	groups = normalizeLookupValues(groups)
	if len(groups) > 0 {
		query = query.Where("abilities."+commonGroupCol+" IN ?", groups)
	}

	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}

	for _, r := range rows {
		if _, ok := result[r.Model]; ok {
			continue
		}
		result[r.Model] = r.ChannelType
	}
	return result, nil
}

func SearchModels(keyword string, vendor string, status string, syncOfficial string, offset int, limit int) ([]*Model, int64, error) {
	var models []*Model
	db := DB.Model(&Model{})
	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("model_name LIKE ? OR description LIKE ? OR tags LIKE ?", like, like, like)
	}
	if vendor != "" {
		if vid, err := strconv.Atoi(vendor); err == nil {
			db = db.Where("models.vendor_id = ?", vid)
		} else {
			db = db.Joins("JOIN vendors ON vendors.id = models.vendor_id").Where("vendors.name LIKE ?", "%"+vendor+"%")
		}
	}
	if statusValue, ok := parseModelStatusFilter(status); ok {
		db = db.Where("models.status = ?", statusValue)
	}
	if syncValue, ok := parseModelSyncFilter(syncOfficial); ok {
		db = db.Where("models.sync_official = ?", syncValue)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Order("models.id DESC").Offset(offset).Limit(limit).Find(&models).Error; err != nil {
		return nil, 0, err
	}
	return models, total, nil
}

// parseModelStatusFilter maps UI/API status values to the models.status column.
// Returns ok=false when no status filter should be applied.
func parseModelStatusFilter(status string) (value int, ok bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "all":
		return 0, false
	case "enabled", "1":
		return 1, true
	case "disabled", "0":
		return 0, true
	default:
		n, err := strconv.Atoi(status)
		if err != nil {
			return 0, false
		}
		return n, true
	}
}

// parseModelSyncFilter maps UI/API sync values to the models.sync_official column.
// Returns ok=false when no sync filter should be applied.
func parseModelSyncFilter(syncOfficial string) (value int, ok bool) {
	switch strings.ToLower(strings.TrimSpace(syncOfficial)) {
	case "", "all":
		return 0, false
	case "yes", "1":
		return 1, true
	case "no", "0":
		return 0, true
	default:
		n, err := strconv.Atoi(syncOfficial)
		if err != nil {
			return 0, false
		}
		return n, true
	}
}
