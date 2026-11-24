package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestAccumulateUsageFallsBackToInputAndOutputWhenTotalMissing(t *testing.T) {
	total := &dto.RealtimeUsage{}
	segment := &dto.RealtimeUsage{
		InputTokens:  7,
		OutputTokens: 3,
	}

	require.NoError(t, accumulateUsage(segment, total))
	require.Equal(t, 10, total.TotalTokens)
	require.Equal(t, 7, total.InputTokens)
	require.Equal(t, 3, total.OutputTokens)
}

func TestAccumulateUsageSumsDurationDetails(t *testing.T) {
	total := &dto.RealtimeUsage{}
	// 火山每轮 response.done 带两路计费时长；session.close 后还有一笔
	// total_tokens=0 但带残余时长的账，都要累计。
	require.NoError(t, accumulateUsage(&dto.RealtimeUsage{
		InputTokens:     100,
		DurationDetails: &dto.RealtimeDurationDetails{AudioDurationMS: 6000, VisionDurationMS: 4000},
	}, total))
	require.NoError(t, accumulateUsage(&dto.RealtimeUsage{
		DurationDetails: &dto.RealtimeDurationDetails{AudioDurationMS: 663},
	}, total))
	require.NotNil(t, total.DurationDetails)
	require.Equal(t, int64(6663), total.DurationDetails.AudioDurationMS)
	require.Equal(t, int64(4000), total.DurationDetails.VisionDurationMS)

	// 负值视为上游异常，不累计
	require.NoError(t, accumulateUsage(&dto.RealtimeUsage{
		DurationDetails: &dto.RealtimeDurationDetails{AudioDurationMS: -100, VisionDurationMS: -5},
	}, total))
	require.Equal(t, int64(6663), total.DurationDetails.AudioDurationMS)
	require.Equal(t, int64(4000), total.DurationDetails.VisionDurationMS)

	// 无 duration 的段落不影响已累计值
	require.NoError(t, accumulateUsage(&dto.RealtimeUsage{InputTokens: 1}, total))
	require.Equal(t, int64(6663), total.DurationDetails.AudioDurationMS)
}
