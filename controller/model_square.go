package controller

import (
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/capability"

	"github.com/gin-gonic/gin"
)

// modelSquarePricing 是模型级 token 单价，单位为美元/百万 token，由倍率换算得到。
// 按次计价的模型（quota_type=1）没有 token 单价，输入和输出都填每次调用的价格。
type modelSquarePricing struct {
	InputPrice     float64  `json:"input_price"`
	OutputPrice    float64  `json:"output_price"`
	CacheReadPrice *float64 `json:"cache_read_price,omitempty"`
}

type modelSquareItem struct {
	ID                     int                            `json:"id"`
	ModelName              string                         `json:"model_name"`
	CatalogSlug            string                         `json:"catalog_slug"`
	Description            string                         `json:"description,omitempty"`
	Icon                   string                         `json:"icon,omitempty"`
	Tags                   string                         `json:"tags,omitempty"`
	VendorID               int                            `json:"vendor_id,omitempty"`
	VendorName             string                         `json:"vendor_name,omitempty"`
	VendorIcon             string                         `json:"vendor_icon,omitempty"`
	VendorDescription      string                         `json:"vendor_description,omitempty"`
	ContextLength          int64                          `json:"context_length,omitempty"`
	ParameterCount         string                         `json:"parameter_count,omitempty"`
	ReleaseDate            string                         `json:"release_date,omitempty"`
	QuotaType              int                            `json:"quota_type"`
	ModelRatio             float64                        `json:"model_ratio"`
	ModelPrice             float64                        `json:"model_price"`
	CompletionRatio        float64                        `json:"completion_ratio"`
	CacheRatio             *float64                       `json:"cache_ratio,omitempty"`
	CreateCacheRatio       *float64                       `json:"create_cache_ratio,omitempty"`
	EnableGroup            []string                       `json:"enable_groups"`
	SupportedEndpointTypes []string                       `json:"supported_endpoint_types"`
	EndpointMap            map[string]common.EndpointInfo `json:"endpoint_map,omitempty"`
	BillingMode            string                         `json:"billing_mode,omitempty"`
	BillingExpr            string                         `json:"billing_expr,omitempty"`
	Pricing                *modelSquarePricing            `json:"pricing,omitempty"`

	// 能力摘要来自模型级 capabilities 声明；未声明时全部为空。
	Capabilities     []string               `json:"capabilities,omitempty"`
	InputModalities  []string               `json:"input_modalities,omitempty"`
	OutputModalities []string               `json:"output_modalities,omitempty"`
	MaxOutputTokens  *int64                 `json:"max_output_tokens,omitempty"`
	Parameters       []capability.Parameter `json:"parameters,omitempty"`

	// CatalogKey 是模型广场详情页使用的路由键。它按具体模型名编码，
	// 这样命中同一条前缀/后缀声明的多个模型也能各自有稳定的详情地址；
	// 编码后不含 "/"，模型名里带斜杠（如 vendor/model）也能作为单个路径段。
	CatalogKey         string                  `json:"catalog_key"`
	Channels           []modelSquareChannel    `json:"channels,omitempty"`
	BasePricing        *modelSquareBasePricing `json:"basePricing,omitempty"`
	MaxInputTokens     *int64                  `json:"maxInputTokens,omitempty"`
	CapabilityDeclared bool                    `json:"capabilityDeclared"`
}

type modelSquareBasePricing struct {
	SourceModel    string                     `json:"sourceModel"`
	Effective      model.PricingValues        `json:"effective"`
	CacheWriteMode model.CacheWriteMode       `json:"cacheWriteMode,omitempty"`
	BillingDetails model.LegacyBillingDetails `json:"billingDetails"`
}

const modelSquareCatalogKeyPrefix = "m_"

func modelSquareCatalogKey(modelName string) string {
	return modelSquareCatalogKeyPrefix + base64.RawURLEncoding.EncodeToString([]byte(modelName))
}

func decodeModelSquareCatalogKey(key string) (string, bool) {
	encoded, ok := strings.CutPrefix(key, modelSquareCatalogKeyPrefix)
	if !ok || encoded == "" {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return "", false
	}
	return string(decoded), true
}

// modelSquareChannel 是模型广场公开的单个服务渠道信息。
// 只暴露渠道 ID、费用倍率与生效能力，不暴露渠道名称和密钥。
type modelSquareChannel struct {
	ID                 int                    `json:"id"`
	MaxInputTokens     *int64                 `json:"maxInputTokens,omitempty"`
	CapabilityDeclared bool                   `json:"capabilityDeclared"`
	CostMultiplier     float64                `json:"cost_multiplier"`
	Capabilities       []string               `json:"capabilities,omitempty"`
	InputModalities    []string               `json:"input_modalities,omitempty"`
	OutputModalities   []string               `json:"output_modalities,omitempty"`
	MaxOutputTokens    *int64                 `json:"max_output_tokens,omitempty"`
	Parameters         []capability.Parameter `json:"parameters,omitempty"`
}

// modelSquareRoute 是一条启用的"模型 → 渠道"路由，携带计算展示信息所需的渠道字段。
type modelSquareRoute struct {
	Model     string
	ChannelID int
	Setting   *string
}

// modelSquarePricingFromPricing 把倍率换算成美元/百万 token 的单价：倍率 1 对应输入 2 美元，
// 与前端 calculateTokenPrice 的换算一致。
func modelSquarePricingFromPricing(pricing model.Pricing) *modelSquarePricing {
	if pricing.QuotaType == 1 {
		return &modelSquarePricing{InputPrice: pricing.ModelPrice, OutputPrice: pricing.ModelPrice}
	}
	inputPrice := pricing.ModelRatio * 2
	result := &modelSquarePricing{
		InputPrice:  inputPrice,
		OutputPrice: inputPrice * pricing.CompletionRatio,
	}
	if pricing.CacheRatio != nil {
		cacheReadPrice := inputPrice * *pricing.CacheRatio
		result.CacheReadPrice = &cacheReadPrice
	}
	return result
}

func modelSquareItemFromModel(
	meta *model.Model,
	modelName string,
	pricingByName map[string]model.Pricing,
	vendors map[int]model.Vendor,
) modelSquareItem {
	catalogSlug := modelName
	if vendor, ok := vendors[meta.VendorID]; ok && vendor.Name != "" {
		ownerSlug := strings.ToLower(strings.TrimSpace(vendor.Name))
		ownerSlug = strings.NewReplacer(" ", "-", "/", "-", "_", "-").Replace(ownerSlug)
		catalogSlug = ownerSlug + "/" + modelName
	}
	item := modelSquareItem{
		ID:                     meta.Id,
		ModelName:              modelName,
		CatalogKey:             modelSquareCatalogKey(modelName),
		CatalogSlug:            catalogSlug,
		Description:            meta.Description,
		Icon:                   meta.Icon,
		Tags:                   meta.Tags,
		VendorID:               meta.VendorID,
		ContextLength:          meta.ContextLength,
		ParameterCount:         meta.ParameterCount,
		ReleaseDate:            meta.ReleaseDate,
		EnableGroup:            []string{},
		SupportedEndpointTypes: []string{},
	}
	if vendor, ok := vendors[meta.VendorID]; ok {
		item.VendorName = vendor.Name
		item.VendorIcon = vendor.Icon
		item.VendorDescription = vendor.Description
	}
	spec := capability.MustParse(meta.Capabilities)
	item.MaxInputTokens = spec.Context.MaxInputTokens
	_, item.CapabilityDeclared = model.ResolveModelCapability(modelName)
	summary := capability.Summarize(spec)
	item.Capabilities = summary.Capabilities
	item.InputModalities = summary.InputModalities
	item.OutputModalities = summary.OutputModalities
	item.MaxOutputTokens = summary.MaxOutputTokens
	item.Parameters = capability.Parameters(spec)

	if pricing, ok := pricingByName[modelName]; ok {
		item.QuotaType = pricing.QuotaType
		item.ModelRatio = pricing.ModelRatio
		item.ModelPrice = pricing.ModelPrice
		item.CompletionRatio = pricing.CompletionRatio
		item.CacheRatio = pricing.CacheRatio
		item.CreateCacheRatio = pricing.CreateCacheRatio
		item.EnableGroup = append([]string{}, pricing.EnableGroup...)
		item.SupportedEndpointTypes = make([]string, 0, len(pricing.SupportedEndpointTypes))
		for _, endpoint := range pricing.SupportedEndpointTypes {
			item.SupportedEndpointTypes = append(item.SupportedEndpointTypes, string(endpoint))
		}
		item.BillingMode = pricing.BillingMode
		item.BillingExpr = pricing.BillingExpr
		item.Pricing = modelSquarePricingFromPricing(pricing)
	}
	return item
}

// loadModelSquareRoutes 返回所有启用的"模型 → 渠道"路由。
func loadModelSquareRoutes() ([]modelSquareRoute, error) {
	var routes []modelSquareRoute
	err := model.DB.Table("abilities").
		Select("abilities.model as model, channels.id as channel_id, channels.setting as setting").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where("abilities.enabled = ? AND channels.status = ?", true, common.ChannelStatusEnabled).
		Distinct().
		Order("abilities.model, channels.id").
		Scan(&routes).Error
	return routes, err
}

// squareChannelsByModel 把路由行整理成公开的渠道展示信息。
func squareChannelsByModel(routes []modelSquareRoute, names map[string]struct{}) map[string][]modelSquareChannel {
	byModel := make(map[string][]modelSquareChannel)
	for _, route := range routes {
		if _, wanted := names[route.Model]; !wanted {
			continue
		}
		setting := ""
		if route.Setting != nil {
			setting = *route.Setting
		}
		channel := modelSquareChannel{
			ID:             route.ChannelID,
			CostMultiplier: model.ChannelCostMultiplier(setting, route.Model),
		}
		if channel.CostMultiplier <= 0 {
			channel.CostMultiplier = 1
		}
		if spec, ok := model.EffectiveChannelCapability(route.ChannelID, route.Model); ok {
			channel.MaxInputTokens = spec.Context.MaxInputTokens
			channel.CapabilityDeclared = true
			summary := capability.Summarize(spec)
			channel.Capabilities = summary.Capabilities
			channel.InputModalities = summary.InputModalities
			channel.OutputModalities = summary.OutputModalities
			channel.MaxOutputTokens = summary.MaxOutputTokens
			channel.Parameters = capability.Parameters(spec)
		}
		byModel[route.Model] = append(byModel[route.Model], channel)
	}
	return byModel
}

func buildModelSquareItems(
	resolved map[string]*model.Model,
	names []string,
	routes []modelSquareRoute,
) ([]modelSquareItem, error) {
	pricingByName := make(map[string]model.Pricing)
	for _, pricing := range model.GetPricing() {
		pricingByName[pricing.ModelName] = pricing
	}
	var vendorRows []model.Vendor
	if err := model.DB.Find(&vendorRows).Error; err != nil {
		return nil, err
	}
	vendors := make(map[int]model.Vendor, len(vendorRows))
	for _, vendor := range vendorRows {
		vendors[vendor.Id] = vendor
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	channelsByName := squareChannelsByModel(routes, wanted)
	items := make([]modelSquareItem, 0, len(names))
	for _, name := range names {
		meta := resolved[name]
		if meta == nil {
			continue
		}
		item := modelSquareItemFromModel(meta, name, pricingByName, vendors)
		item.Channels = channelsByName[name]
		items = append(items, item)
	}
	return items, nil
}

// getModelSquareItems 从启用路由出发构建模型广场列表。
// 只要模型能被任意声明行（精确/前缀/后缀/包含）命中、有价格、有启用渠道，
// 就出现在广场里；元数据取命中行的描述、厂商与能力声明。
func getModelSquareItems(offset, limit int) ([]modelSquareItem, int64, error) {
	pricingByName := make(map[string]model.Pricing)
	for _, pricing := range model.GetPricing() {
		pricingByName[pricing.ModelName] = pricing
	}
	routes, err := loadModelSquareRoutes()
	if err != nil {
		return nil, 0, err
	}
	names := make([]string, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if _, hasPricing := pricingByName[route.Model]; !hasPricing {
			continue
		}
		if _, exists := seen[route.Model]; exists {
			continue
		}
		seen[route.Model] = struct{}{}
		names = append(names, route.Model)
	}
	if len(names) == 0 {
		return []modelSquareItem{}, 0, nil
	}
	resolved, err := model.ResolveModelMetadataByName(names)
	if err != nil {
		return nil, 0, err
	}
	visible := make([]string, 0, len(names))
	for _, name := range names {
		if meta := resolved[name]; meta != nil && meta.Status == 1 {
			visible = append(visible, name)
		}
	}
	sort.Strings(visible)
	total := len(visible)
	if offset > 0 {
		if offset >= total {
			return []modelSquareItem{}, int64(total), nil
		}
		visible = visible[offset:]
	}
	if limit > 0 && limit < len(visible) {
		visible = visible[:limit]
	}
	items, err := buildModelSquareItems(resolved, visible, routes)
	return items, int64(total), err
}

// getModelSquareItemByCatalogKey 按广场路由键取详情。
// 数值键保持旧链接兼容（元数据行 ID，仅精确声明）；非数值键是具体模型名。
func getModelSquareItemByCatalogKey(key string) (*modelSquareItem, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("model not found")
	}
	routes, err := loadModelSquareRoutes()
	if err != nil {
		return nil, err
	}
	var meta *model.Model
	var modelName string
	if id, parseErr := strconv.Atoi(key); parseErr == nil {
		// 旧链接使用元数据行 ID；只有精确行能用 ID 定位。
		var exact model.Model
		if err := model.DB.Where("id = ? AND status = ? AND name_rule = ?", id, 1, model.NameRuleExact).
			First(&exact).Error; err != nil {
			return nil, errors.New("model not found")
		}
		meta = &exact
		modelName = exact.ModelName
	} else {
		decoded, ok := decodeModelSquareCatalogKey(key)
		if !ok {
			return nil, errors.New("model not found")
		}
		resolved, resolveErr := model.ResolveModelMetadataByName([]string{decoded})
		if resolveErr != nil {
			return nil, resolveErr
		}
		meta = resolved[decoded]
		if meta == nil || meta.Status != 1 {
			return nil, errors.New("model not found")
		}
		modelName = decoded
	}
	hasRoute := slices.ContainsFunc(routes, func(route modelSquareRoute) bool {
		return route.Model == modelName
	})
	if !hasRoute {
		return nil, errors.New("model not found")
	}
	items, err := buildModelSquareItems(map[string]*model.Model{modelName: meta}, []string{modelName}, routes)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("model not found")
	}
	items[0].EndpointMap = getModelSquareEndpointMap(items[0].SupportedEndpointTypes)
	snapshot, err := model.GetModelPricingSnapshot([]string{modelName})
	if err != nil {
		return nil, err
	}
	if len(snapshot.Entries) == 1 {
		entry := snapshot.Entries[0]
		values := make(model.PricingValues)
		for _, key := range []string{"ModelPrice", "ModelRatio", "CompletionRatio", "CacheRatio", "CreateCacheRatio", "ImageRatio", "AudioRatio", "AudioCompletionRatio", "billing_setting.billing_mode", "billing_setting.billing_expr"} {
			if value, ok := entry.Effective[key]; ok {
				values[key] = value
			}
		}
		items[0].BasePricing = &modelSquareBasePricing{
			SourceModel:    modelName,
			Effective:      values,
			CacheWriteMode: entry.CacheWriteMode,
			BillingDetails: entry.BillingDetails,
		}
	}
	return &items[0], nil
}

func getModelSquareEndpointMap(endpointTypes []string) map[string]common.EndpointInfo {
	if len(endpointTypes) == 0 {
		return nil
	}
	configured := model.GetSupportedEndpointMap()
	result := make(map[string]common.EndpointInfo, len(endpointTypes))
	for _, endpointType := range endpointTypes {
		if info, ok := configured[endpointType]; ok {
			result[endpointType] = info
			continue
		}
		if info, ok := common.GetDefaultEndpointInfo(constant.EndpointType(endpointType)); ok {
			result[endpointType] = info
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func parseModelSquarePagination(c *gin.Context) (int, int, error) {
	offset := 0
	limit := 24
	if rawOffset := c.Query("offset"); rawOffset != "" {
		parsed, err := strconv.Atoi(rawOffset)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
		offset = parsed
	}
	if rawLimit := c.Query("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		if parsed > 100 {
			parsed = 100
		}
		limit = parsed
	}
	return offset, limit, nil
}

func GetModelSquare(c *gin.Context) {
	offset, limit, err := parseModelSquarePagination(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items, total, err := getModelSquareItems(offset, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"items":  items,
		"total":  total,
		"offset": offset,
		"limit":  limit,
	}})
}

func GetModelSquareDetail(c *gin.Context) {
	item, err := getModelSquareItemByCatalogKey(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "model not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}
