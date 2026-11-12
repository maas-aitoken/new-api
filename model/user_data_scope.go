package model

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	MaxUserDataScopeMembers  = 5000
	MaxUserDataScopeOrgPaths = 500
	maxUserDataScopeOrgPath  = 512
)

// UserDataScope rows are people picked one by one, replaced as a whole per manager.
type UserDataScope struct {
	Id        int   `json:"id"`
	ManagerId int   `json:"manager_id" gorm:"not null;uniqueIndex:idx_user_data_scope_pair,priority:1"`
	MemberId  int   `json:"member_id" gorm:"not null;uniqueIndex:idx_user_data_scope_pair,priority:2"`
	CreatedAt int64 `json:"created_at" gorm:"type:bigint;not null"`
	UpdatedBy int   `json:"updated_by" gorm:"not null"`
}

// UserDataScopeOrg rows are departments granted as a whole; they follow users.org_str.
// Saves are serialized by the manager row lock, so no unique index is needed.
type UserDataScopeOrg struct {
	Id        int    `json:"id"`
	ManagerId int    `json:"manager_id" gorm:"not null;index"`
	OrgPath   string `json:"org_path" gorm:"type:varchar(512);not null"`
	CreatedAt int64  `json:"created_at" gorm:"type:bigint;not null"`
	UpdatedBy int    `json:"updated_by" gorm:"not null"`
}

func normalizeOrgPath(orgStr string) string {
	segments := make([]string, 0)
	for segment := range strings.SplitSeq(orgStr, "/") {
		if segment = strings.TrimSpace(segment); segment != "" {
			segments = append(segments, segment)
		}
	}
	return strings.Join(segments, "/")
}

func orgPathCovered(path string, roots []string) bool {
	if path == "" {
		return false
	}
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
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
	// Members includes the people of OrgPaths.
	OrgPaths  []string `json:"org_paths"`
	CreatedAt int64    `json:"created_at"`
}

type userDataScopeMemberRow struct {
	ManagerId int
	UserDataScopeMember
}

func GetUserDirectory() ([]UserDirectoryEntry, error) {
	entries := make([]UserDirectoryEntry, 0)
	if err := DB.Model(&User{}).Select("id", "username", "display_name", "avatar", "org_str", "status").Order("id").Find(&entries).Error; err != nil {
		return nil, err
	}
	savedAt, err := userDataScopeSavedAt()
	if err != nil {
		return nil, err
	}
	managerIds := make([]int, 0, len(savedAt))
	for id := range savedAt {
		managerIds = append(managerIds, id)
	}
	members, err := resolveDataScopeMembers(managerIds)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].ManagedCount = len(members[entries[i].Id])
	}
	return entries, nil
}

func userDataScopeSavedAt() (map[int]int64, error) {
	savedAt := make(map[int]int64)
	for _, table := range []string{"user_data_scopes", "user_data_scope_orgs"} {
		var rows []struct {
			ManagerId int
			CreatedAt int64
		}
		if err := DB.Table(table).Select("manager_id, MAX(created_at) AS created_at").Group("manager_id").Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			savedAt[row.ManagerId] = max(savedAt[row.ManagerId], row.CreatedAt)
		}
	}
	return savedAt, nil
}

// resolveDataScopeMembers: picked people in save order, then granted departments;
// deleted users and the manager are left out.
func resolveDataScopeMembers(managerIds []int) (map[int][]UserDataScopeMember, error) {
	members := make(map[int][]UserDataScopeMember, len(managerIds))
	if len(managerIds) == 0 {
		return members, nil
	}
	var picked []userDataScopeMemberRow
	err := DB.Table("user_data_scopes").
		Select("user_data_scopes.manager_id AS manager_id, users.id AS id, users.username AS username, users.display_name AS display_name, users.org_str AS org_str, users.status AS status").
		Joins("JOIN users ON users.id = user_data_scopes.member_id AND users.deleted_at IS NULL").
		Where("user_data_scopes.manager_id IN ?", managerIds).
		Order("user_data_scopes.id").
		Scan(&picked).Error
	if err != nil {
		return nil, err
	}
	var orgRows []UserDataScopeOrg
	if err := DB.Where("manager_id IN ?", managerIds).Order("id").Find(&orgRows).Error; err != nil {
		return nil, err
	}
	seen := make(map[int]map[int]bool, len(managerIds))
	add := func(managerId int, member UserDataScopeMember) {
		if seen[managerId] == nil {
			seen[managerId] = make(map[int]bool)
		}
		if member.Id == managerId || seen[managerId][member.Id] {
			return
		}
		seen[managerId][member.Id] = true
		members[managerId] = append(members[managerId], member)
	}
	for _, row := range picked {
		add(row.ManagerId, row.UserDataScopeMember)
	}
	if len(orgRows) == 0 {
		return members, nil
	}
	roots := make(map[int][]string)
	for _, row := range orgRows {
		roots[row.ManagerId] = append(roots[row.ManagerId], row.OrgPath)
	}
	var people []UserDataScopeMember
	if err := DB.Model(&User{}).Select("id", "username", "display_name", "org_str", "status").Where("org_str <> ''").Order("id").Find(&people).Error; err != nil {
		return nil, err
	}
	for _, person := range people {
		path := normalizeOrgPath(person.OrgStr)
		for managerId, managerRoots := range roots {
			if orgPathCovered(path, managerRoots) {
				add(managerId, person)
			}
		}
	}
	return members, nil
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

// ListUserDataScopeManagers pages managers, latest saved first; the keyword matches case-insensitively.
func ListUserDataScopeManagers(keyword string, startIdx int, num int) ([]UserDataScopeManager, int64, error) {
	managers := make([]UserDataScopeManager, 0)
	savedAt, err := userDataScopeSavedAt()
	if err != nil || len(savedAt) == 0 {
		return managers, 0, err
	}
	managerIds := make([]int, 0, len(savedAt))
	for id := range savedAt {
		managerIds = append(managerIds, id)
	}
	var managerUsers []UserDataScopeMember
	if err := DB.Model(&User{}).Select("id", "username", "display_name", "org_str", "status").Where("id IN ?", managerIds).Find(&managerUsers).Error; err != nil {
		return nil, 0, err
	}
	needle := strings.ToLower(keyword)
	matched := make([]UserDataScopeMember, 0, len(managerUsers))
	for _, user := range managerUsers {
		if strings.Contains(strings.ToLower(user.Username), needle) || strings.Contains(strings.ToLower(user.DisplayName), needle) {
			matched = append(matched, user)
		}
	}
	slices.SortFunc(matched, func(a, b UserDataScopeMember) int {
		return cmp.Or(cmp.Compare(savedAt[b.Id], savedAt[a.Id]), cmp.Compare(b.Id, a.Id))
	})
	total := int64(len(matched))
	page := matched[min(max(startIdx, 0), len(matched)):min(max(startIdx, 0)+max(num, 0), len(matched))]
	if len(page) == 0 {
		return managers, total, nil
	}
	pageIds := make([]int, len(page))
	for i, user := range page {
		pageIds[i] = user.Id
	}
	membersByManager, err := resolveDataScopeMembers(pageIds)
	if err != nil {
		return nil, 0, err
	}
	var orgRows []UserDataScopeOrg
	if err := DB.Where("manager_id IN ?", pageIds).Order("id").Find(&orgRows).Error; err != nil {
		return nil, 0, err
	}
	orgPathsByManager := make(map[int][]string, len(pageIds))
	for _, row := range orgRows {
		orgPathsByManager[row.ManagerId] = append(orgPathsByManager[row.ManagerId], row.OrgPath)
	}
	for _, user := range page {
		members := membersByManager[user.Id]
		if members == nil {
			members = []UserDataScopeMember{}
		}
		orgPaths := orgPathsByManager[user.Id]
		if orgPaths == nil {
			orgPaths = []string{}
		}
		managers = append(managers, UserDataScopeManager{
			Id:          user.Id,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			OrgStr:      user.OrgStr,
			Status:      user.Status,
			MemberCount: len(members),
			Members:     members,
			OrgPaths:    orgPaths,
			CreatedAt:   savedAt[user.Id],
		})
	}
	return managers, total, nil
}

func GetUserDataScopeMemberIds(managerId int) ([]int, error) {
	members, err := resolveDataScopeMembers([]int{managerId})
	if err != nil {
		return nil, err
	}
	memberIds := make([]int, 0, len(members[managerId]))
	for _, member := range members[managerId] {
		memberIds = append(memberIds, member.Id)
	}
	return memberIds, nil
}

func GetUserDataScopeOrgPaths(managerId int) ([]string, error) {
	orgPaths := make([]string, 0)
	if err := DB.Model(&UserDataScopeOrg{}).Where("manager_id = ?", managerId).Order("id").Pluck("org_path", &orgPaths).Error; err != nil {
		return nil, err
	}
	return orgPaths, nil
}

type UsageViewerScope struct {
	// nil for the whole company.
	UserIDs []int
	// Filters the console preselects; nil leaves them open.
	DefaultBusinessUnit *string
	DefaultDepartment   *string
}

// GetUsageViewerScope adds the department the viewer heads (is_charge) to the permission settings;
// an SSO account without a department sees the whole company. Matched in Go so LIKE wildcards
// cannot widen it. The defaults preselect the single business unit (and department) of those seen.
func GetUsageViewerScope(viewerId int) (*UsageViewerScope, error) {
	scope := &UsageViewerScope{UserIDs: []int{viewerId}}
	var viewer User
	if err := DB.Select("id", "org_str", "is_charge", "hoidc_id").Where("id = ?", viewerId).Take(&viewer).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return scope, nil
		}
		return nil, err
	}
	viewerPath := normalizeOrgPath(viewer.OrgStr)
	if viewer.HoidcId != "" && viewerPath == "" {
		return &UsageViewerScope{}, nil
	}
	roots, err := GetUserDataScopeOrgPaths(viewerId)
	if err != nil {
		return nil, err
	}
	if viewer.IsCharge == 1 && viewerPath != "" {
		roots = append(roots, viewerPath)
	}
	var pickedIds []int
	if err := DB.Model(&UserDataScope{}).Where("manager_id = ?", viewerId).Pluck("member_id", &pickedIds).Error; err != nil {
		return nil, err
	}
	if len(roots) == 0 && len(pickedIds) == 0 {
		return scope, nil
	}
	picked := make(map[int]bool, len(pickedIds))
	for _, id := range pickedIds {
		picked[id] = true
	}
	// A business unit granted or run as a whole is a one-segment root.
	unitRoots := make([]string, 0)
	for _, root := range roots {
		if !strings.Contains(root, "/") {
			unitRoots = append(unitRoots, root)
		}
	}

	var people []struct {
		Id           int
		OrgStr       string
		BusinessUnit string
		Department   string
	}
	err = DB.Table("users").
		Select("id, org_str, "+usageBusinessUnitExpr+" AS business_unit, "+usageDepartmentExpr+" AS department").
		Where("deleted_at IS NULL AND id <> ?", viewerId).
		Order("id").
		Scan(&people).Error
	if err != nil {
		return nil, err
	}
	type orgPair struct{ businessUnit, department string }
	pairs := make(map[orgPair]bool)
	units := make(map[string]bool)
	wholeUnit := false
	for _, person := range people {
		path := normalizeOrgPath(person.OrgStr)
		if !picked[person.Id] && !orgPathCovered(path, roots) {
			continue
		}
		scope.UserIDs = append(scope.UserIDs, person.Id)
		pairs[orgPair{person.BusinessUnit, person.Department}] = true
		units[person.BusinessUnit] = true
		wholeUnit = wholeUnit || orgPathCovered(path, unitRoots)
	}

	if len(units) != 1 {
		return scope, nil
	}
	for pair := range pairs {
		// An unassigned business unit is no department to preselect.
		if pair.businessUnit == "" {
			return scope, nil
		}
		scope.DefaultBusinessUnit = &pair.businessUnit
		if len(pairs) == 1 && !wholeUnit && pair.department != "" {
			scope.DefaultDepartment = &pair.department
		}
	}
	return scope, nil
}

// ReplaceUserDataScope keeps the outermost departments and the people outside them, so leaving
// a department drops someone out. Duplicates and the manager's own id are dropped.
func ReplaceUserDataScope(managerId int, memberIds []int, orgPaths []string, updatedBy int) ([]int, []string, error) {
	if len(memberIds) > MaxUserDataScopeMembers {
		return nil, nil, common.NewMessage("An employee can manage at most {{max}} people", map[string]any{"max": MaxUserDataScopeMembers})
	}
	if len(orgPaths) > MaxUserDataScopeOrgPaths {
		return nil, nil, common.NewMessage("Invalid parameters")
	}
	members := make([]int, 0, len(memberIds))
	seen := make(map[int]struct{}, len(memberIds))
	for _, id := range memberIds {
		if id <= 0 {
			return nil, nil, common.NewMessage("Invalid member ID")
		}
		if _, dup := seen[id]; dup || id == managerId {
			continue
		}
		seen[id] = struct{}{}
		members = append(members, id)
	}
	paths := make([]string, 0, len(orgPaths))
	for _, orgPath := range orgPaths {
		path := normalizeOrgPath(orgPath)
		if path == "" || utf8.RuneCountInString(path) > maxUserDataScopeOrgPath {
			return nil, nil, common.NewMessage("Invalid parameters")
		}
		paths = append(paths, path)
	}
	// Shorter paths first, so a department below a granted one is dropped.
	slices.SortStableFunc(paths, func(a, b string) int { return cmp.Compare(len(a), len(b)) })
	outermost := make([]string, 0, len(paths))
	for _, path := range paths {
		if !orgPathCovered(path, outermost) {
			outermost = append(outermost, path)
		}
	}
	if len(members) == 0 && len(outermost) == 0 {
		return nil, nil, common.NewMessage("Select at least one person the employee can manage")
	}

	now := common.GetTimestamp()
	stored := make([]int, 0, len(members))
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Serializes concurrent saves of the same manager.
		var manager User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", managerId).Take(&manager).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return common.NewMessage("Employee does not exist or has been deleted")
			}
			return err
		}
		var existing []User
		if len(members) > 0 {
			if err := tx.Select("id", "org_str").Where("id IN ?", members).Find(&existing).Error; err != nil {
				return err
			}
		}
		if len(existing) != len(members) {
			return common.NewMessage("Some selected people do not exist or have been deleted")
		}
		insideGranted := make(map[int]bool, len(existing))
		for _, user := range existing {
			insideGranted[user.Id] = orgPathCovered(normalizeOrgPath(user.OrgStr), outermost)
		}
		for _, id := range members {
			if !insideGranted[id] {
				stored = append(stored, id)
			}
		}
		if err := tx.Where("manager_id = ?", managerId).Delete(&UserDataScope{}).Error; err != nil {
			return err
		}
		if err := tx.Where("manager_id = ?", managerId).Delete(&UserDataScopeOrg{}).Error; err != nil {
			return err
		}
		if len(stored) > 0 {
			rows := make([]UserDataScope, len(stored))
			for i, memberId := range stored {
				rows[i] = UserDataScope{ManagerId: managerId, MemberId: memberId, CreatedAt: now, UpdatedBy: updatedBy}
			}
			if err := tx.CreateInBatches(rows, 500).Error; err != nil {
				return err
			}
		}
		if len(outermost) == 0 {
			return nil
		}
		orgRows := make([]UserDataScopeOrg, len(outermost))
		for i, path := range outermost {
			orgRows[i] = UserDataScopeOrg{ManagerId: managerId, OrgPath: path, CreatedAt: now, UpdatedBy: updatedBy}
		}
		return tx.CreateInBatches(orgRows, 500).Error
	})
	if err != nil {
		return nil, nil, err
	}
	return stored, outermost, nil
}

func DeleteUserDataScope(managerId int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("manager_id = ?", managerId).Delete(&UserDataScope{}).Error; err != nil {
			return err
		}
		return tx.Where("manager_id = ?", managerId).Delete(&UserDataScopeOrg{}).Error
	})
}
