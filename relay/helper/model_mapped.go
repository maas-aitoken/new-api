package helper

import (
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

func ModelMappedHelper(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}

	// 支持链式模型重定向，最终使用链尾的模型。链的解析与容量额度共用
	// model.ResolveMappedModel，保证额度的键就是这里算出的上游模型名。
	upstream, mapped, err := model.ResolveMappedModel(c.GetString("model_mapping"), info.OriginModelName)
	if err != nil {
		return err
	}
	if mapped {
		info.IsModelMapped = true
		info.UpstreamModelName = upstream
	}

	if request != nil {
		request.SetModelName(info.UpstreamModelName)
	}
	return nil
}
