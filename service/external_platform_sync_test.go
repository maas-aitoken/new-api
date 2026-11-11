package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadExternalPlatformConfigsDisabledByDefault(t *testing.T) {
	t.Setenv(externalPlatformsConfigEnv, "")
	configs, err := LoadExternalPlatformConfigs()
	require.NoError(t, err)
	require.Empty(t, configs)
}

func TestLoadExternalPlatformConfigsSupportsPlatformWrapper(t *testing.T) {
	t.Setenv(externalPlatformsConfigEnv, `{"platforms":[{"name":" JP ","label":"Japan","base_url":"https://example.invalid/","admin_token":"test-token","enabled":true}]}`)
	configs, err := LoadExternalPlatformConfigs()
	require.NoError(t, err)
	require.Len(t, configs, 1)
	require.Equal(t, "jp", configs[0].Name)
	require.Equal(t, "https://example.invalid", configs[0].BaseURL)
	require.Equal(t, 1, configs[0].AdminUserID)
	require.Equal(t, 180, configs[0].TimeoutSeconds)
}

func TestLoadExternalPlatformConfigsSkipsDisabledEntries(t *testing.T) {
	t.Setenv(externalPlatformsConfigEnv, `[{"name":"disabled","base_url":"https://example.invalid","admin_token":"test-token","enabled":false}]`)
	configs, err := LoadExternalPlatformConfigs()
	require.NoError(t, err)
	require.Empty(t, configs)
}
