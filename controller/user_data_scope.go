package controller

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// OrgPaths are granted as a whole; their people in MemberIds are not stored one by one.
type userDataScopeRequest struct {
	MemberIds []int    `json:"member_ids"`
	OrgPaths  []string `json:"org_paths"`
}

// Only these fields leave the server; the SSO list also carries phone, bank and HR data.
type ssoOrgPerson struct {
	Uid     int    `json:"uid"`
	Name    string `json:"name"`
	Avatar  string `json:"avatar"`
	OrgPath string `json:"org_path"`
	// 0 without a MaaS account.
	UserId     int `json:"user_id"`
	UserStatus int `json:"user_status"`
}

const ssoOrgCacheTTL = 5 * time.Minute

var (
	ssoOrgCacheMu    sync.Mutex
	ssoOrgCacheUsers []ssoUserListItem
	ssoOrgCacheUntil time.Time
)

func cachedSsoUsers() ([]ssoUserListItem, error) {
	ssoOrgCacheMu.Lock()
	defer ssoOrgCacheMu.Unlock()
	if ssoOrgCacheUsers != nil && time.Now().Before(ssoOrgCacheUntil) {
		return ssoOrgCacheUsers, nil
	}
	users, err := fetchAllSsoUsers()
	if err != nil {
		return nil, err
	}
	ssoOrgCacheUsers = users
	ssoOrgCacheUntil = time.Now().Add(ssoOrgCacheTTL)
	return users, nil
}

// GetUserDataScopeSsoOrg returns enabled=false when the SSO user list is not configured.
func GetUserDataScopeSsoOrg(c *gin.Context) {
	if common.SsoUserListApiUrl == "" || common.SsoUserListPlatformId == "" || common.SsoUserListSecret == "" {
		common.ApiSuccess(c, gin.H{"enabled": false, "people": []ssoOrgPerson{}})
		return
	}
	users, err := cachedSsoUsers()
	if err != nil {
		common.SysError("failed to load SSO organization: " + err.Error())
		common.ApiErrorT(c, "Failed to load the SSO organization")
		return
	}
	emails := make([]string, 0, len(users))
	for _, user := range users {
		emails = append(emails, user.Email)
	}
	accounts, err := model.GetUserAccountsByEmail(emails)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	people := make([]ssoOrgPerson, 0, len(users))
	for _, user := range users {
		account := accounts[strings.ToLower(strings.TrimSpace(user.Email))]
		people = append(people, ssoOrgPerson{
			Uid:        user.Uid,
			Name:       strings.TrimSpace(user.Name),
			Avatar:     strings.TrimSpace(user.Avatar),
			OrgPath:    strings.TrimSpace(user.DepartmentTreePathStr),
			UserId:     account.Id,
			UserStatus: account.Status,
		})
	}
	common.ApiSuccess(c, gin.H{"enabled": true, "people": people})
}

func GetUserDataScopeDirectory(c *gin.Context) {
	entries, err := model.GetUserDirectory()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, entries)
}

func ListUserDataScopes(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	managers, total, err := model.ListUserDataScopeManagers(strings.TrimSpace(c.Query("keyword")), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(managers)
	common.ApiSuccess(c, pageInfo)
}

func GetUserDataScope(c *gin.Context) {
	managerId, err := strconv.Atoi(c.Param("manager_id"))
	if err != nil || managerId <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	respondUserDataScope(c, managerId)
}

// respondUserDataScope reports everyone covered now and the granted departments.
func respondUserDataScope(c *gin.Context, managerId int) {
	memberIds, err := model.GetUserDataScopeMemberIds(managerId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	orgPaths, err := model.GetUserDataScopeOrgPaths(managerId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"manager_id": managerId, "member_ids": memberIds, "org_paths": orgPaths})
}

func UpdateUserDataScope(c *gin.Context) {
	managerId, err := strconv.Atoi(c.Param("manager_id"))
	if err != nil || managerId <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	var req userDataScopeRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if _, _, err := model.ReplaceUserDataScope(managerId, req.MemberIds, req.OrgPaths, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	respondUserDataScope(c, managerId)
}

func DeleteUserDataScope(c *gin.Context) {
	managerId, err := strconv.Atoi(c.Param("manager_id"))
	if err != nil || managerId <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	if err := model.DeleteUserDataScope(managerId); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
