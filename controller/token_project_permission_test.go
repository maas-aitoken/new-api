package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// callAs 发一个带身份的请求并返回解码后的响应。
func callAs(t *testing.T, userID int, method, target string, body any, handler func(*gin.Context), params ...gin.Param) tokenAPIResponse {
	t.Helper()
	ctx, recorder := newAuthenticatedContext(t, method, target, body, userID)
	if len(params) > 0 {
		ctx.Params = params
	}
	handler(ctx)
	return decodeAPIResponse(t, recorder)
}

func reloadToken(t *testing.T, db *gorm.DB, id int) model.Token {
	t.Helper()
	var token model.Token
	require.NoError(t, db.First(&token, id).Error)
	return token
}

// TestRevealPermission 锁住谁能取明文。
// 这是整组改动里唯一不可逆的泄露面：明文一旦被复制走，移除成员也收不回来。
func TestRevealPermission(t *testing.T) {
	db := setupProjectViewDB(t)
	business := seedViewToken(t, db, "biz", ownerID, common.TokenSceneBusiness, []int{memberID})
	// 下级的办公 Key：leader 看不到也取不到 —— 它花的是持有人的个人配额。
	office := seedViewToken(t, db, "office", ownerID, common.TokenSceneOffice, nil)

	for _, tc := range []struct {
		name    string
		userID  int
		tokenID int
		allowed bool
	}{
		{"负责人取自己的业务 Key", ownerID, business.Id, true},
		{"成员取明文（可查看并使用）", memberID, business.Id, true},
		{"leader 取下级业务 Key 的明文", leaderID, business.Id, true},
		{"无关的人取不到", strangerD, business.Id, false},
		{"负责人取自己的办公 Key", ownerID, office.Id, true},
		{"leader 取不到下级的办公 Key", leaderID, office.Id, false},
		{"成员身份不跨到办公 Key", memberID, office.Id, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := callAs(t, tc.userID, http.MethodPost, "/api/token/1/key", nil,
				GetTokenKey, gin.Param{Key: "id", Value: strconv.Itoa(tc.tokenID)})
			assert.Equal(t, tc.allowed, response.Success, response.Message)
		})
	}
}

// TestWritePermission 锁住谁能改配置和状态：负责人或其 leader。
func TestWritePermission(t *testing.T) {
	db := setupProjectViewDB(t)
	token := seedViewToken(t, db, "biz", ownerID, common.TokenSceneBusiness, []int{memberID})

	body := func(name string) map[string]any {
		return map[string]any{
			"id": token.Id, "name": name, "expired_time": -1,
			"unlimited_quota": true, "scene": "business", "group": "default",
		}
	}

	t.Run("改配置", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			userID  int
			allowed bool
		}{
			{"负责人可以改", ownerID, true},
			{"leader 可以改", leaderID, true},
			{"成员不能改", memberID, false},
			{"无关的人不能改", strangerD, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				response := callAs(t, tc.userID, http.MethodPut, "/api/token/",
					body("renamed-by-"+strconv.Itoa(tc.userID)), UpdateToken)
				assert.Equal(t, tc.allowed, response.Success, response.Message)
			})
		}
	})

	t.Run("改状态", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			userID  int
			allowed bool
		}{
			{"负责人可以禁用", ownerID, true},
			{"leader 可以禁用", leaderID, true},
			{"成员不能禁用", memberID, false},
			{"无关的人不能禁用", strangerD, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.NoError(t, db.Model(&model.Token{}).Where("id = ?", token.Id).
					UpdateColumn("status", common.TokenStatusEnabled).Error)
				response := callAs(t, tc.userID, http.MethodPut, "/api/token/?status_only=true",
					map[string]any{"id": token.Id, "status": common.TokenStatusDisabled}, UpdateToken)
				assert.Equal(t, tc.allowed, response.Success, response.Message)
				if tc.allowed {
					assert.Equal(t, common.TokenStatusDisabled, reloadToken(t, db, token.Id).Status)
				}
			})
		}
	})
}

// TestDeletePermission 锁住删除只有负责人能做。
func TestDeletePermission(t *testing.T) {
	db := setupProjectViewDB(t)

	for _, tc := range []struct {
		name    string
		userID  int
		allowed bool
	}{
		{"成员不能删", memberID, false},
		{"leader 不能删", leaderID, false},
		{"负责人可以删", ownerID, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := seedViewToken(t, db, "del-"+strconv.Itoa(tc.userID), ownerID,
				common.TokenSceneBusiness, []int{memberID})
			response := callAs(t, tc.userID, http.MethodDelete, "/api/token/1", nil,
				DeleteToken, gin.Param{Key: "id", Value: strconv.Itoa(token.Id)})
			assert.Equal(t, tc.allowed, response.Success, response.Message)
		})
	}
}

// TestTransferOwnership 锁住转交负责人即转移所有权。
// 这个操作不可撤销，所以只有当前负责人能做 —— leader 能改配置，但转不走下级的 Key。
func TestTransferOwnership(t *testing.T) {
	db := setupProjectViewDB(t)

	body := func(tokenID int, newOwner any) map[string]any {
		payload := map[string]any{
			"id": tokenID, "name": "biz", "expired_time": -1,
			"unlimited_quota": true, "scene": "business", "group": "default",
		}
		if newOwner != nil {
			payload["project_owner_id"] = newOwner
		}
		return payload
	}

	t.Run("leader 不能转走下级的 Key", func(t *testing.T) {
		token := seedViewToken(t, db, "t-leader", ownerID, common.TokenSceneBusiness, nil)
		response := callAs(t, leaderID, http.MethodPut, "/api/token/", body(token.Id, strangerD), UpdateToken)
		assert.False(t, response.Success, "转交只有负责人能做")
		assert.Equal(t, ownerID, reloadToken(t, db, token.Id).UserId)
	})

	t.Run("负责人原样带回自己的 id 不算转交", func(t *testing.T) {
		token := seedViewToken(t, db, "t-noop", ownerID, common.TokenSceneBusiness, nil)
		// 前端编辑弹窗会把现有负责人一起提交，这种情况不该触发任何变更。
		response := callAs(t, ownerID, http.MethodPut, "/api/token/", body(token.Id, ownerID), UpdateToken)
		require.True(t, response.Success, response.Message)
		assert.Equal(t, ownerID, reloadToken(t, db, token.Id).UserId)
	})

	t.Run("负责人转交后 user_id 真的变了", func(t *testing.T) {
		token := seedViewToken(t, db, "t-ok", ownerID, common.TokenSceneBusiness, nil)
		response := callAs(t, ownerID, http.MethodPut, "/api/token/", body(token.Id, strangerD), UpdateToken)
		require.True(t, response.Success, response.Message)
		assert.Equal(t, strangerD, reloadToken(t, db, token.Id).UserId,
			"转交即转移所有权，不只是挂一个标记")

		// 原负责人从此看不到这个 Key —— 这正是转交的语义。
		page := listTokensAs(t, ownerID, "")
		for _, item := range page.Items {
			assert.NotEqual(t, "t-ok", item.Name, "转交后原负责人不该再看到")
		}
	})

	t.Run("新负责人不留在成员列表里", func(t *testing.T) {
		token := seedViewToken(t, db, "t-member", ownerID, common.TokenSceneBusiness, []int{memberID, strangerD})
		response := callAs(t, ownerID, http.MethodPut, "/api/token/", body(token.Id, strangerD), UpdateToken)
		require.True(t, response.Success, response.Message)
		updated := reloadToken(t, db, token.Id)
		assert.Equal(t, strangerD, updated.UserId)
		assert.Equal(t, []int{memberID}, updated.GetProjectMembers(),
			"可见性已由 user_id 覆盖，成员列表里不该再冗余一份")
	})

	t.Run("办公 Key 不能转交", func(t *testing.T) {
		token := seedViewToken(t, db, "t-office", ownerID, common.TokenSceneOffice, nil)
		payload := body(token.Id, strangerD)
		payload["scene"] = "office"
		response := callAs(t, ownerID, http.MethodPut, "/api/token/", payload, UpdateToken)
		assert.False(t, response.Success, "办公 Key 消耗持有人配额，转交等于把账记到别人头上")
		assert.Equal(t, ownerID, reloadToken(t, db, token.Id).UserId)
	})

	t.Run("转交对象必须存在且启用", func(t *testing.T) {
		token := seedViewToken(t, db, "t-ghost", ownerID, common.TokenSceneBusiness, nil)
		response := callAs(t, ownerID, http.MethodPut, "/api/token/", body(token.Id, 999999), UpdateToken)
		assert.False(t, response.Success)
		assert.Equal(t, ownerID, reloadToken(t, db, token.Id).UserId)
	})
}

// TestProjectMembersOnWrite 锁住成员的写入规则。
func TestProjectMembersOnWrite(t *testing.T) {
	db := setupProjectViewDB(t)

	t.Run("办公 Key 强制清空成员", func(t *testing.T) {
		response := callAs(t, ownerID, http.MethodPost, "/api/token/", map[string]any{
			"name": "office-key", "expired_time": -1, "unlimited_quota": true,
			"scene": "office", "group": "default",
			"project_members": []int{memberID},
		}, AddToken)
		require.True(t, response.Success, response.Message)
		var token model.Token
		require.NoError(t, db.Where("name = ?", "office-key").Take(&token).Error)
		assert.Empty(t, token.ProjectMembers, "办公 Key 不该带成员")
	})

	t.Run("成员必须是在册用户", func(t *testing.T) {
		response := callAs(t, ownerID, http.MethodPost, "/api/token/", map[string]any{
			"name": "bad-member", "expired_time": -1, "unlimited_quota": true,
			"scene": "business", "group": "default",
			"project_members": []int{999999},
		}, AddToken)
		// 成员能取明文，写错 id 等于把明文授权给无关的人。
		assert.False(t, response.Success, "不存在的用户不能加进成员列表")
	})

	t.Run("负责人不进成员列表", func(t *testing.T) {
		response := callAs(t, ownerID, http.MethodPost, "/api/token/", map[string]any{
			"name": "self-member", "expired_time": -1, "unlimited_quota": true,
			"scene": "business", "group": "default",
			"project_members": []int{ownerID, memberID},
		}, AddToken)
		require.True(t, response.Success, response.Message)
		var token model.Token
		require.NoError(t, db.Where("name = ?", "self-member").Take(&token).Error)
		assert.Equal(t, []int{memberID}, token.GetProjectMembers(),
			"负责人由 user_id 覆盖，不该在成员列表里冗余一份")
	})
}
