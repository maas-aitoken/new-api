package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenAutoGroupsRoundTripThroughRedisHashCache(t *testing.T) {
	useUserCacheMiniRedis(t)
	token := Token{
		Id:         42,
		UserId:     7,
		Key:        "token-auto-groups-cache-key",
		Name:       "auto-cache",
		Group:      "auto",
		AutoGroups: `["vip","default"]`,
	}

	require.NoError(t, cacheSetTokenForTest(token))
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, token.AutoGroups, cached.AutoGroups)
	groups, err := cached.GetAutoGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"vip", "default"}, groups)
}

func TestTokenAllowUserQuotaQueryRoundTripsThroughRedisHashCache(t *testing.T) {
	server := useUserCacheMiniRedis(t)
	token := Token{Id: 43, UserId: 7, Key: "token-user-quota-cache-key", Name: "user-quota-cache", AllowUserQuotaQuery: true}

	initialized, err := cacheInitToken(token)
	require.NoError(t, err)
	require.Equal(t, 1, initialized)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.True(t, cached.AllowUserQuotaQuery)
	assert.Positive(t, server.TTL(getTokenCacheKey(token.Key)))
}

func TestTokenAutoPreferenceRoundTripsThroughRedisHashCache(t *testing.T) {
	server := useUserCacheMiniRedis(t)
	token := Token{Id: 44, UserId: 7, Key: "token-auto-preference-cache-key", Name: "auto-preference-cache", AutoPreference: "cost"}

	initialized, err := cacheInitToken(token)
	require.NoError(t, err)
	require.Equal(t, 1, initialized)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, "cost", cached.AutoPreference)
	assert.Positive(t, server.TTL(getTokenCacheKey(token.Key)), "新字段占了 ARGV 位置，过期时间的下标要跟着后移")

	// 升级前写进缓存的令牌没有这个字段：读出来为空，走平台默认，不报错。
	legacyKey := "token-auto-preference-legacy-key"
	server.HSet(getTokenCacheKey(legacyKey), "Id", "45", "UserId", "7", "Name", "legacy")
	legacy, err := cacheGetTokenByKey(legacyKey)
	require.NoError(t, err)
	assert.Empty(t, legacy.AutoPreference)
}

func TestTokenSceneRoundTripsThroughRedisHashCache(t *testing.T) {
	server := useUserCacheMiniRedis(t)
	token := Token{Id: 44, UserId: 7, Key: "token-scene-cache-key", Name: "scene-cache", Scene: common.TokenSceneBusiness}

	initialized, err := cacheInitToken(token)
	require.NoError(t, err)
	require.Equal(t, 1, initialized)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, common.TokenSceneBusiness, cached.Scene)
	assert.Positive(t, server.TTL(getTokenCacheKey(token.Key)))
}

// RPM decides whether TokenRateLimit throttles a key, so losing it in the cache
// is worse than losing a display field: RedisHGetObj leaves a field missing from
// the hash at its Go zero value, and 0 means "inherit the platform default".
// Dropping RPM from cacheInitToken would therefore silently ignore every key's
// own limit without raising an error, and -1 would flip from "never throttled"
// to "throttled at the platform rate".
func TestTokenRPMRoundTripsThroughRedisHashCache(t *testing.T) {
	for _, tc := range []struct {
		name string
		rpm  int
	}{
		{name: "own limit", rpm: 120},
		{name: "opted out", rpm: -1},
		{name: "inherits platform default", rpm: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := useUserCacheMiniRedis(t)
			token := Token{
				Id:     46,
				UserId: 7,
				Key:    "token-rpm-cache-key-" + tc.name,
				Name:   "rpm-cache",
				RPM:    tc.rpm,
			}

			initialized, err := cacheInitToken(token)
			require.NoError(t, err)
			require.Equal(t, 1, initialized)
			cached, err := cacheGetTokenByKey(token.Key)
			require.NoError(t, err)
			assert.Equal(t, tc.rpm, cached.RPM)
			assert.Positive(t, server.TTL(getTokenCacheKey(token.Key)), "新字段占了 ARGV 位置，过期时间的下标要跟着后移")
		})
	}
}

func TestTokenUpdatePersistsScene(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:         7,
		Key:            "token-scene-update-key",
		Name:           "scene-update",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Scene:          common.TokenSceneOffice,
	}
	require.NoError(t, token.Insert())

	token.Scene = common.TokenSceneBusiness
	require.NoError(t, token.Update())

	// Update 的 Select 是手工维护的白名单，漏列字段会静默不落库。
	reloaded, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, common.TokenSceneBusiness, reloaded.Scene)
}

func TestTokenUpdateSynchronouslyNarrowsPreheatedAutoGroupsCache(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:          7,
		Key:             "token-auto-groups-update-cache-key",
		Name:            "auto-cache-update",
		Status:          common.TokenStatusEnabled,
		ExpiredTime:     -1,
		UnlimitedQuota:  true,
		Group:           "auto",
		CrossGroupRetry: true,
		AutoGroups:      `["default","vip"]`,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, cacheSetTokenForTest(token))

	preheated, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.JSONEq(t, `["default","vip"]`, preheated.AutoGroups)

	require.NoError(t, token.SetAutoGroups([]string{"vip"}))
	require.NoError(t, token.Update())
	// Update 是限制性变更：写库前删除缓存并设置 fence。缓存不再提供旧的
	// 宽分组值，下一次读取必须看到收紧后的分组。
	_, cacheErr := cacheGetTokenByKey(token.Key)
	require.Error(t, cacheErr, "the pre-update cache entry must be invalidated")
	reloaded, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.JSONEq(t, `["vip"]`, reloaded.AutoGroups)
}

// cacheSetTokenForTest 以测试身份写入完整 token 缓存（含额度字段），
// 模拟“已水合”的缓存状态。
func cacheSetTokenForTest(token Token) error {
	_, err := cacheInitToken(token)
	return err
}
