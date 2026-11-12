package controller

import (
	"net/http"
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 这组测试里的固定角色。负责人恒等于 user_id，没有独立的「管理员」身份。
const (
	ownerID   = 11 // 业务 Key 的负责人
	memberID  = 13 // 被加为项目成员
	leaderID  = 14 // ownerID 的上级
	strangerD = 15 // 和这些 Key 无关的人
)

// tokenProjectResponseItem 比 tokenResponseItem 多了人员字段，
// 用来断言业务视图的回填结果。
type tokenProjectResponseItem struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	UserID int    `json:"user_id"`
	Scene  string `json:"scene"`
	MyRole string `json:"my_role"`
	Owner  *struct {
		ID          int    `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"owner"`
	ProjectMembers []struct {
		ID int `json:"id"`
	} `json:"project_members"`
}

type tokenProjectPageResponse struct {
	Total int                        `json:"total"`
	Items []tokenProjectResponseItem `json:"items"`
}

// setupProjectViewDB 在 token fixture 之外补上 users 和 user_leaders ——
// 业务视图要查用户名、要展开汇报链。
func setupProjectViewDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	// 业务视图的可见范围要查这四张表：用户、汇报链，以及
	// users/permissions 配的两种授权。
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.UserLeader{},
		&model.UserDataScope{}, &model.UserDataScopeOrg{},
	))

	for id, name := range map[int]string{
		ownerID: "owner", memberID: "member", leaderID: "leader", strangerD: "stranger",
	} {
		require.NoError(t, db.Create(&model.User{
			Id: id, Username: name, DisplayName: name + "-display",
			HoidcId: name + "-hoidc", Role: common.RoleCommonUser,
			Status: common.UserStatusEnabled, Group: "default", AffCode: name,
		}).Error)
	}
	// owner 向 leader 汇报。
	require.NoError(t, db.Create(&model.UserLeader{
		UserId: ownerID, LeaderId: leaderID, LeaderHoidcId: "leader-hoidc", SyncedAt: 1,
	}).Error)
	return db
}

func seedViewToken(t *testing.T, db *gorm.DB, name string, userID int, scene common.TokenScene, members []int) *model.Token {
	t.Helper()
	token := &model.Token{
		UserId: userID, Name: name, Key: name + "-key-secret-value",
		Status: common.TokenStatusEnabled, CreatedTime: 1, ExpiredTime: -1,
		Scene: scene, RoutingMode: common.TokenRoutingModeSmart,
	}
	require.NoError(t, token.SetProjectMembers(members))
	require.NoError(t, db.Create(token).Error)
	return token
}

func listTokensAs(t *testing.T, userID int, query string) tokenProjectPageResponse {
	t.Helper()
	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/?p=1&size=50"+query, nil, userID)
	GetAllTokens(ctx)
	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var page tokenProjectPageResponse
	require.NoError(t, common.Unmarshal(response.Data, &page))
	return page
}

// TestTokenViewScopes 锁住两个视图各自带出哪些 Key。
func TestTokenViewScopes(t *testing.T) {
	db := setupProjectViewDB(t)

	seedViewToken(t, db, "owner-office", ownerID, common.TokenSceneOffice, nil)
	seedViewToken(t, db, "owner-business", ownerID, common.TokenSceneBusiness, []int{memberID})
	seedViewToken(t, db, "member-own-office", memberID, common.TokenSceneOffice, nil)
	seedViewToken(t, db, "stranger-business", strangerD, common.TokenSceneBusiness, nil)

	for _, tc := range []struct {
		name   string
		userID int
		query  string
		want   []string
	}{
		{
			// 非 leader 看不到业务视图的 tab，参与的 Key 必须在个人视图出现，
			// 否则「成员可查看并使用该 Key」没有入口可用。
			name:   "成员的个人视图带出被加进去的业务 Key",
			userID: memberID, query: "",
			want: []string{"member-own-office", "owner-business"},
		},
		{
			name:   "负责人的个人视图是自己名下全部，不分场景",
			userID: ownerID, query: "",
			want: []string{"owner-office", "owner-business"},
		},
		{
			name:   "无关的人看不到任何别人的 Key",
			userID: strangerD, query: "",
			want: []string{"stranger-business"},
		},
		{
			// leader 的业务视图并上下级名下的，但只含业务场景 ——
			// 下级的个人办公 Key 不在其中。
			name:   "leader 的业务视图含下级的业务 Key、不含其办公 Key",
			userID: leaderID, query: "&view=business",
			want: []string{"owner-business"},
		},
		{
			name:   "非 leader 的业务视图只有自己参与的",
			userID: memberID, query: "&view=business",
			want: []string{"owner-business"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := listTokensAs(t, tc.userID, tc.query)
			var names []string
			for _, item := range page.Items {
				names = append(names, item.Name)
			}
			assert.ElementsMatch(t, tc.want, names)
			assert.Equal(t, len(tc.want), page.Total, "total 要和过滤后的行数一致")
		})
	}
}

// TestBusinessViewHonorsGrantedScope 锁住 users/permissions 配的授权在业务视图里生效。
// 只认 SSO 汇报链的话，管理员配了「能管某个部门」的人在密钥页看不到那些 Key，
// 而在用量分析里看得到 —— 同一个「我管哪些人」在两个页面给出不同答案。
func TestBusinessViewHonorsGrantedScope(t *testing.T) {
	db := setupProjectViewDB(t)
	// strangerD 和 ownerID 之间没有任何汇报关系。
	seedViewToken(t, db, "owner-business", ownerID, common.TokenSceneBusiness, nil)
	seedViewToken(t, db, "owner-office", ownerID, common.TokenSceneOffice, nil)

	// 授权前：看不到。
	page := listTokensAs(t, strangerD, "&view=business")
	assert.Empty(t, page.Items, "没有授权时看不到别人的 Key")

	// 管理员在 users/permissions 里把 ownerID 指派给 strangerD。
	require.NoError(t, db.Create(&model.UserDataScope{
		ManagerId: strangerD, MemberId: ownerID, CreatedAt: 1,
	}).Error)

	page = listTokensAs(t, strangerD, "&view=business")
	require.Len(t, page.Items, 1, "授权后业务 Key 可见")
	assert.Equal(t, "owner-business", page.Items[0].Name)
	assert.Equal(t, common.TokenRoleLeader, page.Items[0].MyRole)

	// 可见范围和可操作范围必须一致：能看到就该能启停、能取明文。
	response := callAs(t, strangerD, http.MethodPut, "/api/token/?status_only=true",
		map[string]any{"id": page.Items[0].ID, "status": common.TokenStatusDisabled}, UpdateToken)
	assert.True(t, response.Success, "列表里看得见的行必须能操作: %s", response.Message)

	// 但办公 Key 仍然不可见 —— 它花的是持有人的个人配额。
	for _, item := range page.Items {
		assert.NotEqual(t, "owner-office", item.Name, "授权不把办公 Key 带进业务视图")
	}
}

// TestTokenViewPinsBusinessScene 锁住业务视图忽略客户端传的 scene。
// 这里刻意不报错：如果 leader 和非 leader 对同一个请求一个成功一个报错，
// 这个差异本身就泄露了谁是 leader。
func TestTokenViewPinsBusinessScene(t *testing.T) {
	db := setupProjectViewDB(t)
	seedViewToken(t, db, "my-office", memberID, common.TokenSceneOffice, nil)
	seedViewToken(t, db, "joined-business", ownerID, common.TokenSceneBusiness, []int{memberID})

	page := listTokensAs(t, memberID, "&view=business&scene=office")
	require.Len(t, page.Items, 1, "scene=office 被静默钉回 business，不报错")
	assert.Equal(t, "joined-business", page.Items[0].Name)
}

// TestTokenViewRejectsUnknownValue 锁住非法 view 值报错而不是静默返回全量。
func TestTokenViewRejectsUnknownValue(t *testing.T) {
	setupProjectViewDB(t)
	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/?p=1&size=10&view=everything", nil, ownerID)
	GetAllTokens(ctx)
	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	assert.Empty(t, response.Data)
}

// TestTokenMyRoleAndPeople 锁住 my_role 与人员回填。
// my_role 决定前端显示哪些行操作，回填错了会把注定被拒的入口显示出来。
func TestTokenMyRoleAndPeople(t *testing.T) {
	db := setupProjectViewDB(t)
	seedViewToken(t, db, "shared", ownerID, common.TokenSceneBusiness, []int{memberID})

	for _, tc := range []struct {
		userID int
		want   string
	}{
		{ownerID, common.TokenRoleOwner},
		{memberID, common.TokenRoleMember},
		{leaderID, common.TokenRoleLeader},
	} {
		page := listTokensAs(t, tc.userID, "&view=business")
		require.Len(t, page.Items, 1, "user %d", tc.userID)
		item := page.Items[0]
		assert.Equal(t, tc.want, item.MyRole, "user %d 的身份", tc.userID)

		require.NotNil(t, item.Owner, "负责人要回填")
		assert.Equal(t, ownerID, item.Owner.ID)
		assert.Equal(t, "owner-display", item.Owner.DisplayName)
		require.Len(t, item.ProjectMembers, 1)
		assert.Equal(t, memberID, item.ProjectMembers[0].ID)
	}
}

// TestPersonalViewFillsMyRole 锁住个人视图也回填 my_role。
// 个人视图现在会带出「别人名下、我是成员」的行，不回填的话前端按 owner 渲染，
// 会显示编辑和删除入口。
func TestPersonalViewFillsMyRole(t *testing.T) {
	db := setupProjectViewDB(t)
	seedViewToken(t, db, "mine", memberID, common.TokenSceneOffice, nil)
	seedViewToken(t, db, "joined", ownerID, common.TokenSceneBusiness, []int{memberID})

	page := listTokensAs(t, memberID, "")
	roles := make(map[string]string, len(page.Items))
	for _, item := range page.Items {
		roles[item.Name] = item.MyRole
	}
	assert.Equal(t, common.TokenRoleOwner, roles["mine"])
	assert.Equal(t, common.TokenRoleMember, roles["joined"], "参与的那一行必须标成 member")
}

// TestAdminListUserTokensStaysOwned 锁住管理员接口只看目标用户名下的 Key，
// 不把该用户作为成员参与的、别人名下的 Key 算进来。
func TestAdminListUserTokensStaysOwned(t *testing.T) {
	db := setupProjectViewDB(t)
	seedViewToken(t, db, "target-own", memberID, common.TokenSceneOffice, nil)
	seedViewToken(t, db, "target-joined", ownerID, common.TokenSceneBusiness, []int{memberID})

	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/user/13/tokens?p=1&size=50", nil, 999)
	ctx.Set("role", common.RoleRootUser)
	ctx.Params = gin.Params{{Key: "id", Value: "13"}}
	ListUserTokens(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var page tokenProjectPageResponse
	require.NoError(t, common.Unmarshal(response.Data, &page))

	var names []string
	for _, item := range page.Items {
		names = append(names, item.Name)
	}
	slices.Sort(names)
	assert.Equal(t, []string{"target-own"}, names,
		"管理员要的是归属关系，不是参与关系")
}
