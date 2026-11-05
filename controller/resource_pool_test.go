package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIsMaskedSecret(t *testing.T) {
	assert.True(t, isMaskedSecret("****abcd"))
	assert.True(t, isMaskedSecret("****"))
	assert.False(t, isMaskedSecret(""))
	assert.False(t, isMaskedSecret("plain"))
	assert.False(t, isMaskedSecret("abc"))
	// A short secret is never mistaken for a mask.
	assert.False(t, isMaskedSecret("***"))
}

func TestIsValidResourcePoolProvider(t *testing.T) {
	for _, provider := range []string{"claude", "gpt", "gemini", "grok"} {
		assert.True(t, isValidResourcePoolProvider(provider), provider)
	}
	for _, provider := range []string{"", "openai", "Claude", "anthropic"} {
		assert.False(t, isValidResourcePoolProvider(provider), provider)
	}
}

func setupCapacityPoolControllerTest(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ResourcePoolSource{}, &model.CapacityPool{}))
	model.DB = db
	previousEnabled, previousClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = false, nil
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled, common.RDB = previousEnabled, previousClient
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func callCapacityHandler(t *testing.T, handler gin.HandlerFunc, target string, out any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, target, nil)
	handler(context)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), out))
}

// 绑了容量池的 <渠道, 模型>：能力页的余量接口返回整个池的额度与用量，并附上成员自己的用量；
// 资源池页的容量接口按池列出每个成员各用了多少。
func TestCapacityPoolCapacityForBoundMembers(t *testing.T) {
	db := setupCapacityPoolControllerTest(t)
	require.NoError(t, db.Create(&model.ResourcePoolSource{Id: 1, Provider: model.ResourcePoolClaude, Name: "kiro", Endpoint: "https://k.example.com"}).Error)
	pool := &model.CapacityPool{Name: "kiro-aws1", Provider: model.ResourcePoolClaude}
	require.NoError(t, model.SaveCapacityPool(pool, model.CapacityPoolConfig{SourceIds: []int64{1}, Floor: dto.ModelLimit{RPM: 9}}))
	// 渠道号取大值，避开同一进程里其它测试的内存计数池。
	bound := fmt.Sprintf(`{"model_limits":{"claude-sonnet":{"rpm":1}},"capacity_pools":{"claude-sonnet":%d}}`, pool.Id)
	mapping := `{"sonnet":"claude-sonnet"}`
	for _, id := range []int{9101, 9102} {
		require.NoError(t, db.Create(&model.Channel{Id: id, Name: fmt.Sprint(id), Models: "sonnet", Status: common.ChannelStatusEnabled,
			ModelMapping: &mapping, Setting: &bound}).Error)
	}
	require.NoError(t, db.Create(&model.Channel{Id: 9103, Name: "unbound", Models: "sonnet", Status: common.ChannelStatusEnabled}).Error)

	poolID := channelcapacity.CapacityPoolID(pool.Id)
	leases := []*channelcapacity.Lease{
		channelcapacity.Begin(9101, "claude-sonnet", pool.Id, 10),
		channelcapacity.Begin(9101, "claude-sonnet", pool.Id, 10),
		channelcapacity.Begin(9102, "claude-sonnet", pool.Id, 10),
	}
	t.Cleanup(func() {
		for _, lease := range leases {
			channelcapacity.Finish(lease, false, 0, 0)
		}
	})

	// 还没发布动态额度：用保底额度。
	var response struct {
		Data struct {
			Items []struct {
				ChannelId    int               `json:"channel_id"`
				PoolId       string            `json:"pool_id"`
				Limit        dto.ModelLimit    `json:"limit"`
				RPMUsed      int64             `json:"rpm_used"`
				Remaining    remainingCapacity `json:"remaining"`
				CapacityPool *struct {
					Id            int    `json:"id"`
					Name          string `json:"name"`
					Source        string `json:"source"`
					MemberRPMUsed int64  `json:"member_rpm_used"`
				} `json:"capacity_pool"`
			} `json:"items"`
		} `json:"data"`
	}
	callCapacityHandler(t, GetChannelModelCapacity, "/api/channel-model-capabilities/capacity?model=sonnet&channel_ids=9101,9102,9103", &response)
	require.Len(t, response.Data.Items, 3)
	first := response.Data.Items[0]
	assert.Equal(t, poolID, first.PoolId)
	assert.Equal(t, dto.ModelLimit{RPM: 9}, first.Limit, "绑定期间自己的 model_limits 不生效")
	assert.Equal(t, int64(3), first.RPMUsed, "整个池的用量")
	require.NotNil(t, first.Remaining.RPM)
	assert.Equal(t, int64(6), *first.Remaining.RPM)
	require.NotNil(t, first.CapacityPool)
	assert.Equal(t, "kiro-aws1", first.CapacityPool.Name)
	assert.Equal(t, "floor", first.CapacityPool.Source)
	assert.Equal(t, int64(2), first.CapacityPool.MemberRPMUsed, "成员自己的用量")
	assert.Nil(t, response.Data.Items[2].CapacityPool, "没绑定的渠道不带池信息")

	// 发布动态额度后按它算；耗尽时剩余都是 0。
	require.NoError(t, channelcapacity.PublishLimit(poolID, channelcapacity.PublishedLimit{RPM: 5, ComputedAt: 1}, time.Minute))
	callCapacityHandler(t, GetChannelModelCapacity, "/api/channel-model-capabilities/capacity?model=sonnet&channel_ids=9101", &response)
	assert.Equal(t, "dynamic", response.Data.Items[0].CapacityPool.Source)
	assert.Equal(t, int64(2), *response.Data.Items[0].Remaining.RPM)
	require.NoError(t, channelcapacity.PublishLimit(poolID, channelcapacity.PublishedLimit{Exhausted: true, ComputedAt: 2}, time.Minute))
	callCapacityHandler(t, GetChannelModelCapacity, "/api/channel-model-capabilities/capacity?model=sonnet&channel_ids=9101", &response)
	assert.Equal(t, int64(0), *response.Data.Items[0].Remaining.RPM)

	var poolResponse struct {
		Data struct {
			Items []struct {
				Id        int                       `json:"id"`
				Exhausted bool                      `json:"exhausted"`
				RPMUsed   int64                     `json:"rpm_used"`
				Members   []capacityPoolMemberUsage `json:"members"`
			} `json:"items"`
		} `json:"data"`
	}
	callCapacityHandler(t, GetCapacityPoolCapacity, "/api/channel/resource_pools/capacity_pools/capacity?provider=claude", &poolResponse)
	require.Len(t, poolResponse.Data.Items, 1)
	assert.True(t, poolResponse.Data.Items[0].Exhausted)
	assert.Equal(t, int64(3), poolResponse.Data.Items[0].RPMUsed)
	assert.Equal(t, []capacityPoolMemberUsage{
		{ChannelId: 9101, UpstreamModel: "claude-sonnet", RPMUsed: 2, TPMUsed: 20, InFlight: 2},
		{ChannelId: 9102, UpstreamModel: "claude-sonnet", RPMUsed: 1, TPMUsed: 10, InFlight: 1},
	}, poolResponse.Data.Items[0].Members)
}
