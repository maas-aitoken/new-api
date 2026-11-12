package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// AdminTokenFilter root 视角的令牌筛选，不限定归属人。
type AdminTokenFilter struct {
	UserId      int // 0 表示不限用户
	Keyword     string
	Key         string
	Scene       common.TokenScene
	RoutingMode common.TokenRoutingMode
	Status      int
}

func (f AdminTokenFilter) apply(query *gorm.DB) (*gorm.DB, error) {
	if f.UserId > 0 {
		query = query.Where("user_id = ?", f.UserId)
	}
	if f.Keyword != "" {
		pattern, err := sanitizeLikePattern(f.Keyword)
		if err != nil {
			return nil, err
		}
		query = query.Where("name LIKE ? ESCAPE '!'", pattern)
	}
	if f.Key != "" {
		pattern, err := sanitizeLikePattern(strings.TrimPrefix(f.Key, "sk-"))
		if err != nil {
			return nil, err
		}
		query = query.Where(commonKeyCol+" LIKE ? ESCAPE '!'", pattern)
	}
	switch f.Scene {
	case common.TokenSceneBusiness:
		query = query.Where("scene = ?", string(common.TokenSceneBusiness))
	case common.TokenSceneOffice:
		query = query.Where("(scene IS NULL OR scene <> ?)", string(common.TokenSceneBusiness))
	}
	switch f.RoutingMode {
	case common.TokenRoutingModeFixed:
		query = query.Where("routing_mode = ?", string(common.TokenRoutingModeFixed))
	case common.TokenRoutingModeSmart:
		query = query.Where("routing_mode = ?", string(common.TokenRoutingModeSmart))
	}
	if f.Status != 0 {
		query = query.Where("status = ?", f.Status)
	}
	return query, nil
}

// ListAllTokens root 分页查询全站令牌。
func ListAllTokens(filter AdminTokenFilter, offset int, limit int) (tokens []*Token, total int64, err error) {
	baseQuery, err := filter.apply(DB.Model(&Token{}))
	if err != nil {
		return nil, 0, err
	}
	if err = baseQuery.Count(&total).Error; err != nil {
		common.SysError(common.LogText("failed to count all tokens: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to get the token count")
	}
	if err = baseQuery.Order("id desc").Offset(offset).Limit(limit).Find(&tokens).Error; err != nil {
		common.SysError(common.LogText("failed to list all tokens: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to get the token list")
	}
	return tokens, total, nil
}
