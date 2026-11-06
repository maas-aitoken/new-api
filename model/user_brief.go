package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	TokenMemberCandidateLimit = 20
	MinMemberCandidateKeyword = 1
)

type UserBrief struct {
	Id          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	OrgStr      string `json:"org_str"`
	Avatar      string `json:"avatar"`
}

func SearchMemberCandidates(keyword string) ([]UserBrief, error) {
	keyword = strings.TrimSpace(keyword)

	if len([]rune(keyword)) < MinMemberCandidateKeyword {
		return []UserBrief{}, nil
	}
	escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(keyword)
	like := "%" + escaped + "%"
	candidates := make([]UserBrief, 0, TokenMemberCandidateLimit)
	err := DB.Model(&User{}).
		Select("id", "username", "display_name", "org_str", "avatar").
		Where("status = ?", common.UserStatusEnabled).
		Where("(username LIKE ? ESCAPE '!' OR display_name LIKE ? ESCAPE '!')", like, like).
		Order("id").
		Limit(TokenMemberCandidateLimit).
		Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

// GetUserBriefsByIds 批量取用户信息
func GetUserBriefsByIds(ids []int) (map[int]UserBrief, error) {
	briefs := make(map[int]UserBrief, len(ids))
	if len(ids) == 0 {
		return briefs, nil
	}
	rows := make([]UserBrief, 0, len(ids))
	err := DB.Model(&User{}).
		Select("id", "username", "display_name", "org_str", "avatar").
		Where("id IN ?", ids).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		briefs[row.Id] = row
	}
	return briefs, nil
}

// FindMissingUserIds 返回 ids 里不存在或已注销的用户 id
func FindMissingUserIds(ids []int) ([]int, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	found := make([]int, 0, len(ids))
	if err := DB.Model(&User{}).Where("id IN ?", ids).Pluck("id", &found).Error; err != nil {
		return nil, err
	}
	foundSet := make(map[int]struct{}, len(found))
	for _, id := range found {
		foundSet[id] = struct{}{}
	}
	var missing []int
	for _, id := range ids {
		if _, ok := foundSet[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing, nil
}
