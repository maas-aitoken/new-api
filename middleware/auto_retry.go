package middleware

import (
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// patchAutoRoutedBody applies the selected model and reasoning effort to the
// stored request body, including channels that forward the body unchanged.
func patchAutoRoutedBody(c *gin.Context, result *autorouter.Result) error {
	protocol, _ := service.AutoRouterProtocol(c.Request.URL.Path)
	return rewriteJSONBody(c, func(body []byte) ([]byte, error) {
		return autorouter.PatchBody(body, protocol, result.ResolvedModel, result.EffortAction, result.AppliedEffort)
	})
}

// NextAutoRetryModel resolves the next model without starting another retry
// loop. The relay controller uses this callback only after a retryable failure.
func NextAutoRetryModel(c *gin.Context) (string, *types.NewAPIError) {
	value, exists := c.Get(ginKeyAutoRetryReroute)
	if !exists {
		return "", nil
	}
	reroute, _ := value.(func() (string, *types.NewAPIError))
	if reroute == nil {
		return "", nil
	}
	return reroute()
}
