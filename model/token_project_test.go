package model

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenProjectMembers(t *testing.T) {
	for _, dialect := range []struct {
		name   string
		dbType common.DatabaseType
		env    string
	}{
		{"sqlite", common.DatabaseTypeSQLite, ""},
		{"mysql", common.DatabaseTypeMySQL, "TEST_MYSQL_DSN"},
		{"postgres", common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"},
	} {
		t.Run(dialect.name, func(t *testing.T) {
			var db *gorm.DB
			if dialect.env == "" {
				var err error
				db, err = gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared",
					strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				t.Cleanup(func() { _ = sqlDB.Close() })
			} else {
				dsn := strings.TrimSpace(os.Getenv(dialect.env))
				if dsn == "" {
					t.Skip(dialect.env + " is not configured")
				}
				db = openDialectAnalyticsDB(t, dialect.dbType, dsn)
			}
			useDialectAnalyticsDB(t, db, db, dialect.dbType)

			for name, run := range map[string]func(*testing.T){
				"set members":      testSetProjectMembers,
				"member matching":  testProjectMemberMatching,
				"scope visibility": testTokenScopeVisibility,
				"role predicates":  testProjectRolePredicates,
			} {
				require.NoError(t, db.Exec("DELETE FROM tokens").Error)
				t.Run(name, run)
			}
		})
	}
}

func seedProjectToken(t *testing.T, name string, userId int, scene common.TokenScene, members []int) *Token {
	t.Helper()
	token := &Token{
		UserId: userId, Name: name, Key: name + "-key-secret",
		Status: common.TokenStatusEnabled, CreatedTime: 1, ExpiredTime: -1,
		Scene: scene, RoutingMode: common.TokenRoutingModeSmart,
	}
	require.NoError(t, token.SetProjectMembers(members))
	require.NoError(t, DB.Create(token).Error)
	return token
}

func testSetProjectMembers(t *testing.T) {
	token := &Token{UserId: 100}

	require.NoError(t, token.SetProjectMembers([]int{3, 1, 2, 1}))
	assert.Equal(t, ",1,2,3,", token.ProjectMembers, "去重并排序后写成前后带分隔符的规范格式")
	assert.Equal(t, []int{1, 2, 3}, token.GetProjectMembers())

	require.NoError(t, token.SetProjectMembers([]int{100, 5}))
	assert.Equal(t, ",5,", token.ProjectMembers)

	require.NoError(t, token.SetProjectMembers(nil))
	assert.Empty(t, token.ProjectMembers, "空列表写空串而不是 \",\"")
	assert.Nil(t, token.GetProjectMembers())

	assert.Error(t, token.SetProjectMembers([]int{0}), "非法 id 要报错")
	assert.Error(t, token.SetProjectMembers([]int{-1}))

	many := make([]int, 200)
	for i := range many {
		many[i] = i + 1
	}
	require.NoError(t, token.SetProjectMembers(many))
	assert.Len(t, token.GetProjectMembers(), 199)

	dirty := &Token{ProjectMembers: ",1,,abc,2,-3,"}
	assert.Equal(t, []int{1, 2}, dirty.GetProjectMembers(), "非法片段跳过，合法的照常返回")
}

func testProjectMemberMatching(t *testing.T) {
	seedProjectToken(t, "has-5", 1, common.TokenSceneBusiness, []int{5})
	seedProjectToken(t, "has-15", 1, common.TokenSceneBusiness, []int{15})
	seedProjectToken(t, "has-150", 1, common.TokenSceneBusiness, []int{150})
	seedProjectToken(t, "has-5-and-15", 1, common.TokenSceneBusiness, []int{5, 15})
	legacy := seedProjectToken(t, "legacy-null", 1, common.TokenSceneBusiness, nil)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", legacy.Id).
		UpdateColumn("project_members", nil).Error)

	for _, tc := range []struct {
		member int
		want   []string
	}{
		{5, []string{"has-5", "has-5-and-15"}},
		{15, []string{"has-15", "has-5-and-15"}},
		{150, []string{"has-150"}},
		{1, nil},
	} {
		var names []string
		require.NoError(t, DB.Model(&Token{}).
			Where("project_members LIKE ?", projectMemberLikePattern(tc.member)).
			Order("name").Pluck("name", &names).Error)
		assert.ElementsMatch(t, tc.want, names, "member %d 的包含匹配", tc.member)
	}
}

func testTokenScopeVisibility(t *testing.T) {
	const me = 7
	seedProjectToken(t, "mine-office", me, common.TokenSceneOffice, nil)
	seedProjectToken(t, "mine-business", me, common.TokenSceneBusiness, nil)
	seedProjectToken(t, "i-am-member", 8, common.TokenSceneBusiness, []int{me})
	seedProjectToken(t, "subordinate-business", 9, common.TokenSceneBusiness, nil)
	seedProjectToken(t, "subordinate-office", 9, common.TokenSceneOffice, nil)
	seedProjectToken(t, "stranger", 99, common.TokenSceneBusiness, []int{98})

	for _, tc := range []struct {
		name   string
		filter TokenFilter
		want   []string
	}{
		{
			name:   "Owned 只看 user_id 等于自己的",
			filter: TokenFilter{UserId: me, Scope: common.TokenScopeOwned},
			want:   []string{"mine-business", "mine-office"},
		},
		{
			name:   "Personal 带出自己被加为成员的，不限场景",
			filter: TokenFilter{UserId: me, Scope: common.TokenScopePersonal},
			want:   []string{"i-am-member", "mine-business", "mine-office"},
		},
		{
			name: "Business 并上下级名下的，且只含业务场景",
			filter: TokenFilter{
				UserId: me, Scope: common.TokenScopeBusiness,
				Scene: common.TokenSceneBusiness, TeamUserIds: []int{9},
			},
			want: []string{"i-am-member", "mine-business", "subordinate-business"},
		},
		{
			name: "非 leader 的 Business：TeamUserIds 为空则退化成只有自己那几支",
			filter: TokenFilter{
				UserId: me, Scope: common.TokenScopeBusiness, Scene: common.TokenSceneBusiness,
			},
			want: []string{"i-am-member", "mine-business"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokens, total, err := ListTokens(tc.filter, 0, 50)
			require.NoError(t, err)
			var names []string
			for _, token := range tokens {
				names = append(names, token.Name)
			}
			assert.ElementsMatch(t, tc.want, names)
			assert.Equal(t, int64(len(tc.want)), total, "total 要和过滤后的行数一致")
		})
	}

	for _, scope := range []common.TokenViewScope{
		common.TokenScopeOwned, common.TokenScopePersonal, common.TokenScopeBusiness,
	} {
		for _, userId := range []int{0, -1} {
			tokens, total, err := ListTokens(TokenFilter{UserId: userId, Scope: scope}, 0, 50)
			require.NoError(t, err)
			assert.Empty(t, tokens, "scope=%d UserId=%d 必须查不到", scope, userId)
			assert.Zero(t, total)
		}
	}
}

func testProjectRolePredicates(t *testing.T) {
	business := &Token{
		UserId: 1, Scene: common.TokenSceneBusiness, ProjectMembers: ",3,",
	}
	assert.True(t, business.IsProjectKey())
	assert.True(t, business.IsProjectMember(3))
	assert.False(t, business.IsProjectMember(2))
	assert.False(t, business.IsProjectMember(0), "id 非正数一律 false")

	office := &Token{
		UserId: 1, Scene: common.TokenSceneOffice, ProjectMembers: ",3,",
	}
	assert.False(t, office.IsProjectKey())
	assert.False(t, office.IsProjectMember(3), "office 场景不认 project_members")

	for _, tc := range []struct {
		token  *Token
		userId int
		want   string
	}{
		{business, 1, common.TokenRoleOwner},
		{business, 3, common.TokenRoleMember},
		{business, 99, common.TokenRoleLeader},
		{office, 1, common.TokenRoleOwner},
		{office, 3, common.TokenRoleLeader},
	} {
		assert.Equal(t, tc.want, TokenRoleFor(tc.token, tc.userId),
			"user %d on %s key", tc.userId, tc.token.Scene)
	}
}
