package middleware

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupContextForTokenInjectsRoutingPreference(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	// 旧版本写入的约束字段（max_attempts 等）已不再生效，解析时被忽略。
	token := &model.Token{Id: 1, UserId: 2, RoutingPreference: `{"mode":"latency","max_attempts":2}`}

	require.NoError(t, SetupContextForToken(ctx, token))
	preference, ok := common.GetContextKeyType[*dto.RoutingPreference](ctx, constant.ContextKeyTokenRoutingPreference)
	require.True(t, ok)
	assert.Equal(t, "latency", preference.Mode)
	assert.Equal(t, dto.RoutingPreference{Mode: "latency"}, *preference)
}

func TestSetupContextForTokenWithoutRoutingPreferenceInheritsDefault(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	require.NoError(t, SetupContextForToken(ctx, &model.Token{Id: 1, UserId: 2}))
	_, ok := common.GetContextKey(ctx, constant.ContextKeyTokenRoutingPreference)
	assert.False(t, ok)
}

// Distribute 和 /v1/models 只认上下文里的有效模式：空值（旧令牌）和业务 key 都是锁定模型。
func TestSetupContextForTokenSetsTheEffectiveRoutingMode(t *testing.T) {
	for _, tc := range []struct {
		scene common.TokenScene
		mode  common.TokenRoutingMode
		want  common.TokenRoutingMode
	}{
		{scene: common.TokenSceneOffice, mode: common.TokenRoutingModeSmart, want: common.TokenRoutingModeSmart},
		{scene: common.TokenSceneOffice, mode: common.TokenRoutingModeFixed, want: common.TokenRoutingModeFixed},
		{scene: common.TokenSceneOffice, mode: "", want: common.TokenRoutingModeFixed},
		{scene: common.TokenSceneBusiness, mode: common.TokenRoutingModeSmart, want: common.TokenRoutingModeFixed},
	} {
		ctx := newTokenAutoGroupsContext()
		require.NoError(t, SetupContextForToken(ctx, &model.Token{Id: 1, UserId: 2, Scene: tc.scene, RoutingMode: tc.mode}))
		got, ok := common.GetContextKeyType[common.TokenRoutingMode](ctx, constant.ContextKeyTokenRoutingMode)
		require.True(t, ok)
		assert.Equal(t, tc.want, got, "scene=%q mode=%q", tc.scene, tc.mode)
	}
}

func TestSetupContextForTokenMalformedRoutingPreferenceFallsBackToDefault(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{Id: 1, UserId: 2, RoutingPreference: `not-json`}

	require.NoError(t, SetupContextForToken(ctx, token))
	_, ok := common.GetContextKey(ctx, constant.ContextKeyTokenRoutingPreference)
	assert.False(t, ok)
}
