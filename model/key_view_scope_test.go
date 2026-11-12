package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestKeyViewScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })

	useDialectAnalyticsDB(t, db, db, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&UserLeader{}, &UserDataScope{}, &UserDataScopeOrg{}))

	for name, run := range map[string]func(*testing.T){
		"union of three sources": testKeyViewScopeUnion,
		"single member check":    testIsInKeyViewTeam,
		"blank org never widens": testKeyViewScopeBlankOrg,
	} {
		for _, table := range []string{"user_leaders", "user_data_scopes", "user_data_scope_orgs", "users"} {
			require.NoError(t, db.Exec("DELETE FROM "+table).Error)
		}
		t.Run(name, run)
	}
}

func seedScopeUser(t *testing.T, username, orgStr string, isCharge int) User {
	t.Helper()
	user := User{
		Username: username, DisplayName: username, OrgStr: orgStr, IsCharge: isCharge,
		HoidcId: username + "-hoidc", Password: "unused-password-hash", AffCode: username,
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func testKeyViewScopeUnion(t *testing.T) {
	viewer := seedScopeUser(t, "viewer", "BU/Dept", 0)
	// 来源 1：汇报链（含隔级）
	reportee := seedScopeUser(t, "reportee", "BU/Other", 0)
	grandchild := seedScopeUser(t, "grandchild", "BU/Other", 0)
	// 来源 2a：点对点授权
	picked := seedScopeUser(t, "picked", "Elsewhere/Team", 0)
	// 来源 2b：组织路径授权
	inPath := seedScopeUser(t, "in-path", "Granted/Sub/Team", 0)
	outPath := seedScopeUser(t, "out-path", "Granted2/Team", 0)
	stranger := seedScopeUser(t, "stranger", "Nowhere", 0)

	require.NoError(t, DB.Create(&UserLeader{UserId: reportee.Id, LeaderId: viewer.Id, SyncedAt: 1}).Error)
	require.NoError(t, DB.Create(&UserLeader{UserId: grandchild.Id, LeaderId: reportee.Id, SyncedAt: 1}).Error)
	require.NoError(t, DB.Create(&UserDataScope{ManagerId: viewer.Id, MemberId: picked.Id, CreatedAt: 1}).Error)
	require.NoError(t, DB.Create(&UserDataScopeOrg{ManagerId: viewer.Id, OrgPath: "Granted", CreatedAt: 1}).Error)

	ids, err := GetKeyViewTeamUserIds(viewer.Id)
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{reportee.Id, grandchild.Id, picked.Id, inPath.Id}, ids,
		"三个来源都要并进来，隔级下属也算")
	assert.NotContains(t, ids, outPath.Id, "Granted2 不是 Granted 的子路径")
	assert.NotContains(t, ids, stranger.Id)
	assert.NotContains(t, ids, viewer.Id, "不含自己")

	has, err := HasKeyViewTeam(viewer.Id)
	require.NoError(t, err)
	assert.True(t, has)

	has, err = HasKeyViewTeam(stranger.Id)
	require.NoError(t, err)
	assert.False(t, has, "没有任何来源的人不带团队")
}

func testIsInKeyViewTeam(t *testing.T) {
	viewer := seedScopeUser(t, "viewer", "BU/Dept", 0)
	reportee := seedScopeUser(t, "reportee", "Other", 0)
	picked := seedScopeUser(t, "picked", "Other", 0)
	inPath := seedScopeUser(t, "in-path", "Granted/Team", 0)
	stranger := seedScopeUser(t, "stranger", "Nowhere", 0)
	// 部门负责人自动管自己的部门
	head := seedScopeUser(t, "head", "BU/Dept", 1)
	peer := seedScopeUser(t, "peer", "BU/Dept/Sub", 0)

	require.NoError(t, DB.Create(&UserLeader{UserId: reportee.Id, LeaderId: viewer.Id, SyncedAt: 1}).Error)
	require.NoError(t, DB.Create(&UserDataScope{ManagerId: viewer.Id, MemberId: picked.Id, CreatedAt: 1}).Error)
	require.NoError(t, DB.Create(&UserDataScopeOrg{ManagerId: viewer.Id, OrgPath: "Granted", CreatedAt: 1}).Error)

	for _, tc := range []struct {
		name           string
		viewer, member int
		want           bool
	}{
		{"汇报链", viewer.Id, reportee.Id, true},
		{"点对点授权", viewer.Id, picked.Id, true},
		{"组织路径授权", viewer.Id, inPath.Id, true},
		{"无关的人", viewer.Id, stranger.Id, false},
		{"自己不算", viewer.Id, viewer.Id, false},
		{"反向不成立", reportee.Id, viewer.Id, false},
		{"部门负责人管自己部门子树", head.Id, peer.Id, true},
		{"非负责人不因同部门获得权限", viewer.Id, peer.Id, false},
		{"非法 id", 0, reportee.Id, false},
	} {
		got, err := IsInKeyViewTeam(tc.viewer, tc.member)
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
	}

	ids, err := GetKeyViewTeamUserIds(viewer.Id)
	require.NoError(t, err)
	for _, id := range ids {
		got, err := IsInKeyViewTeam(viewer.Id, id)
		require.NoError(t, err)
		assert.True(t, got, "全量展开里的 %d 必须通过定向判定", id)
	}
}

func testKeyViewScopeBlankOrg(t *testing.T) {
	blank := seedScopeUser(t, "blank", "", 1) // 负责人标记有，组织路径没有
	other := seedScopeUser(t, "other", "BU/Dept", 0)
	another := seedScopeUser(t, "another", "", 0)

	ids, err := GetKeyViewTeamUserIds(blank.Id)
	require.NoError(t, err)
	assert.Empty(t, ids, "组织路径为空的负责人不该看到任何人")

	got, err := IsInKeyViewTeam(blank.Id, other.Id)
	require.NoError(t, err)
	assert.False(t, got)

	got, err = IsInKeyViewTeam(blank.Id, another.Id)
	require.NoError(t, err)
	assert.False(t, got, "两边组织路径都空也不能互相可见")
}
