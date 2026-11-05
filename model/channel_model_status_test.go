package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupChannelModelStatusTest(t *testing.T, memoryCacheEnabled bool) {
	t.Helper()
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	previousCircuitBreakerEnabled := operation_setting.GetMonitorSetting().ChannelModelCircuitBreakerEnabled
	previousExcludedChannelIDs := operation_setting.GetMonitorSetting().ChannelModelExcludedChannelIDs
	common.MemoryCacheEnabled = memoryCacheEnabled
	operation_setting.GetMonitorSetting().ChannelModelCircuitBreakerEnabled = true
	require.NoError(t, DB.Exec("DELETE FROM channel_model_statuses").Error)
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM channel_model_statuses")
		DB.Exec("DELETE FROM abilities")
		DB.Exec("DELETE FROM channels")
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
		operation_setting.GetMonitorSetting().ChannelModelCircuitBreakerEnabled = previousCircuitBreakerEnabled
		operation_setting.GetMonitorSetting().ChannelModelExcludedChannelIDs = previousExcludedChannelIDs
		if previousMemoryCacheEnabled {
			InitChannelCache()
		}
	})
}

func insertChannelModelStatusFixture(t *testing.T) {
	t.Helper()
	for id, priority := range map[int]int64{1: 10, 2: 0} {
		weight := uint(100)
		channel := Channel{
			Id:       id,
			Type:     constant.ChannelTypeOpenAI,
			Key:      fmt.Sprintf("key-%d", id),
			Status:   common.ChannelStatusEnabled,
			Name:     fmt.Sprintf("channel-%d", id),
			Weight:   &weight,
			Models:   "model-a,model-b",
			Group:    "default",
			Priority: &priority,
		}
		require.NoError(t, DB.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(nil))
	}
}

func TestChannelModelDisableOnlyRemovesMatchingRoute(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			setupChannelModelStatusTest(t, memoryCacheEnabled)
			insertChannelModelStatusFixture(t)
			if memoryCacheEnabled {
				InitChannelCache()
			}

			changed, err := DisableChannelModel(1, "model-a", "upstream returned 429")
			require.NoError(t, err)
			require.True(t, changed)

			selectedA, err := GetRandomSatisfiedChannel("default", "model-a", 0, nil)
			require.NoError(t, err)
			require.NotNil(t, selectedA)
			assert.Equal(t, 2, selectedA.Id)

			selectedB, err := GetRandomSatisfiedChannel("default", "model-b", 0, nil)
			require.NoError(t, err)
			require.NotNil(t, selectedB)
			assert.Equal(t, 1, selectedB.Id)

			changed, err = EnableChannelModel(1, "model-a")
			require.NoError(t, err)
			require.True(t, changed)
			restored, err := GetRandomSatisfiedChannel("default", "model-a", 0, nil)
			require.NoError(t, err)
			require.NotNil(t, restored)
			assert.Equal(t, 1, restored.Id)
		})
	}
}

// 原生选路先找列了请求原名的渠道，扣掉熔断后一个不剩才退到路由名（去掉推理强度后缀）。
// GetRoutableChannelIDsForGroupModel 是发给调度器的渠道范围，必须与它一致。
func TestRoutableChannelIDsFollowNativeRouting(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			setupChannelModelStatusTest(t, memoryCacheEnabled)
			weight, priority := uint(100), int64(0)
			for id, models := range map[int]string{1: "gpt-5", 2: "gpt-5-high", 3: "other"} {
				channel := Channel{Id: id, Type: constant.ChannelTypeOpenAI, Key: fmt.Sprintf("key-%d", id), Status: common.ChannelStatusEnabled,
					Name: fmt.Sprintf("channel-%d", id), Weight: &weight, Models: models, Group: "default", Priority: &priority}
				require.NoError(t, DB.Create(&channel).Error)
				require.NoError(t, channel.AddAbilities(nil))
			}
			if memoryCacheEnabled {
				InitChannelCache()
			}
			native := func(requested string) int {
				selected, err := GetRandomSatisfiedChannel("default", requested, 0, nil)
				require.NoError(t, err)
				require.NotNil(t, selected, requested)
				return selected.Id
			}

			for _, requested := range []string{"gpt-5-low", "gpt-5@effort:high", "gpt-5"} {
				assert.Equal(t, []int{1}, GetRoutableChannelIDsForGroupModel("default", requested), "渠道只列了 gpt-5 时 %s 退到路由名", requested)
				assert.Equal(t, 1, native(requested), requested)
			}
			assert.Equal(t, []int{2}, GetRoutableChannelIDsForGroupModel("default", "gpt-5-high"), "列了原名的渠道优先，不再退到路由名")
			assert.Equal(t, 2, native("gpt-5-high"))
			assert.Empty(t, GetRoutableChannelIDsForGroupModel("default", "gpt-6-high"), "原名和路由名都没有渠道列")
			assert.Empty(t, GetRoutableChannelIDsForGroupModel("vip", "gpt-5-low"))

			// 列了原名的渠道全被熔断时退到路由名；熔断按请求原名记，gpt-5 本身不受影响。
			changed, err := DisableChannelModel(2, "gpt-5-high", "upstream returned 429")
			require.NoError(t, err)
			require.True(t, changed)
			assert.Equal(t, []int{1}, GetRoutableChannelIDsForGroupModel("default", "gpt-5-high"))
			assert.Equal(t, 1, native("gpt-5-high"))
			_, err = DisableChannelModel(1, "gpt-5-high", "upstream returned 429")
			require.NoError(t, err)
			assert.Empty(t, GetRoutableChannelIDsForGroupModel("default", "gpt-5-high"), "退到路由名时也按请求原名扣熔断")
			assert.Equal(t, []int{1}, GetRoutableChannelIDsForGroupModel("default", "gpt-5"))
		})
	}
}

func TestDisableChannelModelIsIdempotentAndPreservesOriginalReason(t *testing.T) {
	setupChannelModelStatusTest(t, false)
	insertChannelModelStatusFixture(t)

	changed, err := DisableChannelModel(1, "model-a", "first failure")
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = DisableChannelModel(1, "model-a", "later failure")
	require.NoError(t, err)
	assert.False(t, changed)

	var status ChannelModelStatus
	require.NoError(t, DB.First(&status, "channel_id = ? AND model = ?", 1, "model-a").Error)
	assert.Equal(t, "first failure", status.Reason)
}

func TestEnabledModelsRemainVisibleWhileAnotherChannelCanServeThem(t *testing.T) {
	setupChannelModelStatusTest(t, false)
	insertChannelModelStatusFixture(t)

	_, err := DisableChannelModel(1, "model-a", "first route failed")
	require.NoError(t, err)
	assert.Contains(t, GetGroupEnabledModels("default"), "model-a")

	_, err = DisableChannelModel(2, "model-a", "second route failed")
	require.NoError(t, err)
	assert.NotContains(t, GetGroupEnabledModels("default"), "model-a")
	assert.Contains(t, GetGroupEnabledModels("default"), "model-b")
}

func TestGetChannelIncludesDisabledModelsWithoutChangingConfiguredModels(t *testing.T) {
	setupChannelModelStatusTest(t, false)
	insertChannelModelStatusFixture(t)

	_, err := DisableChannelModel(1, "model-a", "upstream returned 429")
	require.NoError(t, err)
	channel, err := GetChannelById(1, false)
	require.NoError(t, err)
	require.NoError(t, AttachDisabledModels([]*Channel{channel}))

	assert.Equal(t, "model-a,model-b", channel.Models)
	assert.Equal(t, []string{"model-a"}, channel.DisabledModels)
}

func TestClearAllChannelModelStatusesRestoresEveryRoute(t *testing.T) {
	setupChannelModelStatusTest(t, true)
	insertChannelModelStatusFixture(t)
	InitChannelCache()

	changed, err := DisableChannelModel(1, "model-a", "upstream returned 429")
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, IsChannelModelDisabled(1, "model-a"))

	require.NoError(t, ClearAllChannelModelStatuses())
	assert.False(t, IsChannelModelDisabled(1, "model-a"))
	disabledModels, err := GetDisabledChannelModels(1)
	require.NoError(t, err)
	assert.Empty(t, disabledModels)
}

func TestChannelModelRecoveryRespectsDelayIntervalAndConsecutiveSuccesses(t *testing.T) {
	setupChannelModelStatusTest(t, false)
	insertChannelModelStatusFixture(t)
	now := int64(1_000_000)
	require.NoError(t, DB.Create(&ChannelModelStatus{
		ChannelId:    1,
		Model:        "model-a",
		DisabledTime: now - 299,
	}).Error)

	statuses, err := GetDueChannelModelRecoveries(now, 300, 60, 10)
	require.NoError(t, err)
	assert.Empty(t, statuses)

	require.NoError(t, DB.Model(&ChannelModelStatus{}).Where("channel_id = ? AND model = ?", 1, "model-a").Update("disabled_time", now-300).Error)
	statuses, err = GetDueChannelModelRecoveries(now, 300, 60, 10)
	require.NoError(t, err)
	require.Len(t, statuses, 1)

	count, exists, err := RecordChannelModelRecoveryProbe(1, "model-a", true, now)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, 1, count)
	statuses, err = GetDueChannelModelRecoveries(now+59, 300, 60, 10)
	require.NoError(t, err)
	assert.Empty(t, statuses)

	count, exists, err = RecordChannelModelRecoveryProbe(1, "model-a", true, now+60)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, 2, count)
	count, exists, err = RecordChannelModelRecoveryProbe(1, "model-a", false, now+120)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Zero(t, count)
}

func TestExcludedChannelCannotBeDisabledOrScheduledForRecovery(t *testing.T) {
	setupChannelModelStatusTest(t, false)
	insertChannelModelStatusFixture(t)
	operation_setting.GetMonitorSetting().ChannelModelExcludedChannelIDs = "1"

	changed, err := DisableChannelModel(1, "model-a", "upstream returned 429")
	require.NoError(t, err)
	assert.False(t, changed)

	require.NoError(t, DB.Create(&ChannelModelStatus{
		ChannelId: 1, Model: "model-a", DisabledTime: 100,
	}).Error)
	statuses, err := GetDueChannelModelRecoveries(1000, 0, 60, 10)
	require.NoError(t, err)
	assert.Empty(t, statuses)
	assert.False(t, IsChannelModelDisabled(1, "model-a"))
}

func TestExcludedChannelIgnoresExistingCircuitBreakerInEveryRoutingMode(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			setupChannelModelStatusTest(t, memoryCacheEnabled)
			insertChannelModelStatusFixture(t)
			require.NoError(t, DB.Create(&ChannelModelStatus{
				ChannelId: 1, Model: "model-a", DisabledTime: 100,
			}).Error)
			operation_setting.GetMonitorSetting().ChannelModelExcludedChannelIDs = "1"
			if memoryCacheEnabled {
				InitChannelCache()
			}

			selected, err := GetRandomSatisfiedChannel("default", "model-a", 0, nil)
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, 1, selected.Id)
		})
	}
}
