package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinishInputForCostMultiplierKeysByRequestModel pins where the
// <channel, model> cost multiplier recorded into cost_actual comes from: it is
// looked up by the request-side model name when the channel is selected
// (same side as billing), never by the upstream model name the relay ends up
// with after model mapping or after a streaming response reports its own model.
func TestFinishInputForCostMultiplierKeysByRequestModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		channelModels  string
		costMultiplier map[string]float64
		requestModel   string
		upstreamModel  string
		want           float64
	}{
		{
			name:           "aliases mapped to one upstream keep their own multiplier",
			channelModels:  "gpt-4o,gpt-4o-latest",
			costMultiplier: map[string]float64{"gpt-4o": 0.8, "gpt-4o-latest": 0.5, "gpt-4o-2024-08-06": 9},
			requestModel:   "gpt-4o-latest",
			upstreamModel:  "gpt-4o-2024-08-06",
			want:           0.5,
		},
		{
			name:           "upstream name reported by a streaming response does not matter",
			channelModels:  "claude-sonnet-4-5",
			costMultiplier: map[string]float64{"claude-sonnet-4-5": 0.6},
			requestModel:   "claude-sonnet-4-5",
			upstreamModel:  "claude-sonnet-4-5-20250929",
			want:           0.6,
		},
		{
			name:           "effort variant falls back to the channel model it matched",
			channelModels:  "gpt-5",
			costMultiplier: map[string]float64{"gpt-5": 0.7},
			requestModel:   "gpt-5-high",
			upstreamModel:  "gpt-5",
			want:           0.7,
		},
		{
			name:          "unconfigured multiplier records exactly what was billed",
			channelModels: "gpt-4o",
			requestModel:  "gpt-4o",
			upstreamModel: "gpt-4o",
			want:          1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &model.Channel{Id: 7, Models: tt.channelModels}
			channel.SetSetting(dto.ChannelSettings{CostMultiplierMap: tt.costMultiplier})
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, tt.requestModel))
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tt.upstreamModel},
			}

			in := finishInputFor(c, info, nil)

			assert.InDelta(t, tt.want, in.CostMultiplier, 0.000001)
			assert.Equal(t, tt.upstreamModel, in.UpstreamModelName)
		})
	}
}
