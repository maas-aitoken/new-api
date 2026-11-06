package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// TokenActor 记录调用者是以什么身份拿到这个令牌的，用于审计归因
type TokenActor struct {
	IsOwner         bool
	IsProjectMember bool
	IsLeader        bool
}

func getTokenForActor(
	id int,
	userId int,
	allow func(TokenActor) bool,
) (*Token, TokenActor, error) {
	if id <= 0 || userId <= 0 {
		return nil, TokenActor{}, common.NewMessage("Token ID or user ID is empty")
	}
	token, err := GetTokenById(id)
	if err != nil {
		return nil, TokenActor{}, err
	}

	actor := TokenActor{
		IsOwner:         token.UserId == userId,
		IsProjectMember: token.IsProjectMember(userId),
	}

	if !actor.IsOwner && !actor.IsProjectMember && token.IsProjectKey() {
		isLeader, err := IsLeaderOf(userId, token.UserId)
		if err != nil {
			return nil, TokenActor{}, err
		}
		actor.IsLeader = isLeader
	}
	if !allow(actor) {
		return nil, TokenActor{}, gorm.ErrRecordNotFound
	}
	return token, actor, nil
}

func GetTokenForReveal(id int, userId int) (*Token, TokenActor, error) {
	return getTokenForActor(id, userId, func(a TokenActor) bool {
		return a.IsOwner || a.IsProjectMember || a.IsLeader
	})
}

func GetTokenForWrite(id int, userId int) (*Token, TokenActor, error) {
	return getTokenForActor(id, userId, func(a TokenActor) bool {
		return a.IsOwner || a.IsLeader
	})
}
