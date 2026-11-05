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

// TEST_MYSQL_DSN / TEST_POSTGRES_DSN must point to disposable databases.
func TestUserDataScope(t *testing.T) {
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
				// The replace transaction needs a connection of its own.
				var err error
				db, err = gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
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
			require.NoError(t, db.AutoMigrate(&UserDataScope{}))
			for name, run := range map[string]func(*testing.T){
				"replace":                testUserDataScopeReplace,
				"deleted users hidden":   testUserDataScopeHidesDeletedUsers,
				"list, count and delete": testUserDataScopeListAndDelete,
				"accounts by email":      testUserAccountsByEmail,
			} {
				for _, table := range []string{"user_data_scopes", "users"} {
					require.NoError(t, db.Exec("DELETE FROM "+table).Error)
				}
				t.Run(name, run)
			}
		})
	}
}

func seedUserDataScopeUser(t *testing.T, username, displayName, orgStr string) User {
	t.Helper()
	user := User{Username: username, DisplayName: displayName, OrgStr: orgStr, Password: "unused-password-hash", AffCode: username, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func testUserDataScopeReplace(t *testing.T) {
	manager := seedUserDataScopeUser(t, "scope-manager", "Manager", "Org/Team")
	a := seedUserDataScopeUser(t, "scope-a", "A", "Org/Team")
	b := seedUserDataScopeUser(t, "scope-b", "B", "Org/Team")
	c := seedUserDataScopeUser(t, "scope-c", "C", "Org")
	require.NoError(t, DB.Model(&c).Update("status", common.UserStatusDisabled).Error)

	stored, err := ReplaceUserDataScope(manager.Id, []int{b.Id, a.Id, b.Id, manager.Id}, 99)
	require.NoError(t, err)
	assert.Equal(t, []int{b.Id, a.Id}, stored)
	memberIds, err := GetUserDataScopeMemberIds(manager.Id)
	require.NoError(t, err)
	assert.Equal(t, []int{b.Id, a.Id}, memberIds)

	// The second save replaces the whole list; disabled people can be kept.
	stored, err = ReplaceUserDataScope(manager.Id, []int{c.Id, a.Id}, 100)
	require.NoError(t, err)
	assert.Equal(t, []int{c.Id, a.Id}, stored)
	var rows []UserDataScope
	require.NoError(t, DB.Where("manager_id = ?", manager.Id).Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, []int{c.Id, a.Id}, []int{rows[0].MemberId, rows[1].MemberId})
	assert.Equal(t, rows[0].CreatedAt, rows[1].CreatedAt)
	assert.Positive(t, rows[0].CreatedAt)
	assert.Equal(t, []int{100, 100}, []int{rows[0].UpdatedBy, rows[1].UpdatedBy})

	for name, tc := range map[string]struct {
		managerId int
		memberIds []int
		message   string
	}{
		"only the manager":   {manager.Id, []int{manager.Id}, "Select at least one person the employee can manage"},
		"empty list":         {manager.Id, nil, "Select at least one person the employee can manage"},
		"invalid id":         {manager.Id, []int{a.Id, 0}, "Invalid member ID"},
		"unknown member":     {manager.Id, []int{a.Id, c.Id + 1000}, "Some selected people do not exist or have been deleted"},
		"unknown manager":    {c.Id + 1000, []int{a.Id}, "Employee does not exist or has been deleted"},
		"too many requested": {manager.Id, make([]int, MaxUserDataScopeMembers+1), "An employee can manage at most 5000 people"},
	} {
		_, err := ReplaceUserDataScope(tc.managerId, tc.memberIds, 1)
		require.Error(t, err, name)
		assert.Equal(t, tc.message, err.Error(), name)
	}
	memberIds, err = GetUserDataScopeMemberIds(manager.Id)
	require.NoError(t, err)
	assert.Equal(t, []int{c.Id, a.Id}, memberIds, "a rejected save keeps the stored list")
}

func testUserDataScopeHidesDeletedUsers(t *testing.T) {
	manager := seedUserDataScopeUser(t, "hidden-manager", "Manager", "")
	a := seedUserDataScopeUser(t, "hidden-a", "A", "")
	b := seedUserDataScopeUser(t, "hidden-b", "B", "")
	_, err := ReplaceUserDataScope(manager.Id, []int{a.Id, b.Id}, 1)
	require.NoError(t, err)
	require.NoError(t, DB.Delete(&b).Error)

	memberIds, err := GetUserDataScopeMemberIds(manager.Id)
	require.NoError(t, err)
	assert.Equal(t, []int{a.Id}, memberIds)
	managers, total, err := ListUserDataScopeManagers("", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, managers, 1)
	assert.Equal(t, 1, managers[0].MemberCount)
	require.NoError(t, DB.Model(&a).Update("avatar", "https://example.com/a.png").Error)
	directory, err := GetUserDirectory()
	require.NoError(t, err)
	assert.Equal(t, []int{manager.Id, a.Id}, []int{directory[0].Id, directory[1].Id})
	assert.Equal(t, 1, directory[0].ManagedCount)
	assert.Equal(t, "https://example.com/a.png", directory[1].Avatar)

	_, err = ReplaceUserDataScope(manager.Id, []int{a.Id, b.Id}, 1)
	require.Error(t, err)
	assert.Equal(t, "Some selected people do not exist or have been deleted", err.Error())

	// A deleted manager disappears from every read.
	require.NoError(t, DB.Delete(&manager).Error)
	managers, total, err = ListUserDataScopeManagers("", 0, 10)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, managers)
	_, err = ReplaceUserDataScope(manager.Id, []int{a.Id}, 1)
	require.Error(t, err)
	assert.Equal(t, "Employee does not exist or has been deleted", err.Error())
}

func testUserDataScopeListAndDelete(t *testing.T) {
	alice := seedUserDataScopeUser(t, "alice-lead", "Alice Lin", "Org/Platform")
	bob := seedUserDataScopeUser(t, "bob-lead", "Bob Zhou", "Org/Ops")
	m1 := seedUserDataScopeUser(t, "member-1", "Member One", "Org/Platform")
	m2 := seedUserDataScopeUser(t, "member-2", "Member Two", "Org/Ops")
	m3 := seedUserDataScopeUser(t, "member-3", "Member Three", "Org/Ops")
	_, err := ReplaceUserDataScope(alice.Id, []int{m1.Id}, 1)
	require.NoError(t, err)
	_, err = ReplaceUserDataScope(bob.Id, []int{m3.Id, m2.Id, alice.Id}, 1)
	require.NoError(t, err)
	// Bob's list is the most recently saved one.
	require.NoError(t, DB.Model(&UserDataScope{}).Where("manager_id = ?", alice.Id).Update("created_at", 1000).Error)
	require.NoError(t, DB.Model(&UserDataScope{}).Where("manager_id = ?", bob.Id).Update("created_at", 2000).Error)

	managers, total, err := ListUserDataScopeManagers("", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, managers, 2)
	assert.Equal(t, UserDataScopeManager{
		Id: bob.Id, Username: "bob-lead", DisplayName: "Bob Zhou", OrgStr: "Org/Ops", Status: common.UserStatusEnabled,
		MemberCount: 3, CreatedAt: 2000,
		Members: []UserDataScopeMember{
			{Id: m3.Id, Username: "member-3", DisplayName: "Member Three", OrgStr: "Org/Ops", Status: common.UserStatusEnabled},
			{Id: m2.Id, Username: "member-2", DisplayName: "Member Two", OrgStr: "Org/Ops", Status: common.UserStatusEnabled},
			{Id: alice.Id, Username: "alice-lead", DisplayName: "Alice Lin", OrgStr: "Org/Platform", Status: common.UserStatusEnabled},
		},
	}, managers[0])
	assert.Equal(t, alice.Id, managers[1].Id)
	assert.Equal(t, 1, managers[1].MemberCount)

	page, total, err := ListUserDataScopeManagers("", 1, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, page, 1)
	assert.Equal(t, alice.Id, page[0].Id)

	for keyword, want := range map[string][]int{"Lin": {alice.Id}, "bob": {bob.Id}, "lead": {bob.Id, alice.Id}, "member": nil} {
		found, total, err := ListUserDataScopeManagers(keyword, 0, 10)
		require.NoError(t, err)
		ids := make([]int, 0, len(found))
		for _, manager := range found {
			ids = append(ids, manager.Id)
		}
		assert.EqualValues(t, len(want), total, keyword)
		assert.ElementsMatch(t, want, ids, keyword)
	}

	directory, err := GetUserDirectory()
	require.NoError(t, err)
	managed := map[int]int{}
	for _, entry := range directory {
		managed[entry.Id] = entry.ManagedCount
	}
	assert.Equal(t, map[int]int{alice.Id: 1, bob.Id: 3, m1.Id: 0, m2.Id: 0, m3.Id: 0}, managed)

	require.NoError(t, DeleteUserDataScope(bob.Id))
	require.NoError(t, DeleteUserDataScope(bob.Id))
	memberIds, err := GetUserDataScopeMemberIds(bob.Id)
	require.NoError(t, err)
	assert.Equal(t, []int{}, memberIds)
	managers, total, err = ListUserDataScopeManagers("", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, managers, 1)
	assert.Equal(t, alice.Id, managers[0].Id)
}

func testUserAccountsByEmail(t *testing.T) {
	older := seedUserDataScopeUser(t, "email-older", "Ana", "")
	newer := seedUserDataScopeUser(t, "email-newer", "Ana 2", "")
	disabled := seedUserDataScopeUser(t, "email-disabled", "Carl", "")
	deleted := seedUserDataScopeUser(t, "email-deleted", "Bob", "")
	for user, email := range map[*User]string{&older: "Ana@Example.com", &newer: "ana@example.com", &disabled: "carl@example.com", &deleted: "bob@example.com"} {
		require.NoError(t, DB.Model(user).Update("email", email).Error)
	}
	require.NoError(t, DB.Model(&disabled).Update("status", common.UserStatusDisabled).Error)
	require.NoError(t, DB.Delete(&deleted).Error)

	accounts, err := GetUserAccountsByEmail([]string{" ANA@example.com", "bob@example.com", "carl@example.com", "nobody@example.com"})
	require.NoError(t, err)
	assert.Equal(t, map[string]UserAccountRef{
		"ana@example.com":  {Id: older.Id, Status: common.UserStatusEnabled},
		"carl@example.com": {Id: disabled.Id, Status: common.UserStatusDisabled},
	}, accounts)

	accounts, err = GetUserAccountsByEmail(nil)
	require.NoError(t, err)
	assert.Empty(t, accounts)
}
