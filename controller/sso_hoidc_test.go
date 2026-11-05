package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configureHoidcTest enables HOIDC with maas.example.com as the frontend origin
// and ssoAPI as the token verification endpoint.
func configureHoidcTest(t *testing.T, ssoAPI string) {
	t.Helper()
	previousEnabled, previousLogin, previousAPI := common.HoidcEnabled, common.HoidcLoginPage, common.HoidcSsoApi
	previousPlatform, previousSignup, previousOrigin := common.HoidcPlatformId, common.HoidcAllowSignup, common.HoidcRedirectOrigin
	previousServer := system_setting.ServerAddress
	common.HoidcEnabled, common.HoidcLoginPage, common.HoidcSsoApi = true, "https://sso.example.com/login", ssoAPI
	common.HoidcPlatformId, common.HoidcAllowSignup, common.HoidcRedirectOrigin = "maas", false, "https://maas.example.com"
	system_setting.ServerAddress = "https://api.example.com"
	t.Cleanup(func() {
		common.HoidcEnabled, common.HoidcLoginPage, common.HoidcSsoApi = previousEnabled, previousLogin, previousAPI
		common.HoidcPlatformId, common.HoidcAllowSignup, common.HoidcRedirectOrigin = previousPlatform, previousSignup, previousOrigin
		system_setting.ServerAddress = previousServer
	})
}

// hoidcRedirectCases pairs a requested redirect with where the user may land.
var hoidcRedirectCases = []struct{ name, redirect, want string }{
	{"empty", "", "/"},
	{"relative path", "/model-square", "/model-square"},
	{"redirect origin", "https://maas.example.com/sso-callback", "https://maas.example.com/sso-callback"},
	{"server address in other case", "https://API.example.com/model-square", "https://API.example.com/model-square"},
	{"external site", "https://evil.example/steal", "/"},
	{"protocol relative", "//evil.example/steal", "/"},
	{"backslash", "/\\evil.example", "/"},
	{"control character", "/\t/evil.example", "/"},
	{"userinfo", "https://maas.example.com@evil.example/", "/"},
	{"lookalike host", "https://maas.example.com.evil.example/", "/"},
	{"other scheme", "http://maas.example.com/sso-callback", "/"},
	{"script url", "javascript:alert(1)", "/"},
}

func TestSsoHoidcLoginKeepsRedirectOnSite(t *testing.T) {
	configureHoidcTest(t, "https://sso.example.com/api")
	for _, test := range hoidcRedirectCases {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/sso/hoidc/login?redirect="+url.QueryEscape(test.redirect), nil)
			SsoHoidcLogin(c)
			require.Equal(t, http.StatusFound, response.Code, response.Body.String())
			login, err := url.Parse(response.Header().Get("Location"))
			require.NoError(t, err)
			callback, err := url.Parse(login.Query().Get("redirect"))
			require.NoError(t, err)
			assert.Equal(t, "https://maas.example.com/api/sso/hoidc/callback", callback.Scheme+"://"+callback.Host+callback.Path)
			assert.Equal(t, test.want, callback.Query().Get("redirect"))
		})
	}
}

// The callback is reachable without our login endpoint, so it sanitizes the
// redirect itself and never puts the session token into the URL.
func TestSsoHoidcCallbackRedirectsWithoutAccessToken(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/auth/verify-access-token", r.URL.Path)
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"uid":10001,"email":"` + user.Username + `@example.com","name":"HOIDC User"}}`))
	}))
	t.Cleanup(upstream.Close)
	configureHoidcTest(t, upstream.URL)
	for _, test := range hoidcRedirectCases {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/sso/hoidc/callback?token=sso-token&redirect="+url.QueryEscape(test.redirect), nil)
			SsoHoidcCallback(c)
			require.Equal(t, http.StatusFound, response.Code, response.Body.String())
			assert.Equal(t, test.want, response.Header().Get("Location"))
			assert.Contains(t, strings.Join(response.Header().Values("Set-Cookie"), "\n"), service.RefreshCookieName+"=")
		})
	}
}
