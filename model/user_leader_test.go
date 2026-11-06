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

func TestUserLeader(t *testing.T) {
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
				sqlDB.SetMaxOpenConns(4)
				t.Cleanup(func() { _ = sqlDB.Close() })
			} else {
				dsn := strings.TrimSpace(os.Getenv(dialect.env))
				if dsn == "" {
					t.Skip(dialect.env + " is not configured")
				}
				db = openDialectAnalyticsDB(t, dialect.dbType, dsn)
			}
			useDialectAnalyticsDB(t, db, db, dialect.dbType)
			require.NoError(t, db.AutoMigrate(&UserLeader{}))

			for name, run := range map[string]func(*testing.T){
				"replace all":   testReplaceAllUserLeaders,
				"sync one":      testSyncUserLeader,
				"is leader of":  testIsLeaderOf,
				"team user ids": testGetLeaderTeamUserIds,
			} {
				for _, table := range []string{"user_leaders", "users"} {
					require.NoError(t, db.Exec("DELETE FROM "+table).Error)
				}
				t.Run(name, run)
			}
		})
	}
}

func seedLeaderUser(t *testing.T, username, hoidcId string) User {
	t.Helper()
	user := User{
		Username: username, HoidcId: hoidcId, Password: "unused-password-hash",
		AffCode: username, Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func testReplaceAllUserLeaders(t *testing.T) {
	alice := seedLeaderUser(t, "alice", "1001")
	bob := seedLeaderUser(t, "bob", "1002")
	carol := seedLeaderUser(t, "carol", "1003")

	count, err := ReplaceAllUserLeaders(map[string]string{
		"1002": "1001",
		"1003": "1001",
		"1001": "9999",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, count, "leader 解析不出来的那条边跳过，不写 leader_id=0 占位")

	var rows []UserLeader
	require.NoError(t, DB.Order("user_id").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, bob.Id, rows[0].UserId)
	assert.Equal(t, alice.Id, rows[0].LeaderId)
	assert.Equal(t, "1001", rows[0].LeaderHoidcId)
	assert.Positive(t, rows[0].SyncedAt)
	assert.Equal(t, carol.Id, rows[1].UserId)

	count, err = ReplaceAllUserLeaders(map[string]string{"1002": "1001"})
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	require.NoError(t, DB.Order("user_id").Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, bob.Id, rows[0].UserId)

	count, err = ReplaceAllUserLeaders(nil)
	require.NoError(t, err)
	assert.Zero(t, count)
	require.NoError(t, DB.Find(&rows).Error)
	assert.Len(t, rows, 1, "空快照必须保留既有数据")

	count, err = ReplaceAllUserLeaders(map[string]string{"1001": "1001"})
	require.NoError(t, err)
	assert.Zero(t, count)
}

func testSyncUserLeader(t *testing.T) {
	alice := seedLeaderUser(t, "alice", "2001")
	bob := seedLeaderUser(t, "bob", "2002")
	dave := seedLeaderUser(t, "dave", "2004")

	require.NoError(t, SyncUserLeader(bob.Id, "2001"))
	var row UserLeader
	require.NoError(t, DB.Where("user_id = ?", bob.Id).Take(&row).Error)
	assert.Equal(t, alice.Id, row.LeaderId)

	require.NoError(t, SyncUserLeader(bob.Id, "2004"))
	var count int64
	require.NoError(t, DB.Model(&UserLeader{}).Where("user_id = ?", bob.Id).Count(&count).Error)
	assert.Equal(t, int64(1), count, "一个人只能有一条汇报边")
	require.NoError(t, DB.Where("user_id = ?", bob.Id).Take(&row).Error)
	assert.Equal(t, dave.Id, row.LeaderId)

	require.NoError(t, SyncUserLeader(bob.Id, ""))
	require.NoError(t, DB.Model(&UserLeader{}).Where("user_id = ?", bob.Id).Count(&count).Error)
	assert.Zero(t, count)

	require.NoError(t, SyncUserLeader(bob.Id, "nonexistent"))
	require.NoError(t, DB.Model(&UserLeader{}).Where("user_id = ?", bob.Id).Count(&count).Error)
	assert.Zero(t, count, "解析不出 leader 时不该留下 leader_id=0 的行")

	require.NoError(t, SyncUserLeader(alice.Id, "2001"))
	require.NoError(t, DB.Model(&UserLeader{}).Where("user_id = ?", alice.Id).Count(&count).Error)
	assert.Zero(t, count)

	assert.Error(t, SyncUserLeader(0, "2001"), "非法 user id 要报错")
}

func testIsLeaderOf(t *testing.T) {
	director := seedLeaderUser(t, "director", "3001")
	manager := seedLeaderUser(t, "manager", "3002")
	lead := seedLeaderUser(t, "lead", "3003")
	worker := seedLeaderUser(t, "worker", "3004")
	outsider := seedLeaderUser(t, "outsider", "3005")

	_, err := ReplaceAllUserLeaders(map[string]string{
		"3002": "3001",
		"3003": "3002",
		"3004": "3003",
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		name           string
		leader, member int
		want           bool
	}{
		{"直属上级", lead.Id, worker.Id, true},
		{"隔一级", manager.Id, worker.Id, true},
		{"隔两级（事业部总看到组员）", director.Id, worker.Id, true},
		{"反向不成立", worker.Id, director.Id, false},
		{"无关的人", outsider.Id, worker.Id, false},
		{"自己不是自己的 leader", worker.Id, worker.Id, false},
		{"非法 id", 0, worker.Id, false},
	} {
		got, err := IsLeaderOf(tc.leader, tc.member)
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
	}

	for _, tc := range []struct {
		userId int
		want   bool
	}{
		{lead.Id, true},
		{director.Id, true},
		{worker.Id, false},
		{outsider.Id, false},
		{0, false},
	} {
		got, err := IsUserLeader(tc.userId)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, "IsUserLeader(%d)", tc.userId)
	}
}

func testGetLeaderTeamUserIds(t *testing.T) {
	director := seedLeaderUser(t, "director", "4001")
	manager := seedLeaderUser(t, "manager", "4002")
	lead := seedLeaderUser(t, "lead", "4003")
	worker := seedLeaderUser(t, "worker", "4004")
	seedLeaderUser(t, "outsider", "4005")

	_, err := ReplaceAllUserLeaders(map[string]string{
		"4002": "4001",
		"4003": "4002",
		"4004": "4003",
	})
	require.NoError(t, err)

	ids, err := GetLeaderTeamUserIds(director.Id)
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{manager.Id, lead.Id, worker.Id}, ids,
		"逐层展开到所有下级，不含自己")

	ids, err = GetLeaderTeamUserIds(lead.Id)
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{worker.Id}, ids)

	ids, err = GetLeaderTeamUserIds(worker.Id)
	require.NoError(t, err)
	assert.Empty(t, ids, "没有下级返回空")

	ids, err = GetLeaderTeamUserIds(0)
	require.NoError(t, err)
	assert.Empty(t, ids)

	require.NoError(t, DB.Delete(&User{}, lead.Id).Error)
	ids, err = GetLeaderTeamUserIds(director.Id)
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{manager.Id, worker.Id}, ids,
		"注销的中层被剔除，但他的下级仍然可见")
}
