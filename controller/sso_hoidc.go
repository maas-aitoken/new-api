package controller

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
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

	redirect := c.Query("redirect")
	if redirect == "" {
		redirect = "/"
	}

	serverAddr := ""
	if common.HoidcRedirectOrigin != "" {
		serverAddr = strings.TrimRight(common.HoidcRedirectOrigin, "/")
	}
	if serverAddr == "" {
		serverAddr = strings.TrimRight(system_setting.ServerAddress, "/")
	}
	if serverAddr == "" {
		scheme := "http"
		if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		serverAddr = scheme + "://" + c.Request.Host
	}

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

	redirect := c.Query("redirect")
	if redirect == "" {
		redirect = "/"
	}

	userInfo, err := service.VerifySsoToken(token)
	if err != nil {
		common.SysError("SSO token verification failed: " + err.Error())
		common.ApiErrorT(c, "SSO login failed")
		return
	}

	// Reporting line and org ids are left to the SSO user sync task.
	user, err := service.ProvisionSsoUser(
		strconv.Itoa(userInfo.Uid), userInfo.Email, userInfo.Name, userInfo.Avatar, userInfo.DepartmentTreePathStr,
		"", "", 0, 0, 0, 0, 0, 0,
		common.HoidcAllowSignup,
	)
	if err != nil {
		common.SysError("SSO user provision failed: " + err.Error())
		common.ApiErrorT(c, "SSO login failed: "+err.Error())
		return
	}

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
	recordLoginAudit(user, c)

	redirectUrl := redirect
	if strings.Contains(redirectUrl, "?") {
		redirectUrl += "&access_token=" + url.QueryEscape(bundle.AccessToken)
	} else {
		redirectUrl += "?access_token=" + url.QueryEscape(bundle.AccessToken)
	}
	c.Redirect(http.StatusFound, redirectUrl)
}
