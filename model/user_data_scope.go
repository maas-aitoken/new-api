package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const MaxUserDataScopeMembers = 5000

// UserDataScope rows are a snapshot of member ids, replaced as a whole per manager.
type UserDataScope struct {
	Id        int   `json:"id"`
	ManagerId int   `json:"manager_id" gorm:"not null;uniqueIndex:idx_user_data_scope_pair,priority:1"`
	MemberId  int   `json:"member_id" gorm:"not null;uniqueIndex:idx_user_data_scope_pair,priority:2"`
	CreatedAt int64 `json:"created_at" gorm:"type:bigint;not null"`
	UpdatedBy int   `json:"updated_by" gorm:"not null"`
}

type UserDirectoryEntry struct {
	Id           int    `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	Avatar       string `json:"avatar"`
	OrgStr       string `json:"org_str"`
	Status       int    `json:"status"`
	ManagedCount int    `json:"managed_count"`
}

type UserDataScopeMember struct {
	Id          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	OrgStr      string `json:"org_str"`
	Status      int    `json:"status"`
}

type UserDataScopeManager struct {
	Id          int                   `json:"id"`
	Username    string                `json:"username"`
	DisplayName string                `json:"display_name"`
	OrgStr      string                `json:"org_str"`
	Status      int                   `json:"status"`
	MemberCount int                   `json:"member_count"`
	Members     []UserDataScopeMember `json:"members"`
	CreatedAt   int64                 `json:"created_at"`
}

type userDataScopeMemberRow struct {
	ManagerId int
	UserDataScopeMember
}

type userDataScopeManagerCount struct {
	ManagerId int
	Count     int
}

func GetUserDirectory() ([]UserDirectoryEntry, error) {
	entries := make([]UserDirectoryEntry, 0)
	if err := DB.Model(&User{}).Select("id", "username", "display_name", "avatar", "org_str", "status").Order("id").Find(&entries).Error; err != nil {
		return nil, err
	}
	var counts []userDataScopeManagerCount
	err := DB.Table("user_data_scopes").
		Select("user_data_scopes.manager_id AS manager_id, COUNT(*) AS count").
		Joins("JOIN users ON users.id = user_data_scopes.member_id AND users.deleted_at IS NULL").
		Group("user_data_scopes.manager_id").
		Scan(&counts).Error
	if err != nil {
		return nil, err
	}
	managedCounts := make(map[int]int, len(counts))
	for _, count := range counts {
		managedCounts[count.ManagerId] = count.Count
	}
	for i := range entries {
		entries[i].ManagedCount = managedCounts[entries[i].Id]
	}
	return entries, nil
}

type UserAccountRef struct {
	Id     int
	Status int
}

// GetUserAccountsByEmail keys by lowercased email and keeps the oldest account.
// Matching in Go keeps case-insensitivity the same on every database.
func GetUserAccountsByEmail(emails []string) (map[string]UserAccountRef, error) {
	accounts := make(map[string]UserAccountRef, len(emails))
	if len(emails) == 0 {
		return accounts, nil
	}
	var rows []struct {
		Id     int
		Email  string
		Status int
	}
	if err := DB.Model(&User{}).Select("id", "email", "status").Where("email <> ''").Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	wanted := make(map[string]struct{}, len(emails))
	for _, email := range emails {
		wanted[strings.ToLower(strings.TrimSpace(email))] = struct{}{}
	}
	for _, row := range rows {
		email := strings.ToLower(strings.TrimSpace(row.Email))
		if _, ok := wanted[email]; !ok {
			continue
		}
		if _, taken := accounts[email]; !taken {
			accounts[email] = UserAccountRef{Id: row.Id, Status: row.Status}
		}
	}
	return accounts, nil
}

func ListUserDataScopeManagers(keyword string, startIdx int, num int) ([]UserDataScopeManager, int64, error) {
	query := DB.Table("user_data_scopes").
		Joins("JOIN users ON users.id = user_data_scopes.manager_id AND users.deleted_at IS NULL")
	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("(users.username LIKE ? OR users.display_name LIKE ?)", like, like)
	}
	query = query.Session(&gorm.Session{})

	var total int64
	if err := query.Select("COUNT(DISTINCT user_data_scopes.manager_id)").Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	managers := make([]UserDataScopeManager, 0)
	if total == 0 {
		return managers, 0, nil
	}
	var pageRows []struct {
		ManagerId int
		CreatedAt int64
	}
	err := query.
		Select("user_data_scopes.manager_id AS manager_id, MAX(user_data_scopes.created_at) AS created_at").
		Group("user_data_scopes.manager_id").
		Order("MAX(user_data_scopes.created_at) DESC").
		Order("user_data_scopes.manager_id DESC").
		Limit(num).Offset(startIdx).
		Scan(&pageRows).Error
	if err != nil {
		return nil, 0, err
	}
	if len(pageRows) == 0 {
		return managers, total, nil
	}
	managerIds := make([]int, len(pageRows))
	for i, row := range pageRows {
		managerIds[i] = row.ManagerId
	}

	var managerUsers []UserDataScopeMember
	if err := DB.Model(&User{}).Select("id", "username", "display_name", "org_str", "status").Where("id IN ?", managerIds).Find(&managerUsers).Error; err != nil {
		return nil, 0, err
	}
	var memberRows []userDataScopeMemberRow
	err = DB.Table("user_data_scopes").
		Select("user_data_scopes.manager_id AS manager_id, users.id AS id, users.username AS username, users.display_name AS display_name, users.org_str AS org_str, users.status AS status").
		Joins("JOIN users ON users.id = user_data_scopes.member_id AND users.deleted_at IS NULL").
		Where("user_data_scopes.manager_id IN ?", managerIds).
		Order("user_data_scopes.id").
		Scan(&memberRows).Error
	if err != nil {
		return nil, 0, err
	}
	membersByManager := make(map[int][]UserDataScopeMember, len(managerIds))
	for _, row := range memberRows {
		membersByManager[row.ManagerId] = append(membersByManager[row.ManagerId], row.UserDataScopeMember)
	}
	usersById := make(map[int]UserDataScopeMember, len(managerUsers))
	for _, user := range managerUsers {
		usersById[user.Id] = user
	}
	for _, row := range pageRows {
		user, ok := usersById[row.ManagerId]
		if !ok {
			continue
		}
		members := membersByManager[row.ManagerId]
		if members == nil {
			members = []UserDataScopeMember{}
		}
		managers = append(managers, UserDataScopeManager{
			Id:          user.Id,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			OrgStr:      user.OrgStr,
			Status:      user.Status,
			MemberCount: len(members),
			Members:     members,
			CreatedAt:   row.CreatedAt,
		})
	}
	return managers, total, nil
}

func GetUserDataScopeMemberIds(managerId int) ([]int, error) {
	memberIds := make([]int, 0)
	err := DB.Table("user_data_scopes").
		Joins("JOIN users ON users.id = user_data_scopes.member_id AND users.deleted_at IS NULL").
		Where("user_data_scopes.manager_id = ?", managerId).
		Order("user_data_scopes.id").
		Pluck("user_data_scopes.member_id", &memberIds).Error
	if err != nil {
		return nil, err
	}
	return memberIds, nil
}

// ReplaceUserDataScope drops duplicates and the manager's own id, and returns the stored ids.
func ReplaceUserDataScope(managerId int, memberIds []int, updatedBy int) ([]int, error) {
	if len(memberIds) > MaxUserDataScopeMembers {
		return nil, common.NewMessage("An employee can manage at most {{max}} people", map[string]any{"max": MaxUserDataScopeMembers})
	}
	members := make([]int, 0, len(memberIds))
	seen := make(map[int]struct{}, len(memberIds))
	for _, id := range memberIds {
		if id <= 0 {
			return nil, common.NewMessage("Invalid member ID")
		}
		if _, dup := seen[id]; dup || id == managerId {
			continue
		}
		seen[id] = struct{}{}
		members = append(members, id)
	}
	if len(members) == 0 {
		return nil, common.NewMessage("Select at least one person the employee can manage")
	}

	now := common.GetTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Serializes concurrent saves of the same manager.
		var manager User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", managerId).Take(&manager).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return common.NewMessage("Employee does not exist or has been deleted")
			}
			return err
		}
		var existing int64
		if err := tx.Model(&User{}).Where("id IN ?", members).Count(&existing).Error; err != nil {
			return err
		}
		if existing != int64(len(members)) {
			return common.NewMessage("Some selected people do not exist or have been deleted")
		}
		if err := tx.Where("manager_id = ?", managerId).Delete(&UserDataScope{}).Error; err != nil {
			return err
		}
		rows := make([]UserDataScope, len(members))
		for i, memberId := range members {
			rows[i] = UserDataScope{ManagerId: managerId, MemberId: memberId, CreatedAt: now, UpdatedBy: updatedBy}
		}
		return tx.CreateInBatches(rows, 500).Error
	})
	if err != nil {
		return nil, err
	}
	return members, nil
}

func DeleteUserDataScope(managerId int) error {
	return DB.Where("manager_id = ?", managerId).Delete(&UserDataScope{}).Error
}
