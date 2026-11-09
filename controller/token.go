package controller

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

type tokenAutoGroupsInput struct {
	Set    bool
	Groups []string
}

func (input *tokenAutoGroupsInput) UnmarshalJSON(data []byte) error {
	input.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		input.Groups = nil
		return nil
	}
	return common.Unmarshal(data, &input.Groups)
}

type tokenRequest struct {
	model.Token
	AutoGroups tokenAutoGroupsInput `json:"auto_groups"`
	// 指针区分“未传”和显式 false：新建时未传即开启，更新时未传则保留原值。
	AllowUserQuotaQuery *bool `json:"allow_user_quota_query"`
	// 指针区分“未传”和显式空串（空串表示用平台默认）：更新时未传则保留原值，
	// 不认识这个字段的老客户端改令牌时不会把偏好清掉。
	AutoPreference        *string                       `json:"auto_preference"`
	RoutingPreferenceMode *common.RoutingPreferenceMode `json:"routing_preference_mode"`
	ProjectMembers        *[]int                        `json:"project_members"`
	ProjectOwnerId        *int                          `json:"project_owner_id"`
	// 这三个是 MaaS 加的字段，new-api 自带控制台和老脚本不传，用指针区分“未传”。
	// 更新时没传就保留原值，否则在旧控制台改一次，业务 key 会变成办公 key、项目成员和描述被清空。
	Scene       *string `json:"scene"`
	RoutingMode *string `json:"routing_mode"`
	Description *string `json:"description"`
}

// smartRoutingPoolUsable 判断智能路由 key 的模型池里有没有 auto 能选的模型；池子里一个都选不出时，
// 这个 key 的对话请求会全部失败，所以保存时就拒绝。不限模型或平台关了 auto 时不检查。
func smartRoutingPoolUsable(token *model.Token) bool {
	if !token.UsesSmartRouting() || !token.ModelLimitsEnabled || !autorouter_setting.GetAutoRouterSetting().Enabled {
		return true
	}
	// AutoRouterListed 在列表里已有 auto 时返回 false；auto 不是池子的成员，先去掉。
	pool := slices.DeleteFunc(token.GetModelLimits(), func(name string) bool { return name == autorouter.AutoModel })
	return service.AutoRouterListed(pool)
}

// validAutoPreference 判断请求里的 auto_preference 是否可以写入：未传或空串（平台默认）都可以。
func (r tokenRequest) validAutoPreference() bool {
	return r.AutoPreference == nil || *r.AutoPreference == "" || slices.Contains(autorouter.SelectablePreferences, *r.AutoPreference)
}

type tokenResponse struct {
	*model.Token
	AutoGroups     []string                     `json:"auto_groups"`
	PreferenceMode common.RoutingPreferenceMode `json:"preference_mode"`
	Owner          *model.UserBrief             `json:"owner,omitempty"`
	ProjectMembers []model.UserBrief            `json:"project_members,omitempty"`
	// MyRole owner / member / leader。
	MyRole       string `json:"my_role,omitempty"`
	EffectiveRPM int    `json:"effective_rpm"`
	// 本月（服务器本地时区的自然月）成功调用次数和消耗额度，只在列表接口里有；
	// 指针为 nil 表示没查到（汇总表查询失败），前端显示占位而不是 0。
	MonthlyCalls *int64 `json:"monthly_calls,omitempty"`
	MonthlyQuota *int64 `json:"monthly_quota,omitempty"`
}

func effectiveTokenRPM(rpm int) int {
	if rpm < 0 {
		return -1
	}
	if rpm > 0 {
		return rpm
	}
	if setting.TokenRequestRateLimitEnabled && setting.TokenRequestRateLimitCount > 0 {
		return setting.TokenRequestRateLimitCount
	}
	return -1
}

func maxTokenQuota() int {
	quota, err := common.WalletQuotaFromDecimalStrict(
		decimal.NewFromInt(1_000_000_000).Mul(decimal.NewFromFloat(common.QuotaPerUnit)),
	)
	if err != nil {
		return common.MaxWalletQuota
	}
	return quota
}

func buildMaskedTokenResponse(token *model.Token) *tokenResponse {
	if token == nil {
		return nil
	}
	maskedToken := *token
	maskedToken.Key = token.GetMaskedKey()
	autoGroups, err := token.GetAutoGroups()
	if err != nil {
		common.SysError(common.LogText("failed to parse auto groups for token %d: %v", token.Id, err))
		autoGroups = nil
	}
	if len(autoGroups) == 0 {
		autoGroups = nil
	}
	return &tokenResponse{
		Token:          &maskedToken,
		AutoGroups:     autoGroups,
		PreferenceMode: token.GetRoutingPreferenceMode(),
		EffectiveRPM:   effectiveTokenRPM(token.RPM),
	}
}

func buildMaskedTokenResponses(tokens []*model.Token) []*tokenResponse {
	maskedTokens := make([]*tokenResponse, 0, len(tokens))
	for _, token := range tokens {
		maskedTokens = append(maskedTokens, buildMaskedTokenResponse(token))
	}
	return maskedTokens
}

func getTokenRequestUserGroup(c *gin.Context) (string, error) {
	if userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup); userGroup != "" {
		return userGroup, nil
	}
	if userGroup := c.GetString("group"); userGroup != "" {
		return userGroup, nil
	}
	return model.GetUserGroup(c.GetInt("id"), false)
}

func applyTokenProjectMembers(c *gin.Context, token *model.Token, memberIds *[]int) bool {
	if token.Scene != common.TokenSceneBusiness {
		token.ProjectMembers = ""
		return true
	}
	if memberIds != nil {
		if err := token.SetProjectMembers(*memberIds); err != nil {
			common.ApiError(c, err)
			return false
		}
	}
	members := token.GetProjectMembers()
	if len(members) == 0 {
		return true
	}
	missing, err := model.FindMissingUserIds(members)
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	if len(missing) > 0 {
		common.ApiErrorT(c, "Some selected people do not exist or have been deleted")
		return false
	}
	return true
}

func transferTokenOwner(c *gin.Context, token *model.Token, newOwnerId int) bool {
	if newOwnerId <= 0 {
		common.ApiErrorT(c, "Invalid key owner")
		return false
	}
	if newOwnerId == token.UserId {
		return true
	}
	if token.Scene != common.TokenSceneBusiness {
		common.ApiErrorT(c, "Only business keys can be transferred")
		return false
	}

	newOwner, err := model.GetUserById(newOwnerId, true)
	if err != nil {
		common.ApiErrorT(c, "The selected owner does not exist or has been deleted")
		return false
	}
	if newOwner.Status != common.UserStatusEnabled {
		common.ApiErrorT(c, "The selected owner's account is not enabled")
		return false
	}
	if token.Group != "" {
		if _, ok := service.GetUserUsableGroups(newOwner.Group)[token.Group]; !ok {
			common.ApiErrorT(c, "The selected owner cannot use group {{group}}",
				map[string]any{"group": token.Group})
			return false
		}
	}

	token.UserId = newOwnerId
	members := token.GetProjectMembers()
	kept := make([]int, 0, len(members))
	for _, id := range members {
		if id != newOwnerId {
			kept = append(kept, id)
		}
	}
	if err := token.SetProjectMembers(kept); err != nil {
		common.ApiError(c, err)
		return false
	}
	return true
}

func setTokenAutoGroups(c *gin.Context, token *model.Token, groups []string) bool {
	if len(groups) == 0 {
		if err := token.SetAutoGroups(nil); err != nil {
			common.ApiError(c, err)
			return false
		}
		return true
	}

	maxCount := setting.GetMaxTokenAutoGroups()
	if len(groups) > maxCount {
		common.ApiErrorT(c, "A token can select at most {{max}} Auto groups", map[string]any{"max": maxCount})
		return false
	}

	userGroup, err := getTokenRequestUserGroup(c)
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if _, ok := seen[group]; ok {
			common.ApiErrorT(c, "Auto group {{group}} is duplicated", map[string]any{"group": group})
			return false
		}
		seen[group] = struct{}{}
		if !service.IsUserSelectableGroup(userGroup, group) {
			common.ApiErrorT(c, "Auto group {{group}} is unavailable or unauthorized", map[string]any{"group": group})
			return false
		}
	}

	if err := token.SetAutoGroups(groups); err != nil {
		common.ApiError(c, err)
		return false
	}
	return true
}

// parseTokenFilter 读取列表与搜索共用的筛选参数
func parseTokenFilter(c *gin.Context) (model.TokenFilter, bool) {
	filter := model.TokenFilter{UserId: c.GetInt("id")}

	switch scene := c.Query("scene"); scene {
	case "":
	case string(common.TokenSceneOffice), string(common.TokenSceneBusiness):
		filter.Scene = common.TokenScene(scene)
	default:
		common.ApiErrorT(c, "Invalid scene filter")
		return filter, false
	}

	switch routingMode := c.Query("routing_mode"); routingMode {
	case "":
	case string(common.TokenRoutingModeSmart), string(common.TokenRoutingModeFixed):
		filter.RoutingMode = common.TokenRoutingMode(routingMode)
	default:
		common.ApiErrorT(c, "Invalid routing mode filter")
		return filter, false
	}

	if statusParam := c.Query("status"); statusParam != "" {
		status, err := strconv.Atoi(statusParam)
		if err != nil || !slices.Contains([]int{
			common.TokenStatusEnabled,
			common.TokenStatusDisabled,
			common.TokenStatusExpired,
			common.TokenStatusExhausted,
		}, status) {
			common.ApiErrorT(c, "Invalid status filter")
			return filter, false
		}
		filter.Status = status
	}

	switch view := c.Query("view"); view {
	case "", "personal":
		filter.Scope = common.TokenScopePersonal
	case "business":
		filter.Scope = common.TokenScopeBusiness
		filter.Scene = common.TokenSceneBusiness
		isLeader, err := model.IsUserLeader(filter.UserId)
		if err != nil {
			common.ApiError(c, err)
			return filter, false
		}
		if isLeader {
			teamUserIds, err := model.GetLeaderTeamUserIds(filter.UserId)
			if err != nil {
				common.ApiError(c, err)
				return filter, false
			}
			filter.TeamUserIds = teamUserIds
		}
	default:
		common.ApiErrorT(c, "Invalid view filter")
		return filter, false
	}

	return filter, true
}

func GetAllTokens(c *gin.Context) {
	filter, ok := parseTokenFilter(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	tokens, total, err := model.ListTokens(filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildTokenListItems(tokens, filter.Scope, filter.UserId))
	common.ApiSuccess(c, pageInfo)
}

func SearchTokens(c *gin.Context) {
	filter, ok := parseTokenFilter(c)
	if !ok {
		return
	}
	keyword := c.Query("keyword")
	token := c.Query("token")

	pageInfo := common.GetPageQuery(c)

	tokens, total, err := model.SearchUserTokens(filter, keyword, token, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildTokenListItems(tokens, filter.Scope, filter.UserId))
	common.ApiSuccess(c, pageInfo)
}

func GetToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, _, err := model.GetTokenForReveal(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	response := buildMaskedTokenResponse(token)
	response.MyRole = model.TokenRoleFor(token, userId)
	common.ApiSuccess(c, response)
}

func GetTokenAutoGroups(c *gin.Context) {
	userGroup, err := getTokenRequestUserGroup(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"groups":    service.GetUserAutoGroup(userGroup),
		"max_count": setting.GetMaxTokenAutoGroups(),
	})
}

func GetTokenKey(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 业务 Key 的管理员、成员，以及创建人的 leader 都可以取明文
	token, actor, err := model.GetTokenForReveal(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	if !actor.IsOwner {
		// 非创建人取明文必须可归因
		params["target_user_id"] = token.UserId
		params["acted_as"] = model.TokenRoleFor(token, userId)
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, gin.H{
		"key": token.GetFullKey(),
	})
}

func GetTokenStatus(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(tokenId, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"object":          "credit_summary",
		"total_granted":   token.RemainQuota,
		"total_used":      0, // not supported currently
		"total_available": token.RemainQuota,
		"expires_at":      expiredAt * 1000,
	})
}

func GetTokenUsage(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "No Authorization header",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid Bearer token",
		})
		return
	}
	tokenKey := parts[1]

	token, err := model.GetTokenByKey(strings.TrimPrefix(tokenKey, "sk-"), false)
	if err != nil {
		common.SysError(common.LogText("failed to get token by key: %s", err.Error()))
		common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	data := gin.H{
		"object":               "token_usage",
		"name":                 token.Name,
		"total_granted":        token.RemainQuota + token.UsedQuota,
		"total_used":           token.UsedQuota,
		"total_available":      token.RemainQuota,
		"unlimited_quota":      token.UnlimitedQuota,
		"model_limits":         token.GetModelLimitsMap(),
		"model_limits_enabled": token.ModelLimitsEnabled,
		"expires_at":           expiredAt,
	}
	// 用户额度只在令牌允许时返回；IP 白名单已由 TokenAuthReadOnly 检查。
	if token.AllowUserQuotaQuery {
		userQuota, err := model.GetUserQuota(token.UserId, false)
		var userUsedQuota int
		if err == nil {
			userUsedQuota, err = model.GetUserUsedQuota(token.UserId)
		}
		if err != nil {
			common.SysError(common.LogText("failed to get user quota: %s", err.Error()))
			common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
			return
		}
		data["user_quota"] = gin.H{
			"total_available": userQuota,
			"total_used":      userUsedQuota,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    true,
		"message": "ok",
		"data":    data,
	})
}

func AddToken(c *gin.Context) {
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	if len(token.Name) > 50 {
		common.ApiErrorT(c, "Token name is too long")
		return
	}
	if !request.validAutoPreference() {
		common.ApiErrorT(c, "Invalid auto model preference")
		return
	}
	params := tokenAuditParams(c)
	params["name"] = token.Name
	// 非无限额度时，检查额度值是否超出有效范围
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorT(c, "Quota value cannot be negative")
			return
		}
		maxQuotaValue := maxTokenQuota()
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorT(c, "Quota value exceeds valid range, maximum is {{max}}", map[string]any{"max": logger.FormatQuota(maxQuotaValue)})
			return
		}
	}
	// 检查用户令牌数量是否已达上限
	maxTokens := operation_setting.GetMaxUserTokens()
	count, err := model.CountUserTokens(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if int(count) >= maxTokens {
		common.ApiErrorT(c, "Maximum number of tokens reached ({{max}})", map[string]any{"max": maxTokens})
		return
	}
	if token.Group == "auto" {
		if !setTokenAutoGroups(c, &token, request.AutoGroups.Groups) {
			return
		}
	} else {
		token.CrossGroupRetry = false
		_ = token.SetAutoGroups(nil)
	}
	key, err := common.GenerateKey()
	if err != nil {
		common.ApiErrorT(c, "Failed to generate token")
		common.SysLog(common.LogText("failed to generate token key: %s", err.Error()))
		return
	}

	cleanToken := model.Token{
		UserId:             c.GetInt("id"),
		Name:               token.Name,
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        token.ExpiredTime,
		RemainQuota:        token.RemainQuota,
		UnlimitedQuota:     token.UnlimitedQuota,
		ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits:        token.ModelLimits,
		AllowIps:           token.AllowIps,
		Group:              token.Group,
		CrossGroupRetry:    token.CrossGroupRetry,
		AutoGroups:         token.AutoGroups,
		// 没传 routing_mode 的令牌存空值，即锁定模型：旧控制台、脚本建的令牌行为不变。
		Scene:       common.NormalizeTokenScene(lo.FromPtr(request.Scene)),
		RoutingMode: common.NormalizeTokenRoutingMode(lo.FromPtr(request.RoutingMode)),
		Description: lo.FromPtr(request.Description),
	}
	if cleanToken.Scene == common.TokenSceneBusiness {
		cleanToken.RoutingMode = common.TokenRoutingModeFixed
	}
	cleanToken.AllowUserQuotaQuery = request.AllowUserQuotaQuery == nil || *request.AllowUserQuotaQuery
	if request.AutoPreference != nil {
		cleanToken.AutoPreference = *request.AutoPreference
	}

	if request.RoutingPreferenceMode != nil {
		if err := cleanToken.SetRoutingPreferenceMode(*request.RoutingPreferenceMode); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if !applyTokenProjectMembers(c, &cleanToken, request.ProjectMembers) {
		return
	}
	if !smartRoutingPoolUsable(&cleanToken) {
		common.ApiErrorT(c, "None of the selected models can be chosen by smart routing. Add a ranked model or switch to fixed model")
		return
	}

	err = cleanToken.Insert()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["id"] = cleanToken.Id
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    buildMaskedTokenResponse(&cleanToken),
	})
}

func DeleteToken(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	err = token.Delete()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func UpdateToken(c *gin.Context) {
	userId := c.GetInt("id")
	statusOnly := c.Query("status_only")
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	params := tokenAuditParams(c)
	if token.Id > 0 {
		params["id"] = token.Id
	}
	if len(token.Name) > 50 {
		common.ApiErrorT(c, "Token name is too long")
		return
	}
	if !request.validAutoPreference() {
		common.ApiErrorT(c, "Invalid auto model preference")
		return
	}
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorT(c, "Quota value cannot be negative")
			return
		}
		maxQuotaValue := maxTokenQuota()
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorT(c, "Quota value exceeds valid range, maximum is {{max}}", map[string]any{"max": logger.FormatQuota(maxQuotaValue)})
			return
		}
	}
	cleanToken, actor, err := model.GetTokenForWrite(token.Id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !actor.IsOwner {
		// 非创建人代操作时留下可归因的审计字段
		params["target_user_id"] = cleanToken.UserId
		params["acted_as"] = model.TokenRoleFor(cleanToken, userId)
	}
	params["name"] = cleanToken.Name
	previous := *cleanToken
	if token.Status == common.TokenStatusEnabled {
		if cleanToken.Status == common.TokenStatusExpired && cleanToken.ExpiredTime <= common.GetTimestamp() && cleanToken.ExpiredTime != -1 {
			common.ApiErrorT(c, "Token has expired and cannot be enabled. Please modify the expiration time or set it to never expire")
			return
		}
		if cleanToken.Status == common.TokenStatusExhausted && cleanToken.RemainQuota <= 0 && !cleanToken.UnlimitedQuota {
			common.ApiErrorT(c, "Token quota is exhausted and cannot be enabled. Please modify the remaining quota or set it to unlimited")
			return
		}
	}
	if statusOnly != "" {
		cleanToken.Status = token.Status
	} else {
		// If you add more fields, please also update token.Update()
		cleanToken.Name = token.Name
		cleanToken.ExpiredTime = token.ExpiredTime
		cleanToken.RemainQuota = token.RemainQuota
		cleanToken.UnlimitedQuota = token.UnlimitedQuota
		cleanToken.ModelLimitsEnabled = token.ModelLimitsEnabled
		cleanToken.ModelLimits = token.ModelLimits
		cleanToken.AllowIps = token.AllowIps
		if request.AllowUserQuotaQuery != nil {
			cleanToken.AllowUserQuotaQuery = *request.AllowUserQuotaQuery
		}
		if request.AutoPreference != nil {
			cleanToken.AutoPreference = *request.AutoPreference
		}
		cleanToken.Group = token.Group
		cleanToken.CrossGroupRetry = token.CrossGroupRetry
		if request.Scene != nil {
			cleanToken.Scene = common.NormalizeTokenScene(*request.Scene)
		}
		if request.RoutingMode != nil {
			cleanToken.RoutingMode = common.NormalizeTokenRoutingMode(*request.RoutingMode)
		}
		if cleanToken.Scene == common.TokenSceneBusiness {
			cleanToken.RoutingMode = common.TokenRoutingModeFixed
		}
		if request.Description != nil {
			cleanToken.Description = *request.Description
		}

		if !applyTokenProjectMembers(c, cleanToken, request.ProjectMembers) {
			return
		}

		if request.ProjectOwnerId != nil && *request.ProjectOwnerId != cleanToken.UserId {
			if !actor.IsOwner {
				common.ApiErrorT(c, "Only the key owner can transfer ownership")
				return
			}
			if !transferTokenOwner(c, cleanToken, *request.ProjectOwnerId) {
				return
			}
		}

		if request.RoutingPreferenceMode != nil {
			if err := cleanToken.SetRoutingPreferenceMode(*request.RoutingPreferenceMode); err != nil {
				common.ApiError(c, err)
				return
			}
		}

		if token.Group != "auto" {
			cleanToken.CrossGroupRetry = false
			_ = cleanToken.SetAutoGroups(nil)
		} else if request.AutoGroups.Set {
			if !setTokenAutoGroups(c, cleanToken, request.AutoGroups.Groups) {
				return
			}
		}
		if !smartRoutingPoolUsable(cleanToken) {
			common.ApiErrorT(c, "None of the selected models can be chosen by smart routing. Add a ranked model or switch to fixed model")
			return
		}
	}
	err = cleanToken.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// routing_preference 不在 Update() 的字段白名单里（上游有测试锁定这个隔离性：
	// 普通编辑不得覆盖专用接口设过的偏好），所以显式传了模式时走专用的单列写入。
	if statusOnly == "" && request.RoutingPreferenceMode != nil {
		if err := cleanToken.UpdateRoutingPreference(); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	params["name"] = cleanToken.Name
	if statusOnly != "" {
		params["from"], params["to"] = previous.Status, cleanToken.Status
	} else {
		changedFields := []string{}
		for _, field := range []struct {
			name    string
			changed bool
		}{
			{"name", previous.Name != cleanToken.Name},
			{"expired_time", previous.ExpiredTime != cleanToken.ExpiredTime},
			{"remain_quota", previous.RemainQuota != cleanToken.RemainQuota},
			{"unlimited_quota", previous.UnlimitedQuota != cleanToken.UnlimitedQuota},
			{"model_limits_enabled", previous.ModelLimitsEnabled != cleanToken.ModelLimitsEnabled},
			{"model_limits", previous.ModelLimits != cleanToken.ModelLimits},
			{"allow_ips", (previous.AllowIps == nil) != (cleanToken.AllowIps == nil) ||
				(previous.AllowIps != nil && cleanToken.AllowIps != nil && *previous.AllowIps != *cleanToken.AllowIps)},
			{"allow_user_quota_query", previous.AllowUserQuotaQuery != cleanToken.AllowUserQuotaQuery},
			{"group", previous.Group != cleanToken.Group},
			{"cross_group_retry", previous.CrossGroupRetry != cleanToken.CrossGroupRetry},
			{"auto_groups", previous.AutoGroups != cleanToken.AutoGroups},
			{"auto_preference", previous.AutoPreference != cleanToken.AutoPreference},
			{"scene", previous.Scene != cleanToken.Scene},
			{"routing_mode", previous.RoutingMode != cleanToken.RoutingMode},
			{"description", previous.Description != cleanToken.Description},
			{"routing_preference", previous.RoutingPreference != cleanToken.RoutingPreference},
			{"owner", previous.UserId != cleanToken.UserId},
			{"project_members", previous.ProjectMembers != cleanToken.ProjectMembers},
		} {
			if field.changed {
				changedFields = append(changedFields, field.name)
			}
		}
		params["changed_fields"] = changedFields
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    buildMaskedTokenResponse(cleanToken),
	})
}

type TokenBatch struct {
	Ids []int `json:"ids"`
}

func DeleteTokenBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	params := tokenBatchAuditParams(c, tokenBatch.Ids)
	if len(tokenBatch.Ids) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	userId := c.GetInt("id")
	count, err := model.BatchDeleteTokens(tokenBatch.Ids, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["count"] = count
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
}

func GetTokenKeysBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	params := tokenBatchAuditParams(c, tokenBatch.Ids)
	if len(tokenBatch.Ids) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if len(tokenBatch.Ids) > 100 {
		common.ApiErrorT(c, "Too many items in batch request, maximum is {{max}}", map[string]any{"max": 100})
		return
	}
	userId := c.GetInt("id")
	tokens, err := model.GetTokenKeysByIds(tokenBatch.Ids, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	keysMap := make(map[int]string)
	returnedIDs := make([]int, 0, len(tokens))
	for _, t := range tokens {
		keysMap[t.Id] = t.GetFullKey()
		returnedIDs = append(returnedIDs, t.Id)
	}
	params["count"] = len(tokens)
	params["returned_ids"] = returnedIDs
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, gin.H{"keys": keysMap})
}

// ListUserTokens lets an admin list another user's tokens (masked keys only),
// e.g. to populate the token filter of the admin usage records page.
func ListUserTokens(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !canManageTargetRole(c.GetInt("role"), user.Role) {
		common.ApiErrorT(c, "No permission to access users of same or higher level")
		return
	}
	filter, ok := parseTokenFilter(c)
	if !ok {
		return
	}
	filter.UserId = userId
	filter.Scope = common.TokenScopeOwned
	filter.TeamUserIds = nil
	pageInfo := common.GetPageQuery(c)
	tokens, total, err := model.ListTokens(filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

// attachTokenPeople 一次性批量查用户
func attachTokenPeople(
	responses []*tokenResponse,
	tokens []*model.Token,
	userId int,
) []*tokenResponse {
	idSet := make(map[int]struct{})
	membersByToken := make(map[int][]int, len(tokens))
	for _, token := range tokens {
		idSet[token.UserId] = struct{}{}
		var members []int
		if token.IsProjectKey() {
			members = token.GetProjectMembers()
		}
		membersByToken[token.Id] = members
		for _, id := range members {
			idSet[id] = struct{}{}
		}
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	briefs, err := model.GetUserBriefsByIds(ids)
	if err != nil {
		common.SysError(common.LogText("failed to load token people: %s", err.Error()))
		briefs = nil
	}
	for i, response := range responses {
		if response == nil || response.Token == nil {
			continue
		}
		response.MyRole = model.TokenRoleFor(tokens[i], userId)
		if briefs == nil {
			continue
		}
		if owner, ok := briefs[response.UserId]; ok {
			response.Owner = &owner
		}
		for _, memberId := range membersByToken[response.Id] {
			if brief, ok := briefs[memberId]; ok {
				response.ProjectMembers = append(response.ProjectMembers, brief)
			}
		}
	}
	return responses
}

// buildTokenListItems 按 scope 决定是否回填人员信息，并回填本月用量
func buildTokenListItems(tokens []*model.Token, scope common.TokenViewScope, userId int) []*tokenResponse {
	responses := buildMaskedTokenResponses(tokens)
	attachTokenMonthlyUsage(responses)
	if scope == common.TokenScopeOwned {
		return responses
	}
	return attachTokenPeople(responses, tokens, userId)
}

// attachTokenMonthlyUsage 一次查出这一页令牌本月的调用次数和额度。查询失败只记日志，
// 字段留空，列表照常返回。
func attachTokenMonthlyUsage(responses []*tokenResponse) {
	ids := make([]int, 0, len(responses))
	for _, response := range responses {
		if response != nil && response.Token != nil {
			ids = append(ids, response.Id)
		}
	}
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()
	usage, err := model.GetTokensUsageSince(ids, monthStart)
	if err != nil {
		common.SysError(common.LogText("failed to load token monthly usage: %s", err.Error()))
		return
	}
	for _, response := range responses {
		if response == nil || response.Token == nil {
			continue
		}
		row := usage[response.Id]
		response.MonthlyCalls, response.MonthlyQuota = &row.Calls, &row.Quota
	}
}

// GetKeyViewScope 当前用户在业务视图里是不是 leader
func GetKeyViewScope(c *gin.Context) {
	userId := c.GetInt("id")
	isLeader, err := model.IsUserLeader(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	memberCount := 0
	if isLeader {
		teamUserIds, err := model.GetLeaderTeamUserIds(userId)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		memberCount = len(teamUserIds)
	}
	common.ApiSuccess(c, gin.H{"is_leader": isLeader, "member_count": memberCount})
}

// SearchTokenMemberCandidates 供业务 Key 选管理员与项目成员用
func SearchTokenMemberCandidates(c *gin.Context) {
	candidates, err := model.SearchMemberCandidates(c.Query("keyword"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, candidates)
}
