package service

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

type SsoUserInfo struct {
	Uid                   int    `json:"uid"`
	Email                 string `json:"email"`
	Name                  string `json:"name"`
	Avatar                string `json:"avatar"`
	DepartmentTreePathStr string `json:"department_tree_path_str"`
	// StraightLineManagerUid 是直属上级的 SSO uid，0 表示顶层或该接口未返回此字段。
	// 类型与 ssoUserListItem 和 User.StraightLineManagerUid 保持一致。
	StraightLineManagerUid int64 `json:"straight_line_manager_uid"`
}

func SyncLoginUserLeader(userId int, managerUid int64) {
	if userId <= 0 {
		return
	}
	if managerUid <= 0 {
		common.SysLog(common.LogText("SSO login: no straight-line manager for user %d, leader edge left to full sync", userId))
		return
	}
	if err := model.SyncUserLeader(userId, strconv.FormatInt(managerUid, 10)); err != nil {
		common.SysError(common.LogText("SSO login: failed to sync leader edge for user %d: %s", userId, err.Error()))
	}
}

func parseOrgInfo(departmentTreePathStr string) (secondary, tertiary, team string) {
	departmentTreePathStr = strings.TrimSpace(departmentTreePathStr)
	if departmentTreePathStr == "" {
		return
	}
	parts := strings.Split(departmentTreePathStr, "/")
	switch len(parts) {
	case 1:
		secondary = parts[0]
		tertiary = parts[0]
		team = parts[0]
	case 2:
		secondary = parts[0]
		tertiary = parts[1]
		team = parts[1]
	default:
		secondary = parts[0]
		tertiary = parts[1]
		team = parts[len(parts)-1]
	}
	// CC is a sub-division marker: merge it into secondary for clarity
	if tertiary == "CC" {
		secondary = secondary + "-CC"
	}
	return
}

func VerifySsoToken(token string) (*SsoUserInfo, error) {
	if common.HoidcSsoApi == "" {
		return nil, errors.New("HOIDC SSO API not configured")
	}
	verifyUrl := strings.TrimRight(common.HoidcSsoApi, "/") +
		"/auth/verify-access-token?token=" + url.QueryEscape(token)
	body := []byte(`{"platform_id":"` + common.HoidcPlatformId + `"}`)

	req, err := http.NewRequest(http.MethodPost, verifyUrl, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Token", token)

	client := GetHttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SSO token verification request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("SSO token verification failed: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Code int         `json:"code"`
		Msg  string      `json:"msg"`
		Data SsoUserInfo `json:"data"`
	}
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse SSO response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("SSO token verification failed: %s", result.Msg)
	}
	if result.Data.Email == "" {
		return nil, errors.New("SSO returned empty email")
	}
	return &result.Data, nil
}

// ProvisionSsoUser creates or refreshes the local account of an SSO user.
// Like the org fields, the reporting-line and org-id values only overwrite an
// existing user when they are non-empty and changed, so callers without them
// (the login path) pass zero values to leave the stored columns untouched.
// isCharge must be able to drop to 0: the sync passes it, the login path passes nil.
func ProvisionSsoUser(
	hoidcId, email, name, avatar, departmentTreePathStr string,
	straightLineManagerEmail, straightLineManagerName string,
	straightLineManagerUid, mentorUid, dottedLineManagerUid, organizationOid, departmentOid int64,
	isCharge *int,
	allowSignup bool,
) (*model.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return nil, errors.New("email is empty")
	}

	secondary, tertiary, team := parseOrgInfo(departmentTreePathStr)
	emailPrefix := strings.Split(email, "@")[0]

	var user model.User
	err := model.DB.Where("username = ?", emailPrefix).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = model.DB.Where("email = ?", email).First(&user).Error
	}

	if err == nil {
		if user.DeletedAt.Valid {
			return nil, errors.New("account has been disabled")
		}
		updates := map[string]any{
			"updated_at": time.Now().Unix(),
		}
		if hoidcId != "" && hoidcId != user.HoidcId {
			updates["hoidc_id"] = hoidcId
		}
		if name != "" && truncateStr(name, 20) != user.DisplayName {
			updates["display_name"] = truncateStr(name, 20)
		}
		if avatar != "" && avatar != user.Avatar {
			updates["avatar"] = avatar
		}
		if user.Email == "" && email != "" {
			updates["email"] = email
		}
		if secondary != "" && secondary != user.SecondaryOrganization {
			updates["secondary_organization"] = secondary
		}
		if tertiary != "" && tertiary != user.TertiaryOrganization {
			updates["tertiary_organization"] = tertiary
		}
		if team != "" && team != user.Team {
			updates["team"] = team
		}
		if departmentTreePathStr != "" && departmentTreePathStr != user.OrgStr {
			updates["org_str"] = departmentTreePathStr
		}
		if straightLineManagerEmail != "" && straightLineManagerEmail != user.StraightLineManagerEmail {
			updates["straight_line_manager_email"] = straightLineManagerEmail
		}
		if straightLineManagerName != "" && straightLineManagerName != user.StraightLineManagerName {
			updates["straight_line_manager_name"] = straightLineManagerName
		}
		if straightLineManagerUid != 0 && straightLineManagerUid != user.StraightLineManagerUid {
			updates["straight_line_manager_uid"] = straightLineManagerUid
		}
		if mentorUid != 0 && mentorUid != user.MentorUid {
			updates["mentor_uid"] = mentorUid
		}
		if dottedLineManagerUid != 0 && dottedLineManagerUid != user.DottedLineManagerUid {
			updates["dotted_line_manager_uid"] = dottedLineManagerUid
		}
		if organizationOid != 0 && organizationOid != user.OrganizationOid {
			updates["organization_oid"] = organizationOid
		}
		if departmentOid != 0 && departmentOid != user.DepartmentOid {
			updates["department_oid"] = departmentOid
		}
		if isCharge != nil && *isCharge != user.IsCharge {
			updates["is_charge"] = *isCharge
		}
		if len(updates) > 0 {
			model.DB.Model(&user).Updates(updates)
		}
		return &user, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	if !allowSignup {
		return nil, errors.New("account not found and signup is disabled")
	}

	username := generateSsoUsername(email)
	newUser := &model.User{
		Email:                 email,
		Username:              username,
		DisplayName:           truncateStr(name, 20),
		Avatar:                avatar,
		Password:              "",
		Role:                  common.RoleCommonUser,
		Status:                common.UserStatusEnabled,
		HoidcId:               hoidcId,
		SecondaryOrganization: secondary,
		TertiaryOrganization:  tertiary,
		Team:                  team,
		OrgStr:                departmentTreePathStr,

		StraightLineManagerEmail: straightLineManagerEmail,
		StraightLineManagerName:  straightLineManagerName,
		StraightLineManagerUid:   straightLineManagerUid,
		MentorUid:                mentorUid,
		DottedLineManagerUid:     dottedLineManagerUid,
		OrganizationOid:          organizationOid,
		DepartmentOid:            departmentOid,
		QuotaTierAutoAssign:      true,
	}
	if isCharge != nil {
		newUser.IsCharge = *isCharge
	}
	if err := newUser.InsertSso(0); err != nil {
		return nil, fmt.Errorf("failed to create SSO user: %w", err)
	}
	if operation_setting.IsQuotaTierEnabled() {
		if _, err := model.EnsureUserQuotaTier(newUser.Id); err != nil {
			common.SysError(common.LogText("failed to assign the quota tier of new SSO user %d: %s", newUser.Id, err.Error()))
		}
	}
	return newUser, nil
}

func generateSsoUsername(email string) string {
	prefix := strings.Split(email, "@")[0]
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	username := prefix
	for i := range 10 {
		var count int64
		model.DB.Model(&model.User{}).Where("username = ?", username).Count(&count)
		if count == 0 {
			return username
		}
		username = fmt.Sprintf("%s%04d", prefix, rand.IntN(10000))
		_ = i
	}
	return username
}

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}
