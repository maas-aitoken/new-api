package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxLeaderChainDepth = 8

type UserLeader struct {
	Id            int    `json:"id"`
	UserId        int    `json:"user_id" gorm:"not null;uniqueIndex"`
	LeaderId      int    `json:"leader_id" gorm:"not null;index;default:0"`
	LeaderHoidcId string `json:"leader_hoidc_id" gorm:"type:varchar(64);index;default:''"`
	SyncedAt      int64  `json:"synced_at" gorm:"type:bigint;not null"`
}

func ReplaceAllUserLeaders(edges map[string]string) (int, error) {
	if len(edges) == 0 {
		return 0, nil
	}

	type idPair struct {
		Id      int
		HoidcId string
	}
	pairs := make([]idPair, 0, len(edges)*2)
	if err := DB.Model(&User{}).Select("id", "hoidc_id").
		Where("hoidc_id <> ''").Find(&pairs).Error; err != nil {
		return 0, err
	}
	idByHoidc := make(map[string]int, len(pairs))
	for _, pair := range pairs {
		idByHoidc[pair.HoidcId] = pair.Id
	}

	now := common.GetTimestamp()
	rows := make([]UserLeader, 0, len(edges))
	for userHoidc, leaderHoidc := range edges {
		if leaderHoidc == "" || userHoidc == leaderHoidc {
			continue
		}
		userId, ok := idByHoidc[userHoidc]
		if !ok {
			continue
		}
		leaderId, ok := idByHoidc[leaderHoidc]
		if !ok {
			continue
		}
		if leaderId == userId {
			continue
		}
		rows = append(rows, UserLeader{
			UserId:        userId,
			LeaderId:      leaderId,
			LeaderHoidcId: leaderHoidc,
			SyncedAt:      now,
		})
	}
	if len(rows) == 0 {
		return 0, nil
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("1 = 1").Delete(&UserLeader{}).Error; err != nil {
			return err
		}
		return tx.CreateInBatches(rows, 500).Error
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

func SyncUserLeader(userId int, leaderHoidcId string) error {
	if userId <= 0 {
		return common.NewMessage("Invalid user ID")
	}
	if leaderHoidcId == "" {
		return DB.Where("user_id = ?", userId).Delete(&UserLeader{}).Error
	}

	var leader User
	err := DB.Select("id").Where("hoidc_id = ?", leaderHoidcId).Take(&leader).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.SysLog(common.LogText("leader %s not provisioned yet, edge for user %d deferred to full sync", leaderHoidcId, userId))
		return nil
	}
	if err != nil {
		return err
	}
	// 自己是自己的 leader 会让链式遍历成环。
	if leader.Id == userId {
		return DB.Where("user_id = ?", userId).Delete(&UserLeader{}).Error
	}

	row := UserLeader{
		UserId:        userId,
		LeaderId:      leader.Id,
		LeaderHoidcId: leaderHoidcId,
		SyncedAt:      common.GetTimestamp(),
	}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"leader_id", "leader_hoidc_id", "synced_at"}),
	}).Create(&row).Error
}

func IsUserLeader(userId int) (bool, error) {
	if userId <= 0 {
		return false, nil
	}
	var count int64
	err := DB.Table("user_leaders").
		Joins("JOIN users ON users.id = user_leaders.user_id AND users.deleted_at IS NULL").
		Where("user_leaders.leader_id = ?", userId).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// IsLeaderOf 判断 memberId 是否在 leaderId 的管辖链上
func IsLeaderOf(leaderId int, memberId int) (bool, error) {
	if leaderId <= 0 || memberId <= 0 || leaderId == memberId {
		return false, nil
	}
	current := memberId
	visited := make(map[int]struct{}, 6)
	for range MaxLeaderChainDepth {
		if _, loop := visited[current]; loop {
			// SSO 数据异常导致汇报关系成环
			common.SysError(common.LogText("leader chain cycle detected at user %d", current))
			return false, nil
		}
		visited[current] = struct{}{}

		var row UserLeader
		err := DB.Select("leader_id").
			Where("user_id = ? AND leader_id > 0", current).
			Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if row.LeaderId == leaderId {
			return true, nil
		}
		current = row.LeaderId
	}
	common.SysError(common.LogText("leader chain for user %d exceeds depth %d", memberId, MaxLeaderChainDepth))
	return false, nil
}

// GetLeaderTeamUserIds 返回 leaderId 管辖的全部下级 user id
func GetLeaderTeamUserIds(leaderId int) ([]int, error) {
	if leaderId <= 0 {
		return nil, nil
	}
	collected := make([]int, 0, 16)
	seen := map[int]struct{}{leaderId: {}}
	frontier := []int{leaderId}

	for depth := 0; depth < MaxLeaderChainDepth && len(frontier) > 0; depth++ {
		memberIds := make([]int, 0, len(frontier)*4)
		err := DB.Model(&UserLeader{}).
			Where("leader_id IN ?", frontier).
			Pluck("user_id", &memberIds).Error
		if err != nil {
			return nil, err
		}

		next := make([]int, 0, len(memberIds))
		for _, id := range memberIds {
			if id <= 0 {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			collected = append(collected, id)
			next = append(next, id)
		}
		frontier = next
	}

	if len(collected) == 0 {
		return nil, nil
	}
	// 已注销用户的令牌不该出现在团队视图里
	alive := make([]int, 0, len(collected))
	if err := DB.Model(&User{}).Where("id IN ?", collected).Pluck("id", &alive).Error; err != nil {
		return nil, err
	}
	return alive, nil
}
