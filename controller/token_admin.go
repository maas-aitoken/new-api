package controller

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func adminToken(c *gin.Context) (*model.Token, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, false
	}
	token, err := model.GetTokenById(id)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	params["target_user_id"] = token.UserId
	return token, true
}

// AdminListTokens root 查看全站 Key
func AdminListTokens(c *gin.Context) {
	filter := model.AdminTokenFilter{
		Keyword: c.Query("keyword"),
		Key:     c.Query("token"),
	}
	if userIdParam := c.Query("user_id"); userIdParam != "" {
		userId, err := strconv.Atoi(userIdParam)
		if err != nil || userId <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		filter.UserId = userId
	}
	switch scene := c.Query("scene"); scene {
	case "":
	case string(common.TokenSceneOffice), string(common.TokenSceneBusiness):
		filter.Scene = common.TokenScene(scene)
	default:
		common.ApiErrorT(c, "Invalid scene filter")
		return
	}
	switch routingMode := c.Query("routing_mode"); routingMode {
	case "":
	case string(common.TokenRoutingModeSmart), string(common.TokenRoutingModeFixed):
		filter.RoutingMode = common.TokenRoutingMode(routingMode)
	default:
		common.ApiErrorT(c, "Invalid routing mode filter")
		return
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
			return
		}
		filter.Status = status
	}

	pageInfo := common.GetPageQuery(c)
	tokens, total, err := model.ListAllTokens(filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}

	responses := buildMaskedTokenResponses(tokens)
	attachTokenMonthlyUsage(responses)
	ownerIds := make([]int, 0, len(tokens))
	for _, token := range tokens {
		ownerIds = append(ownerIds, token.UserId)
	}
	briefs, err := model.GetUserBriefsByIds(ownerIds)
	if err != nil {
		common.SysError(common.LogText("failed to load token owners: %s", err.Error()))
		briefs = nil
	}
	for _, response := range responses {
		if response == nil || response.Token == nil {
			continue
		}
		if owner, found := briefs[response.UserId]; found {
			response.Owner = &owner
		}
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(responses)
	common.ApiSuccess(c, pageInfo)
}

func AdminUpdateTokenStatus(c *gin.Context) {
	token, ok := adminToken(c)
	if !ok {
		return
	}
	request := struct {
		Status int `json:"status"`
	}{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}

	previousStatus := token.Status
	switch request.Status {
	case common.TokenStatusDisabled:
		token.Status = common.TokenStatusDisabled
	case common.TokenStatusEnabled:
		if token.ExpiredTime != -1 && token.ExpiredTime <= common.GetTimestamp() {
			common.ApiErrorT(c, "Token has expired and cannot be enabled. Please modify the expiration time or set it to never expire")
			return
		}
		if !token.UnlimitedQuota && token.RemainQuota <= 0 {
			common.ApiErrorT(c, "Token quota is exhausted and cannot be enabled. Please modify the remaining quota or set it to unlimited")
			return
		}
		token.Status = common.TokenStatusEnabled
	default:
		common.ApiErrorT(c, "Invalid token status")
		return
	}

	if err := token.UpdateStatus(); err != nil {
		common.ApiError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["from"], params["to"] = previousStatus, token.Status
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, buildMaskedTokenResponse(token))
}

// AdminUpdateTokenRateLimit 改 RPM：0 继承平台级配置，-1 不限流，正数为该 Key 上限
func AdminUpdateTokenRateLimit(c *gin.Context) {
	token, ok := adminToken(c)
	if !ok {
		return
	}
	request := struct {
		RPM *int `json:"rpm"`
	}{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.RPM == nil || *request.RPM < -1 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	params := tokenAuditParams(c)
	params["from"], params["to"] = token.RPM, *request.RPM
	token.RPM = *request.RPM
	if err := token.UpdateRateLimit(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, buildMaskedTokenResponse(token))
}

func AdminDeleteToken(c *gin.Context) {
	token, ok := adminToken(c)
	if !ok {
		return
	}
	if err := token.Delete(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}
