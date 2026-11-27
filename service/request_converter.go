package service

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ginContextKey carries the real *gin.Context through relayconvert's
// context.Context parameter. relayconvert wraps that context with its own
// context.WithValue (request-scoped conversion diagnostics), which changes
// its dynamic type, so a direct ctx.(*gin.Context) assertion on the wrapped
// context fails and silently yields a nil *gin.Context. Looking the gin
// context up by this key instead survives any number of such wrappers.
type ginContextKey struct{}

// withGinContext embeds the gin context, if any, so it can be recovered
// later regardless of how many context.WithValue layers relayconvert adds.
func withGinContext(c *gin.Context) context.Context {
	if c == nil {
		return context.Background()
	}
	return context.WithValue(c, ginContextKey{}, c)
}

func init() {
	relayconvert.SetMediaResolver(relayconvert.MediaResolver{
		// relayconvert is gin-free; recover the gin context embedded by
		// withGinContext so file caching/cleanup keeps working.
		GetBase64Data: func(ctx context.Context, source types.FileSource, reason ...string) (string, string, error) {
			ginCtx, ok := ctx.Value(ginContextKey{}).(*gin.Context)
			if !ok {
				logger.LogDebug(ctx, "request_converter: no gin context available while resolving file source '%s'; file caching/cleanup will be skipped", source.GetIdentifier())
			}
			return GetBase64Data(ginCtx, source, reason...)
		},
		DecodeBase64FileData: DecodeBase64FileData,
	})
}

func ConvertRequest(c *gin.Context, info *relaycommon.RelayInfo, target types.RelayFormat, request any) (*relayconvert.RequestResult, error) {
	result, err := relayconvert.ConvertRequest(withGinContext(c), info, target, request)
	if result != nil {
		info.RecordConversionDiagnostics(c, result.Diagnostics)
	}
	return result, err
}

func ConvertRequestByID(c *gin.Context, info *relaycommon.RelayInfo, converter string, request any) (*relayconvert.RequestResult, error) {
	result, err := relayconvert.ConvertRequestByID(withGinContext(c), info, converter, request)
	if result != nil {
		info.RecordConversionDiagnostics(c, result.Diagnostics)
	}
	return result, err
}

func ConvertRequestVia(c *gin.Context, info *relaycommon.RelayInfo, request any, path ...types.RelayFormat) (*relayconvert.RequestResult, error) {
	result, err := relayconvert.ConvertRequestVia(withGinContext(c), info, request, path...)
	if result != nil {
		info.RecordConversionDiagnostics(c, result.Diagnostics)
	}
	return result, err
}

func ClaudeToOpenAIRequest(claudeRequest dto.ClaudeRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	result, err := ConvertRequest(nil, info, types.RelayFormatOpenAI, &claudeRequest)
	if err != nil {
		return nil, err
	}
	openAIRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
	if !ok {
		return nil, fmt.Errorf("expected OpenAI chat completions request, got %T", result.Value)
	}
	return openAIRequest, nil
}

func GeminiToOpenAIRequest(geminiRequest *dto.GeminiChatRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	result, err := ConvertRequest(nil, info, types.RelayFormatOpenAI, geminiRequest)
	if err != nil {
		return nil, err
	}
	openAIRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
	if !ok {
		return nil, fmt.Errorf("expected OpenAI chat completions request, got %T", result.Value)
	}
	return openAIRequest, nil
}
