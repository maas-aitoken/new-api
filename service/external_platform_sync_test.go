package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
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

func TestFetchExternalQuotaDataAsksForEachAllowedUser(t *testing.T) {
	tests := []struct {
		name            string
		honorsUsername  bool
		wantRemoteUsers []string
	}{
		{name: "remote filters by username", honorsUsername: true, wantRemoteUsers: []string{"alice", "bob"}},
		{name: "remote ignores username", honorsUsername: false, wantRemoteUsers: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asked := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				username := r.URL.Query().Get("username")
				asked = append(asked, username)
				if !tt.honorsUsername {
					username = ""
				}
				fmt.Fprintf(w, `{"success":true,"data":[{"username":%q,"model_name":"model-a","created_at":3600,"count":2,"quota":100}]}`, username)
			}))
			defer server.Close()

			config := ExternalPlatformConfig{
				Name: "test", BaseURL: server.URL, AdminToken: "test-token", AdminUserID: 1, TimeoutSeconds: 5,
				AllowedUsers: []string{"alice@example.com", "Alice", "bob", " "},
				UserMap:      map[string]int{"alice": 11, "bob": 12},
			}
			rows, err := fetchExternalQuotaData(context.Background(), config, 0, 7200)
			require.NoError(t, err)

			assert.Equal(t, []string{"alice", "bob"}, asked)
			got := []string{}
			for _, row := range rows {
				got = append(got, row.RemoteUsername)
				assert.Equal(t, config.UserMap[row.RemoteUsername], row.LocalUserID)
				assert.Equal(t, int64(2), row.Count)
			}
			assert.Equal(t, tt.wantRemoteUsers, got)
		})
	}
}

func TestSyncExternalPlatformCountsOverlappingFlowOnce(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.ExternalQuotaData{}, &model.ExternalUsageSyncState{},
		&model.ExternalPeakTPM{}, &model.ExternalChannelCapability{}, &model.ExternalFlowData{}))
	t.Cleanup(func() {
		for _, table := range []string{"external_quota_data", "external_usage_sync_states", "external_peak_tpm",
			"external_channel_capabilities", "external_flow_data"} {
			model.DB.Exec("DELETE FROM " + table)
		}
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/capability-targets":
			fmt.Fprint(w, `{"success":true,"data":{"items":[]}}`)
		case "/api/data/flow":
			// One request per hour in the window.
			start, _ := strconv.ParseInt(r.URL.Query().Get("start_timestamp"), 10, 64)
			end, _ := strconv.ParseInt(r.URL.Query().Get("end_timestamp"), 10, 64)
			fmt.Fprintf(w, `{"success":true,"data":[{"username":"alice","model_name":"model-a","use_group":"default","count":%d}]}`, (end-start)/3600+1)
		default:
			fmt.Fprint(w, `{"success":true,"data":[]}`)
		}
	}))
	defer server.Close()

	now := time.Now().Unix()
	end := now - now%3600
	config := ExternalPlatformConfig{
		Name: "test", BaseURL: server.URL, AdminToken: "test-token", AdminUserID: 1, TimeoutSeconds: 5,
		InitialStartTimestamp: end - 7200, UserMap: map[string]int{"alice": 11},
	}
	for range 2 {
		_, _, _, err := syncExternalPlatform(context.Background(), config)
		require.NoError(t, err)
	}

	flow, err := model.GetExternalFlowData(end-7200, end+3599, 0, "test")
	require.NoError(t, err)
	require.Len(t, flow, 1)
	assert.Equal(t, 3, flow[0].Count)
	assert.Equal(t, 11, flow[0].UserID)
}

func TestLoadExternalPlatformConfigsSkipsDisabledEntries(t *testing.T) {
	t.Setenv(externalPlatformsConfigEnv, `[{"name":"disabled","base_url":"https://example.invalid","admin_token":"test-token","enabled":false}]`)
	configs, err := LoadExternalPlatformConfigs()
	require.NoError(t, err)
	require.Empty(t, configs)
}
