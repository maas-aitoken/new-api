package controller

import (
	"cmp"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

func SsoHoidcLogin(c *gin.Context) {
	if !common.HoidcEnabled {
		common.ApiErrorT(c, "HOIDC SSO is not enabled")
		return
	}
	if common.HoidcLoginPage == "" || common.HoidcPlatformId == "" {
		common.ApiErrorT(c, "HOIDC SSO is not properly configured")
		return
	}

	redirect := sanitizeHoidcRedirect(c, c.Query("redirect"))
	// The first configured origin wins; the request origin is the fallback.
	serverAddr := cmp.Or(hoidcTrustedOrigins(c)...)

	callbackUrl := serverAddr +
		"/api/sso/hoidc/callback?platform_id=" + url.QueryEscape(common.HoidcPlatformId) +
		"&redirect=" + url.QueryEscape(redirect)

	loginUrl := common.HoidcLoginPage +
		"?platform_id=" + url.QueryEscape(common.HoidcPlatformId) +
		"&redirect=" + url.QueryEscape(callbackUrl)

	c.Redirect(http.StatusFound, loginUrl)
}

func SsoHoidcCallback(c *gin.Context) {
	if !common.HoidcEnabled {
		common.ApiErrorT(c, "HOIDC SSO is not enabled")
		return
	}

	token := c.Query("token")
	if token == "" {
		common.ApiErrorT(c, "Missing SSO token")
		return
	}

	// The callback can be reached without our login endpoint, so it checks the
	// redirect again.
	redirect := sanitizeHoidcRedirect(c, c.Query("redirect"))

	userInfo, err := service.VerifySsoToken(token)
	if err != nil {
		common.SysError("SSO token verification failed: " + err.Error())
		common.ApiErrorT(c, "SSO login failed")
		return
	}

	// Reporting line and org ids are left to the SSO user sync task.
	user, err := service.ProvisionSsoUser(
		strconv.Itoa(userInfo.Uid), userInfo.Email, userInfo.Name, userInfo.Avatar, userInfo.DepartmentTreePathStr,
		"", "", 0, 0, 0, 0, 0, nil,
		common.HoidcAllowSignup,
	)
	if err != nil {
		common.SysError("SSO user provision failed: " + err.Error())
		common.ApiErrorT(c, "SSO login failed: "+err.Error())
		return
	}

	// 补一条本人的汇报边，覆盖新员工在两次 SSO 全量同步之间登录的冷启动窗口。
	// 失败不阻断登录：边缺失只影响密钥业务视图的可见范围。
	service.SyncLoginUserLeader(user.Id, userInfo.StraightLineManagerUid)

	bundle, err := service.CreateLoginSession(
		user.Id,
		"sso_hoidc",
		c.ClientIP(),
		c.Request.UserAgent(),
	)
	if err != nil {
		common.SysError("SSO session creation failed: " + err.Error())
		common.ApiErrorT(c, "SSO login failed")
		return
	}

	service.WriteRefreshCookie(c, bundle.RefreshToken)

	c.Set("login_method", "sso_hoidc")
	model.UpdateUserLastLoginAt(user.Id)
	recordLoginAudit(user, c)

	// The frontends restore the session from the refresh cookie, so the access
	// token never goes into the URL.
	c.Redirect(http.StatusFound, redirect)
}

// hoidcTrustedOrigins lists the origins HOIDC may return to, in the order the
// login callback prefers them: the configured redirect origin, the server
// address, then the origin of the current request. Empty entries are kept.
func hoidcTrustedOrigins(c *gin.Context) []string {
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return []string{
		strings.TrimRight(common.HoidcRedirectOrigin, "/"),
		strings.TrimRight(system_setting.ServerAddress, "/"),
		scheme + "://" + c.Request.Host,
	}
}

// sanitizeHoidcRedirect keeps a post-login redirect on this site: a relative
// path, or an absolute URL on one of the trusted origins. Anything else becomes
// "/", so a crafted login link cannot send the user to another site.
func sanitizeHoidcRedirect(c *gin.Context, raw string) string {
	if raw == "" || strings.Contains(raw, "\\") || strings.ContainsFunc(raw, unicode.IsControl) {
		return "/"
	}
	if strings.HasPrefix(raw, "/") {
		if strings.HasPrefix(raw, "//") {
			return "/"
		}
		return raw
	}
	target, err := url.Parse(raw)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.Host == "" {
		return "/"
	}
	for _, origin := range hoidcTrustedOrigins(c) {
		trusted, err := url.Parse(origin)
		if err != nil || trusted.Host == "" {
			continue
		}
		if strings.EqualFold(trusted.Scheme, target.Scheme) && strings.EqualFold(trusted.Host, target.Host) {
			return raw
		}
	}
	return "/"
}
