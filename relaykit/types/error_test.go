package types

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHideErrMsgPreservesCauseWithoutExposingIt(t *testing.T) {
	origin := fmt.Errorf("Post \"https://private.example/v1/chat?token=secret\": %w", context.Canceled)
	apiErr := NewError(origin, ErrorCodeDoRequestFailed, ErrOptionWithHideErrMsg("upstream error: do request failed"))

	assert.Equal(t, "upstream error: do request failed", apiErr.Error())
	assert.NotContains(t, apiErr.ToOpenAIError().Message, "private.example")
	assert.NotContains(t, apiErr.ToOpenAIError().Message, "secret")
	require.ErrorIs(t, apiErr, context.Canceled)
}
