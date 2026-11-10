package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOptionValueRejectsInvalidMaxTokenAutoGroups(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "invalid"} {
		t.Run(value, func(t *testing.T) {
			assert.Error(t, validateOptionValue("MaxTokenAutoGroups", value))
		})
	}
	require.NoError(t, validateOptionValue("MaxTokenAutoGroups", "999999"))
}

// autorouter_setting.* 的值在加载时解析失败会被直接跳过，写入口（单项和批量都走这里）必须先拦住。
func TestValidateOptionValueChecksAutoRouterOptions(t *testing.T) {
	assert.Error(t, validateOptionValue("autorouter_setting.jev_timeout_ms", "50"))
	assert.Error(t, validateOptionValue("autorouter_setting.cost_weights", `{"cheap":1}`))
	assert.Error(t, validateOptionValue("autorouter_setting.jev_api_key", "bad key"))
	require.NoError(t, validateOptionValue("autorouter_setting.jev_timeout_ms", "5000"))
	require.NoError(t, validateOptionValue("autorouter_setting.cost_weights", `{"balanced":0.1}`))
}
