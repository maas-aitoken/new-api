package model

import (
	"context"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"

	"gorm.io/gorm"
)

func applyExplicitLogTextFilter(tx *gorm.DB, column string, value string) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if strings.Contains(value, "%") {
		condition, pattern, err := buildLogLikeCondition(column, value)
		if err != nil {
			return nil, err
		}
		return tx.Where(condition, pattern), nil
	}
	return tx.Where(column+" = ?", value), nil
}

func buildLogLikeCondition(column string, value string) (string, string, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		pattern, err := sanitizeClickHouseLikePattern(value)
		if err != nil {
			return "", "", err
		}
		return column + " LIKE ?", pattern, nil
	}

	pattern, err := sanitizeLikePattern(value)
	if err != nil {
		return "", "", err
	}
	return column + " LIKE ? ESCAPE '!'", pattern, nil
}

func sanitizeClickHouseLikePattern(input string) (string, error) {
	input = strings.ReplaceAll(input, `\`, `\\`)
	input = strings.ReplaceAll(input, `_`, `\_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}
	return input, nil
}

type Log struct {
	Id                int    `json:"id" gorm:"index:idx_created_at_id,priority:2;index:idx_user_id_id,priority:2"`
	UserId            int    `json:"user_id" gorm:"index;index:idx_user_id_id,priority:1"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,priority:1;index:idx_created_at_type"`
	Type              int    `json:"type" gorm:"index:idx_created_at_type"`
	Content           string `json:"content"`
	Username          string `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName         string `json:"token_name" gorm:"index;default:''"`
	ModelName         string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota             int    `json:"quota" gorm:"default:0"`
	PromptTokens      int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens  int    `json:"completion_tokens" gorm:"default:0"`
	UseTime           int    `json:"use_time" gorm:"default:0"`
	IsStream          bool   `json:"is_stream"`
	FastMode          bool   `json:"fast_mode" gorm:"index"`
	AutoRouted        bool   `json:"auto_routed"` // asked for "auto"; ModelName is the model it resolved to. Rows from before the column hold NULL, read as false.
	ChannelId         int    `json:"channel" gorm:"index"`
	ChannelName       string `json:"channel_name" gorm:"->"`
	TokenId           int    `json:"token_id" gorm:"default:0;index"`
	Group             string `json:"group" gorm:"index"`
	Ip                string `json:"ip" gorm:"index;default:''"`
	RequestId         string `json:"request_id,omitempty" gorm:"type:varchar(64);index:idx_logs_request_id;default:''"`
	UpstreamRequestId string `json:"upstream_request_id,omitempty" gorm:"type:varchar(128);index:idx_logs_upstream_request_id;default:''"`
	Other             string `json:"other"`
	IselfEmail        string `json:"iself_email" gorm:"index;default:''"`
	VendorName        string `json:"vendor_name,omitempty" gorm:"-"`
	VendorIcon        string `json:"vendor_icon,omitempty" gorm:"-"`
	UserAvatar        string `json:"user_avatar,omitempty" gorm:"-"`
	UserDisplayName   string `json:"user_display_name,omitempty" gorm:"-"`
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown = 0
	LogTypeTopup   = 1
	LogTypeConsume = 2
	LogTypeManage  = 3
	LogTypeSystem  = 4
	LogTypeError   = 5
	LogTypeRefund  = 6
	LogTypeLogin   = 7
)

func ensureLogRequestId(log *Log) {
	if log != nil && log.RequestId == "" {
		log.RequestId = common.NewRequestId()
	}
}

func createLog(log *Log) error {
	ensureLogRequestId(log)
	return LOG_DB.Create(log).Error
}

func clickHouseLogOrder(prefix string) string {
	return prefix + "created_at desc, " + prefix + "request_id desc"
}

func assignDisplayLogIds(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].Id = startIdx + i + 1
	}
}

func formatUserLogs(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].ChannelName = ""
		logs[i].Other = formatLogOtherJSON(logs[i].Other, logOtherVisibilityUser)
	}
	assignDisplayLogIds(logs, startIdx)
}

// FormatAdminLogs removes root-only diagnostics while retaining operational
// admin_info. Root callers must not pass their results through this formatter.
func FormatAdminLogs(logs []*Log) {
	for i := range logs {
		logs[i].Other = formatLogOtherJSON(logs[i].Other, logOtherVisibilityAdmin)
	}
}

// FillLogVendors derives vendor info from the current model pricing cache, so
// historical logs reflect the model's present vendor rather than a stored value.
func FillLogVendors(logs []*Log) {
	if len(logs) == 0 {
		return
	}
	vendorByID := make(map[int]PricingVendor)
	for _, vendor := range GetVendors() {
		vendorByID[vendor.ID] = vendor
	}
	type logVendor struct{ name, icon string }
	vendorByModel := make(map[string]logVendor)
	for _, pricing := range GetPricing() {
		if vendor, ok := vendorByID[pricing.VendorID]; ok {
			vendorByModel[pricing.ModelName] = logVendor{name: vendor.Name, icon: vendor.Icon}
		} else if pricing.OwnerBy != "" {
			vendorByModel[pricing.ModelName] = logVendor{name: pricing.OwnerBy}
		}
	}
	for i := range logs {
		if vendor, ok := vendorByModel[logs[i].ModelName]; ok {
			logs[i].VendorName = vendor.name
			logs[i].VendorIcon = vendor.icon
		}
	}
}

// FillLogUsers loads avatar and display name for the users on one page of logs.
// Users live in the main DB, which may differ from LOG_DB, so this is a separate
// lookup. Soft-deleted users are included so historical rows keep their identity.
func FillLogUsers(logs []*Log) {
	if len(logs) == 0 {
		return
	}
	idSet := make(map[int]struct{}, len(logs))
	for _, log := range logs {
		if log.UserId > 0 {
			idSet[log.UserId] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	var users []User
	if err := DB.Unscoped().Select("id", "avatar", "display_name").Where("id IN ?", ids).Find(&users).Error; err != nil {
		common.SysError("failed to load log users: " + err.Error())
		return
	}
	userByID := make(map[int]*User, len(users))
	for i := range users {
		userByID[users[i].Id] = &users[i]
	}
	for _, log := range logs {
		if user, ok := userByID[log.UserId]; ok {
			log.UserAvatar = user.Avatar
			log.UserDisplayName = user.DisplayName
		}
	}
}

// FormatRootLogs normalizes legacy metadata into the current scoped shape
// without removing root-only diagnostics.
func FormatRootLogs(logs []*Log) {
	for i := range logs {
		logs[i].Other = formatLogOtherJSON(logs[i].Other, logOtherVisibilityRoot)
	}
}

func GetLogByTokenId(tokenId int) (logs []*Log, err error) {
	order := "id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("")
	}
	err = LOG_DB.Model(&Log{}).Where("token_id = ?", tokenId).Order(order).Limit(common.MaxRecentItems).Find(&logs).Error
	formatUserLogs(logs, 0)
	return logs, err
}

func RecordLog(userId int, logType int, content *common.Message) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	other := NewLogOther()
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   other.setContent([]*common.Message{content}),
		Other:     other.JSONString(),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog(common.LogText("failed to record log: %s", err.Error()))
	}
}

// RecordLogWithAdminInfo stores operator metadata under other.admin_info and
// an optional, user-visible operation descriptor under other.op for localization.
func RecordLogWithAdminInfo(userId int, logType int, content string, adminInfo *AuditAdminInfo, operation *AuditOperation, request ...*gin.Context) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	if logType == LogTypeManage {
		var c *gin.Context
		if len(request) > 0 {
			c = request[0]
		}
		actorRole := 0
		if c != nil {
			actorRole = c.GetInt("role")
		}
		RecordAuditLog(c, AuditLog{UserId: userId, Username: username, ActorRole: actorRole, Category: AuditCategoryOperation, Content: content, Other: AuditOther{AdminInfo: adminInfo, Op: operation}, Success: true})
		return
	}
	if len(request) > 0 && request[0] != nil {
		log.RequestId = request[0].GetString(common.RequestIdKey)
	}
	if adminInfo != nil || operation != nil {
		data, err := common.Marshal(AuditOther{AdminInfo: adminInfo, Op: operation})
		if err != nil {
			common.SysError(common.LogText("failed to encode log admin info: %s", err.Error()))
			return
		}
		log.Other = string(data)
	}
	if err := createLog(log); err != nil {
		common.SysLog(common.LogText("failed to record log: %s", err.Error()))
	}
}

// RecordLoginLog writes new login events to the independent audit table.
// username 由调用方传入（登录流程已持有用户对象），避免额外的数据库查询。
// content 为英文兜底文本（用于导出）；action+params 供前端本地化渲染。
// other 包含 login_method、user_agent 等结构化信息。
func RecordLoginLog(userId, actorRole int, username string, content string, ip string, action string, params map[string]any, other AuditOther, request ...*gin.Context) {
	other.Op = &AuditOperation{Action: action, Params: params}
	var c *gin.Context
	if len(request) > 0 {
		c = request[0]
	}
	RecordAuditLog(c, AuditLog{UserId: userId, Username: username, ActorRole: actorRole, Category: AuditCategoryLogin, Action: action, Content: content, Ip: ip, Other: other, Success: true})
}

// RecordOperationAuditLog writes new operation/security events to the audit table.
// logUserId 为日志归属者，管理审计日志应归属实际操作者；目标资源/用户放入
// action params。username 内部按 logUserId 查询。content 为英文兜底文本（供导出使用）。
// action+params 写入 Other.op，供前端本地化渲染（普通用户可见，不含敏感信息）。
// adminInfo 存放操作者身份（写入 Other.admin_info，普通用户查询时剥离）；
// auditInfo 存放路由/方法/结果等中间件兜底信息（写入 Other.audit_info，普通用户查询时剥离）。
func RecordOperationAuditLog(logUserId, actorRole int, content string, ip string, action string, params map[string]any, adminInfo *AuditAdminInfo, auditInfo *AuditRequestInfo, request ...*gin.Context) {
	username, _ := GetUsernameById(logUserId, false)
	other := AuditOther{
		Op:        &AuditOperation{Action: action, Params: params},
		AdminInfo: adminInfo,
		AuditInfo: auditInfo,
	}
	var c *gin.Context
	if len(request) > 0 {
		c = request[0]
	}
	category := AuditCategoryOperation
	if adminInfo == nil {
		category = AuditCategorySecurity
	}
	status, success := 200, true
	if auditInfo != nil {
		status = auditInfo.Status
		success = auditInfo.Success
	}
	RecordAuditLog(c, AuditLog{UserId: logUserId, Username: username, ActorRole: actorRole, Category: category, Action: action, Content: content, Ip: ip, Status: status, Success: success, Other: other})
}

func RecordTopupLog(userId int, content *common.Message, callerIp string, paymentMethod string, callbackPaymentMethod string) {
	username, _ := GetUsernameById(userId, false)
	other := NewLogOther()
	other.MergeAdmin(map[string]any{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"caller_ip":               callerIp,
		"payment_method":          paymentMethod,
		"callback_payment_method": callbackPaymentMethod,
		"version":                 common.Version,
	})
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   other.setContent([]*common.Message{content}),
		Ip:        callerIp,
		Other:     other.JSONString(),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog(common.LogText("failed to record topup log: %s", err.Error()))
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other *LogOther) {
	logger.LogInfo(c, common.LogText("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, common.LocalLogPreview(content)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	otherStr := other.JSONString()
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            0,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		AutoRouted:       common.GetContextKeyBool(c, constant.ContextKeyAutoRouted),
		Group:            group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
		IselfEmail:        common.GetContextKeyString(c, constant.ContextKeyIselfEmail),
	}
	err := createLog(log)
	if err != nil {
		logger.LogError(c, common.LogText("failed to record log: %s", err.Error()))
	}
}

type RecordConsumeLogParams struct {
	DashboardTokenUsed *int              `json:"-"`
	FastMode           bool              `json:"fast_mode"`
	ChannelId          int               `json:"channel_id"`
	PromptTokens       int               `json:"prompt_tokens"`
	CompletionTokens   int               `json:"completion_tokens"`
	ModelName          string            `json:"model_name"`
	TokenName          string            `json:"token_name"`
	Quota              int               `json:"quota"`
	Content            []*common.Message `json:"content"`
	TokenId            int               `json:"token_id"`
	UseTimeSeconds     int               `json:"use_time_seconds"`
	IsStream           bool              `json:"is_stream"`
	Group              string            `json:"group"`
	Other              *LogOther         `json:"other"`
	OutcomeTracked     bool              `json:"-"`
}

func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	if !common.LogConsumeEnabled {
		return
	}
	logger.LogInfo(c, common.LogText("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	createdAt := common.GetTimestamp()
	other := params.Other
	content := other.setContent(params.Content)
	otherStr := other.JSONString()
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        createdAt,
		Type:             LogTypeConsume,
		Content:          content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		FastMode:         params.FastMode,
		AutoRouted:       common.GetContextKeyBool(c, constant.ContextKeyAutoRouted),
		Group:            params.Group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
		IselfEmail:        common.GetContextKeyString(c, constant.ContextKeyIselfEmail),
	}
	err := createLog(log)
	if err != nil {
		logger.LogError(c, common.LogText("failed to record log: %s", err.Error()))
	}
	if common.DataExportEnabled {
		tokenUsed := params.PromptTokens + params.CompletionTokens
		if params.DashboardTokenUsed != nil {
			tokenUsed = *params.DashboardTokenUsed
		}
		inputTokens := params.PromptTokens
		if params.DashboardTokenUsed != nil {
			inputTokens = *params.DashboardTokenUsed - params.CompletionTokens
		}
		cacheRead, cacheWrite := extractCacheTokens(params.Other)
		isStreamError := isOtherStreamError(params.Other)
		LogQuotaData(QuotaDataLogParams{
			UserID:              userId,
			Username:            username,
			ModelName:           params.ModelName,
			Quota:               params.Quota,
			CreatedAt:           createdAt,
			TokenUsed:           tokenUsed,
			UseGroup:            params.Group,
			TokenID:             params.TokenId,
			ChannelID:           params.ChannelId,
			NodeName:            common.NodeName,
			InputTokens:         inputTokens,
			CompletionTokens:    params.CompletionTokens,
			CacheReadTokens:     int64(cacheRead),
			CacheCreationTokens: int64(cacheWrite),
			CacheWriteTokens:    cacheWrite,
			UseTimeSeconds:      params.UseTimeSeconds,
			IsStreamError:       isStreamError,
			OutcomeTracked:      params.OutcomeTracked,
		})
	}
}

func otherIntValue(other map[string]interface{}, key string) int {
	switch v := other[key].(type) {
	case int:
		return max(v, 0)
	case int64:
		return max(int(v), 0)
	case float64:
		return max(int(v), 0)
	default:
		return 0
	}
}

// Prefer normalized cache writes, falling back to legacy cache_creation_tokens.
func extractCacheTokens(other *LogOther) (cacheRead, cacheWrite int) {
	if other == nil {
		return 0, 0
	}
	cacheRead = otherIntValue(other.public, "cache_tokens")
	cacheWrite = otherIntValue(other.public, "cache_write_tokens")
	if cacheWrite == 0 {
		cacheWrite = otherIntValue(other.public, "cache_creation_tokens")
	}
	return cacheRead, cacheWrite
}

func isOtherStreamError(other *LogOther) bool {
	if other == nil {
		return false
	}
	switch status := other.public["stream_status"].(type) {
	case map[string]interface{}:
		value, _ := status["status"].(string)
		return value == "error"
	case map[string]string:
		return status["status"] == "error"
	default:
		return false
	}
}

type RecordTaskBillingLogParams struct {
	UserId    int
	LogType   int
	Content   []*common.Message
	ChannelId int
	ModelName string
	Quota     int
	TokenId   int
	Group     string
	Other     *LogOther
	NodeName  string // 任务发起节点；为空时回退当前节点
}

func RecordTaskBillingLog(params RecordTaskBillingLogParams) {
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(params.UserId, false)
	tokenName := ""
	if params.TokenId > 0 {
		if token, err := GetTokenById(params.TokenId); err == nil {
			tokenName = token.Name
		}
	}
	createdAt := common.GetTimestamp()
	other := params.Other
	log := &Log{
		UserId:    params.UserId,
		Username:  username,
		CreatedAt: createdAt,
		Type:      params.LogType,
		Content:   other.setContent(params.Content),
		TokenName: tokenName,
		ModelName: params.ModelName,
		Quota:     params.Quota,
		ChannelId: params.ChannelId,
		TokenId:   params.TokenId,
		Group:     params.Group,
		Other:     other.JSONString(),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog(common.LogText("failed to record task billing log: %s", err.Error()))
	}
	if params.LogType == LogTypeConsume && common.DataExportEnabled {
		nodeName := params.NodeName
		if nodeName == "" {
			nodeName = common.NodeName
		}
		LogQuotaData(QuotaDataLogParams{
			UserID:    params.UserId,
			Username:  username,
			ModelName: params.ModelName,
			Quota:     params.Quota,
			CreatedAt: createdAt,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  nodeName,
		})
	}
}

// LogFilter is the usage-log search shared by the log list and the log export.
// Username and Channel are administrator filters.
type LogFilter struct {
	Type              int
	StartTimestamp    int64
	EndTimestamp      int64
	ModelName         string
	Username          string
	TokenName         string
	Channel           int
	Group             string
	RequestId         string
	UpstreamRequestId string
	StreamError       bool
	Retry             bool
	ISelfEmail        string
	// AutoRouted keeps only requests that asked for the "auto" model (true) or
	// only those that did not (false); nil keeps both.
	AutoRouted *bool
}

func (f LogFilter) apply(tx *gorm.DB) (*gorm.DB, error) {
	var err error
	if f.Type != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", f.Type)
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", f.ModelName); err != nil {
		return nil, err
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.username", f.Username); err != nil {
		return nil, err
	}
	if f.TokenName != "" {
		tx = tx.Where("logs.token_name = ?", f.TokenName)
	}
	if f.RequestId != "" {
		tx = tx.Where("logs.request_id = ?", f.RequestId)
	}
	if f.UpstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", f.UpstreamRequestId)
	}
	if f.StartTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", f.StartTimestamp)
	}
	if f.EndTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", f.EndTimestamp)
	}
	if f.Channel != 0 {
		tx = tx.Where("logs.channel_id = ?", f.Channel)
	}
	if f.Group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", f.Group)
	}
	if f.ISelfEmail != "" {
		tx = tx.Where("logs.iself_email = ?", f.ISelfEmail)
	}
	tx = applyStreamErrorFilter(tx, "logs.other", f.StreamError)
	tx = applyRetryFilter(tx, "logs.other", f.Retry)
	tx = applyAutoRoutedFilter(tx, "logs.auto_routed", f.AutoRouted)
	return tx, nil
}

// applyAutoRoutedFilter keeps the logs that did (true) or did not (false) ask for
// the "auto" model. Rows written before the column existed hold NULL, so "did not"
// has to include them; ClickHouse never holds NULL there and the extra test is harmless.
func applyAutoRoutedFilter(tx *gorm.DB, column string, autoRouted *bool) *gorm.DB {
	if autoRouted == nil {
		return tx
	}
	if *autoRouted {
		return tx.Where(column+" = ?", true)
	}
	return tx.Where("("+column+" IS NULL OR "+column+" = ?)", false)
}

func GetAllLogs(filter LogFilter, startIdx int, num int) (logs []*Log, total int64, err error) {
	tx, err := filter.apply(LOG_DB)
	if err != nil {
		return nil, 0, err
	}
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	order := "logs.created_at desc, logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		assignDisplayLogIds(logs, startIdx)
	}

	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if common.MemoryCacheEnabled {
			// Cache get channel
			for _, channelId := range channelIds.Items() {
				if cacheChannel, err := CacheGetChannel(channelId); err == nil {
					channels = append(channels, struct {
						Id   int    `gorm:"column:id"`
						Name string `gorm:"column:name"`
					}{
						Id:   channelId,
						Name: cacheChannel.Name,
					})
				}
			}
		} else {
			// Bulk query channels from DB
			if err = DB.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
				return logs, total, err
			}
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	return logs, total, err
}

const logSearchCountLimit = 10000

// logStreamStatusExpr extracts Other.stream_status.status for the log dialect.
func logStreamStatusExpr(column string) (string, bool) {
	switch common.LogDatabaseType() {
	case common.DatabaseTypeSQLite:
		return "json_extract(CASE WHEN json_valid(" + column + ") THEN " + column + " ELSE '{}' END, '$.stream_status.status')", true
	case common.DatabaseTypeMySQL:
		return "JSON_UNQUOTE(JSON_EXTRACT(CASE WHEN JSON_VALID(" + column + ") THEN " + column + " ELSE '{}' END, '$.stream_status.status'))", true
	case common.DatabaseTypePostgreSQL:
		return "CASE WHEN " + column + " ~ '^\\s*\\{.*\\}\\s*$' THEN (" + column + "::jsonb -> 'stream_status' ->> 'status') ELSE NULL END", true
	case common.DatabaseTypeClickHouse:
		return "JSONExtractString(" + column + ", 'stream_status', 'status')", true
	default:
		return "", false
	}
}

// applyStreamErrorFilter restricts logs to records whose generated stream
func applyStreamErrorFilter(tx *gorm.DB, column string, enabled bool) *gorm.DB {
	if !enabled {
		return tx
	}
	statusExpr, ok := logStreamStatusExpr(column)
	if !ok {
		common.SysError("stream error filter unsupported on log database type: " + string(common.LogDatabaseType()))
		return tx.Where("1 = 0")
	}
	return tx.Where(statusExpr+" = ?", "error")
}

// applyRetryFilter restricts logs to requests that attempted more than one
// channel. Relay logs persist the attempted channel IDs in
// Other.admin_info.use_channel.
func applyRetryFilter(tx *gorm.DB, column string, enabled bool) *gorm.DB {
	if !enabled {
		return tx
	}

	switch common.LogDatabaseType() {
	case common.DatabaseTypeSQLite:
		return tx.Where(
			"json_array_length(json_extract(CASE WHEN json_valid(" + column + ") THEN " + column + " ELSE '{}' END, '$.admin_info.use_channel')) > 1",
		)
	case common.DatabaseTypeMySQL:
		return tx.Where(
			"JSON_LENGTH(JSON_EXTRACT(CASE WHEN JSON_VALID(" + column + ") THEN " + column + " ELSE '{}' END, '$.admin_info.use_channel')) > 1",
		)
	case common.DatabaseTypePostgreSQL:
		return tx.Where(
			"CASE WHEN " + column + " ~ '^\\s*\\{.*\\}\\s*$' AND jsonb_typeof(" + column + "::jsonb -> 'admin_info' -> 'use_channel') = 'array' THEN jsonb_array_length(" + column + "::jsonb -> 'admin_info' -> 'use_channel') ELSE 0 END > 1",
		)
	case common.DatabaseTypeClickHouse:
		return tx.Where("length(JSONExtractArrayRaw(" + column + ", 'admin_info', 'use_channel')) > 1")
	default:
		common.SysError("retry filter unsupported on log database type: " + string(common.LogDatabaseType()))
		return tx.Where("1 = 0")
	}
}

func GetUserLogs(userId int, filter LogFilter, startIdx int, num int) (logs []*Log, total int64, err error) {
	tx, err := filter.apply(LOG_DB.Where("logs.user_id = ?", userId))
	if err != nil {
		return nil, 0, err
	}
	err = tx.Model(&Log{}).Limit(logSearchCountLimit).Count(&total).Error
	if err != nil {
		common.SysError(common.LogText("failed to count user logs: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to query logs")
	}
	order := "logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		common.SysError(common.LogText("failed to search user logs: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to query logs")
	}

	formatUserLogs(logs, startIdx)
	return logs, total, err
}

// StreamAllLogs calls fn with each log matching filter, newest first, as an
// administrator with role sees it. Rows are read one at a time, so an export
// never holds the whole result in memory.
func StreamAllLogs(ctx context.Context, filter LogFilter, role int, fn func(*Log) error) error {
	tx, err := filter.apply(LOG_DB.WithContext(ctx))
	if err != nil {
		return err
	}
	visibility := logOtherVisibilityAdmin
	if role >= common.RoleRootUser {
		visibility = logOtherVisibilityRoot
	}
	channelNames := make(map[int]string)
	return streamLogs(tx, "logs.created_at desc, logs.id desc", func(log *Log) error {
		if log.ChannelId != 0 {
			name, ok := channelNames[log.ChannelId]
			if !ok {
				if channel, err := CacheGetChannel(log.ChannelId); err == nil {
					name = channel.Name
				}
				channelNames[log.ChannelId] = name
			}
			log.ChannelName = name
		}
		log.Other = formatLogOtherJSON(log.Other, visibility)
		return fn(log)
	})
}

// StreamUserLogs is StreamAllLogs for one user's own logs, shaped like the
// user log list.
func StreamUserLogs(ctx context.Context, userId int, filter LogFilter, fn func(*Log) error) error {
	tx, err := filter.apply(LOG_DB.WithContext(ctx).Where("logs.user_id = ?", userId))
	if err != nil {
		return err
	}
	count := 0
	return streamLogs(tx, "logs.id desc", func(log *Log) error {
		count++
		log.Id = count
		log.ChannelName = ""
		log.Other = formatLogOtherJSON(log.Other, logOtherVisibilityUser)
		return fn(log)
	})
}

func streamLogs(tx *gorm.DB, order string, fn func(*Log) error) error {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	rows, err := tx.Model(&Log{}).Order(order).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var log Log
		if err := LOG_DB.ScanRows(rows, &log); err != nil {
			return err
		}
		if err := fn(&log); err != nil {
			return err
		}
	}
	return rows.Err()
}

type Stat struct {
	Quota     int     `json:"quota"`
	Rpm       int     `json:"rpm"`
	Tpm       int     `json:"tpm"`
	Total     int64   `json:"total_count"`
	Fast      int64   `json:"fast_count"`
	FastRatio float64 `json:"fast_ratio"`
}

func buildConsumeLogStatQuery(startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string, streamError bool, retry bool, autoRouted *bool) (*gorm.DB, error) {
	tx := LOG_DB.Table("logs").Where("type = ?", LogTypeConsume)
	var err error
	if tx, err = applyExplicitLogTextFilter(tx, "username", username); err != nil {
		return nil, err
	}
	if tx, err = applyExplicitLogTextFilter(tx, "model_name", modelName); err != nil {
		return nil, err
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where(logGroupCol+" = ?", group)
	}
	tx = applyStreamErrorFilter(tx, "logs.other", streamError)
	tx = applyRetryFilter(tx, "logs.other", retry)
	tx = applyAutoRoutedFilter(tx, "logs.auto_routed", autoRouted)
	return tx, nil
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string, streamError bool, retry bool, autoRouted *bool) (stat Stat, err error) {
	newStatQuery := func() (*gorm.DB, error) {
		// Build a fresh statement for every aggregate. GORM sessions can still
		// share statement clauses after chained Select/Where calls, which causes
		// one aggregate query to overwrite another.
		return buildConsumeLogStatQuery(startTimestamp, endTimestamp, modelName, username, tokenName, channel, group, streamError, retry, autoRouted)
	}
	tx, err := newStatQuery()
	if err != nil {
		return stat, err
	}
	tx = tx.Select("COALESCE(sum(quota), 0) quota")

	// 为rpm和tpm创建单独的查询
	rpmTpmQuery, err := newStatQuery()
	if err != nil {
		return stat, err
	}
	rpmTpmQuery = rpmTpmQuery.Select("count(*) rpm, COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0) tpm")
	countQuery, err := newStatQuery()
	if err != nil {
		return stat, err
	}
	fastQuery, err := newStatQuery()
	if err != nil {
		return stat, err
	}
	fastQuery = fastQuery.Where("fast_mode = ?", true)

	// 只统计最近60秒的rpm和tpm
	rpmTpmQuery = rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	// 执行查询
	if err := tx.Scan(&stat).Error; err != nil {
		common.SysError(common.LogText("failed to query log stat: %s", err.Error()))
		return stat, common.NewMessage("Failed to query statistics")
	}
	var rpmTpmStat struct {
		Rpm int `gorm:"column:rpm"`
		Tpm int `gorm:"column:tpm"`
	}
	if err := rpmTpmQuery.Scan(&rpmTpmStat).Error; err != nil {
		common.SysError("failed to query rpm/tpm stat: " + err.Error())
		return stat, common.NewMessage("Failed to query statistics")
	}
	stat.Rpm = rpmTpmStat.Rpm
	stat.Tpm = rpmTpmStat.Tpm
	if err := countQuery.Count(&stat.Total).Error; err != nil {
		common.SysError("failed to query consume log count: " + err.Error())
		return stat, common.NewMessage("Failed to query statistics")
	}
	if err := fastQuery.Count(&stat.Fast).Error; err != nil {
		common.SysError("failed to query fast consume log count: " + err.Error())
		return stat, common.NewMessage("Failed to query statistics")
	}
	if stat.Total > 0 {
		stat.FastRatio = float64(stat.Fast) / float64(stat.Total)
	}

	return stat, nil
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := LOG_DB.Table("logs").Select("COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func CountOldLog(ctx context.Context, targetTimestamp int64) (int64, error) {
	var total int64
	if err := LOG_DB.WithContext(ctx).Model(&Log{}).Where("created_at < ?", targetTimestamp).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func DeleteOldLogBatch(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if nil != ctx.Err() {
		return 0, ctx.Err()
	}

	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// ClickHouse DELETE is a heavy mutation that rewrites data parts, so
		// per-batch mutations would be pathologically slow. Remove all matching
		// rows in a single synchronous mutation regardless of limit; the reported
		// count lets the caller's progress loop complete in one pass.
		total, err := CountOldLog(ctx, targetTimestamp)
		if err != nil {
			return 0, err
		}
		if total == 0 {
			return 0, nil
		}
		if err := LOG_DB.WithContext(ctx).Exec(
			"ALTER TABLE logs DELETE WHERE created_at < ? SETTINGS mutations_sync = 1",
			targetTimestamp,
		).Error; err != nil {
			return 0, err
		}
		return total, nil
	}

	result := LOG_DB.WithContext(ctx).Where("created_at < ?", targetTimestamp).Limit(limit).Delete(&Log{})
	if nil != result.Error {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
