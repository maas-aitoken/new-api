package model

import (
	"errors"

	"gorm.io/gorm"
)

func GetKeyViewTeamUserIds(userId int) ([]int, error) {
	if userId <= 0 {
		return nil, nil
	}
	seen := map[int]bool{userId: true}
	collected := make([]int, 0, 16)
	collect := func(ids []int) {
		for _, id := range ids {
			if id <= 0 || seen[id] {
				continue
			}
			seen[id] = true
			collected = append(collected, id)
		}
	}

	leaderTeam, err := GetLeaderTeamUserIds(userId)
	if err != nil {
		return nil, err
	}
	collect(leaderTeam)

	granted, err := GetUserDataScopeMemberIds(userId)
	if err != nil {
		return nil, err
	}
	collect(granted)

	headed, err := departmentHeadMemberIds(userId)
	if err != nil {
		return nil, err
	}
	collect(headed)

	if len(collected) == 0 {
		return nil, nil
	}
	return collected, nil
}

func departmentHeadMemberIds(userId int) ([]int, error) {
	var viewer User
	err := DB.Select("id", "org_str", "is_charge").Where("id = ?", userId).Take(&viewer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if viewer.IsCharge != 1 {
		return nil, nil
	}
	root := normalizeOrgPath(viewer.OrgStr)
	if root == "" {
		return nil, nil
	}

	var people []struct {
		Id     int
		OrgStr string
	}
	err = DB.Model(&User{}).Select("id", "org_str").
		Where("org_str <> '' AND id <> ?", userId).
		Order("id").Scan(&people).Error
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(people))
	for _, person := range people {
		if orgPathCovered(normalizeOrgPath(person.OrgStr), []string{root}) {
			ids = append(ids, person.Id)
		}
	}
	return ids, nil
}

func HasKeyViewTeam(userId int) (bool, error) {
	ids, err := GetKeyViewTeamUserIds(userId)
	if err != nil {
		return false, err
	}
	return len(ids) > 0, nil
}

func IsInKeyViewTeam(viewerId int, memberId int) (bool, error) {
	if viewerId <= 0 || memberId <= 0 || viewerId == memberId {
		return false, nil
	}

	isLeader, err := IsLeaderOf(viewerId, memberId)
	if err != nil {
		return false, err
	}
	if isLeader {
		return true, nil
	}

	var picked int64
	err = DB.Model(&UserDataScope{}).
		Where("manager_id = ? AND member_id = ?", viewerId, memberId).
		Count(&picked).Error
	if err != nil {
		return false, err
	}
	if picked > 0 {
		return true, nil
	}

	roots, err := GetUserDataScopeOrgPaths(viewerId)
	if err != nil {
		return false, err
	}
	var viewer User
	err = DB.Select("id", "org_str", "is_charge").Where("id = ?", viewerId).Take(&viewer).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if err == nil && viewer.IsCharge == 1 {
		if root := normalizeOrgPath(viewer.OrgStr); root != "" {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return false, nil
	}

	var member User
	err = DB.Select("id", "org_str").Where("id = ?", memberId).Take(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return orgPathCovered(normalizeOrgPath(member.OrgStr), roots), nil
}
