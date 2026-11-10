package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomDashboardRoutesEnforceAccessTokenScopes(t *testing.T) {
	for _, tc := range []struct {
		method, pattern, path, scope string
		rootOnly                     bool
	}{
		{http.MethodGet, "/api/token/:id/routing-preference", "/api/token/7/routing-preference", "api_key:read", false},
		{http.MethodPut, "/api/token/:id/routing-preference", "/api/token/7/routing-preference", "api_key:write", false},
		{http.MethodGet, "/api/scheduler/config", "/api/scheduler/config", "option:read", true},
		{http.MethodPut, "/api/scheduler/config", "/api/scheduler/config", "option:write", true},
		{http.MethodGet, "/api/scheduler/monitor", "/api/scheduler/monitor", "ops:read", true},
	} {
		t.Run(tc.method+" "+tc.pattern, func(t *testing.T) {
			user, _ := setupAccessTokenAudit(t)
			require.NoError(t, model.DB.Model(user).Update("role", common.RoleRootUser).Error)
			router := gin.New()
			auth := middleware.AdminAuth()
			if tc.rootOnly {
				auth = middleware.RootAuth()
			}
			router.Handle(tc.method, tc.pattern, auth, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			wrongScope, _ := createScopedAccessToken(t, user.Id, 0, "profile:read")
			assert.Equal(t, http.StatusForbidden, auditRequest(router, tc.method, tc.path, wrongScope).Code)
			allowed, _ := createScopedAccessToken(t, user.Id, 0, tc.scope)
			assert.Equal(t, http.StatusNoContent, auditRequest(router, tc.method, tc.path, allowed).Code)
		})
	}
}
