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

func TestMemberCandidates(t *testing.T) {
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
				"search":        testSearchMemberCandidates,
				"like escaping": testSearchMemberCandidatesEscaping,
				"briefs":        testGetUserBriefsByIds,
				"missing ids":   testFindMissingUserIds,
			} {
				require.NoError(t, db.Exec("DELETE FROM users").Error)
				t.Run(name, run)
			}
		})
	}
}

func seedCandidate(t *testing.T, username, displayName string, status int) User {
	t.Helper()
	user := User{
		Username: username, DisplayName: displayName, OrgStr: "BU/Dept/Team",
		Avatar:   "https://example.test/" + username + ".png",
		Password: "unused-password-hash", AffCode: username,
		Role: common.RoleCommonUser, Status: status, Group: "default",
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func testSearchMemberCandidates(t *testing.T) {
	seedCandidate(t, "zhang_wei", "张伟", common.UserStatusEnabled)
	seedCandidate(t, "zhang_min", "张敏", common.UserStatusEnabled)
	seedCandidate(t, "li_na", "李娜", common.UserStatusEnabled)
	seedCandidate(t, "zhang_off", "张离职", common.UserStatusDisabled)

	got, err := SearchMemberCandidates("")
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = SearchMemberCandidates("   ")
	require.NoError(t, err)
	assert.Empty(t, got, "全空白等同于空关键词")

	got, err = SearchMemberCandidates("张")
	require.NoError(t, err)
	var names []string
	for _, candidate := range got {
		names = append(names, candidate.Username)
	}
	assert.ElementsMatch(t, []string{"zhang_wei", "zhang_min"}, names,
		"命中显示名，且排除已禁用的账号")

	got, err = SearchMemberCandidates("li_na")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "李娜", got[0].DisplayName)
	assert.Equal(t, "BU/Dept/Team", got[0].OrgStr)
	assert.Contains(t, got[0].Avatar, "li_na.png", "头像要带出来，前端下拉用它")

	got, err = SearchMemberCandidates("nobody-matches-this")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func testSearchMemberCandidatesEscaping(t *testing.T) {
	seedCandidate(t, "alpha", "Alpha", common.UserStatusEnabled)
	seedCandidate(t, "beta", "Beta", common.UserStatusEnabled)
	seedCandidate(t, "pct_user", "100%真实", common.UserStatusEnabled)
	seedCandidate(t, "under_score", "Under_score", common.UserStatusEnabled)

	got, err := SearchMemberCandidates("%")
	require.NoError(t, err)
	require.Len(t, got, 1, "% 被转义成字面量，不该匹配所有人")
	assert.Equal(t, "pct_user", got[0].Username)

	got, err = SearchMemberCandidates("100%")
	require.NoError(t, err)
	require.Len(t, got, 1, "% 作为字面字符参与匹配")
	assert.Equal(t, "pct_user", got[0].Username)

	got, err = SearchMemberCandidates("_")
	require.NoError(t, err)
	var names []string
	for _, candidate := range got {
		names = append(names, candidate.Username)
	}
	assert.ElementsMatch(t, []string{"pct_user", "under_score"}, names,
		"_ 被转义成字面量")
}

func testGetUserBriefsByIds(t *testing.T) {
	alice := seedCandidate(t, "alice", "Alice", common.UserStatusEnabled)
	bob := seedCandidate(t, "bob", "Bob", common.UserStatusEnabled)
	gone := seedCandidate(t, "gone", "Gone", common.UserStatusEnabled)
	require.NoError(t, DB.Delete(&User{}, gone.Id).Error)

	briefs, err := GetUserBriefsByIds(nil)
	require.NoError(t, err)
	assert.Empty(t, briefs)

	briefs, err = GetUserBriefsByIds([]int{alice.Id, bob.Id, gone.Id, 999999})
	require.NoError(t, err)
	assert.Len(t, briefs, 2, "已注销和不存在的 id 在结果里缺席")
	assert.Equal(t, "Alice", briefs[alice.Id].DisplayName)
	assert.Equal(t, "bob", briefs[bob.Id].Username)
	_, ok := briefs[gone.Id]
	assert.False(t, ok, "软删除的用户查不到")
}

func testFindMissingUserIds(t *testing.T) {
	alice := seedCandidate(t, "alice", "Alice", common.UserStatusEnabled)
	gone := seedCandidate(t, "gone", "Gone", common.UserStatusEnabled)
	require.NoError(t, DB.Delete(&User{}, gone.Id).Error)

	missing, err := FindMissingUserIds(nil)
	require.NoError(t, err)
	assert.Empty(t, missing)

	missing, err = FindMissingUserIds([]int{alice.Id})
	require.NoError(t, err)
	assert.Empty(t, missing)

	missing, err = FindMissingUserIds([]int{alice.Id, gone.Id, 999999})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{gone.Id, 999999}, missing)
}
