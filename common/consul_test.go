package common

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noProbe(context.Context, clickHouseConfig) error { return nil }

// consulKeyResponse renders the response of a single-key GET.
func consulKeyResponse(value string) string {
	return fmt.Sprintf(`[{"Key":"k","Value":%q}]`, base64.StdEncoding.EncodeToString([]byte(value)))
}

func TestParseClickHouseLogDSN(t *testing.T) {
	for _, extra := range []string{"", `,"other":{"database":"other_logs","host":"127.0.0.2","password":"other-secret","port":9001,"user":"other"}`} {
		name := "single config"
		if extra != "" {
			name = "multiple configs"
		}
		t.Run(name, func(t *testing.T) {
			payload := `{"clickhouse":{"cht_maas_log":{"database":"app_logs","host":"192.0.2.20","password":"p@ss:/?#","port":9000,"user":"log_writer"}` + extra + `}}`
			dsn, err := parseClickHouseLogDSN(context.Background(), []byte(payload), noProbe)
			require.NoError(t, err)
			parsed, err := url.Parse(dsn)
			require.NoError(t, err)
			require.NotNil(t, parsed.User)
			password, _ := parsed.User.Password()
			assert.Equal(t, "clickhouse", parsed.Scheme)
			assert.Equal(t, "log_writer", parsed.User.Username())
			assert.Equal(t, "p@ss:/?#", password)
			assert.Equal(t, "192.0.2.20:9000", parsed.Host)
			assert.Equal(t, "/app_logs", parsed.Path)
		})
	}
}

func TestParseClickHouseLogDSNErrors(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "invalid json", payload: `not json`, want: "decode ClickHouse configuration"},
		{name: "missing config", payload: `{"clickhouse":{}}`, want: "contains no clickhouse configuration"},
		{name: "several configs without the default name", payload: `{"clickhouse":{"a":{"database":"logs","host":"localhost","password":"secret","port":9000,"user":"logger"},"b":{"database":"logs","host":"localhost","password":"secret","port":9000,"user":"logger"}}}`, want: `must contain "cht_maas_log" when multiple clickhouse configurations exist`},
		{name: "incomplete config", payload: `{"clickhouse":{"cht_maas_log":{"host":"localhost"}}}`, want: "is incomplete"},
		{name: "incomplete target with valid sibling", payload: `{"clickhouse":{"cht_maas_log":{},"other":{"database":"logs","host":"localhost","password":"secret","port":9000,"user":"logger"}}}`, want: `ClickHouse configuration "cht_maas_log" is incomplete`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseClickHouseLogDSN(context.Background(), []byte(tt.payload), noProbe)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

// The Consul KV payload is keyed by database name, which differs per
// environment, so the target entry is resolved rather than hardcoded.
func TestSelectClickHouseConfigResolution(t *testing.T) {
	sole := clickHouseConfig{Database: "sandbox_logs", Host: "192.0.2.30", Password: "s", Port: 9000, User: "u"}
	dflt := clickHouseConfig{Database: "app_logs", Host: "192.0.2.31", Password: "s", Port: 9000, User: "u"}

	t.Run("sole entry under any name", func(t *testing.T) {
		name, config, err := selectClickHouseConfig(map[string]clickHouseConfig{"sandbox_logs_key": sole})
		require.NoError(t, err)
		assert.Equal(t, "sandbox_logs_key", name)
		assert.Equal(t, sole, config)
	})

	t.Run("default name wins over siblings", func(t *testing.T) {
		name, config, err := selectClickHouseConfig(map[string]clickHouseConfig{"cht_maas_log": dflt, "other": sole})
		require.NoError(t, err)
		assert.Equal(t, "cht_maas_log", name)
		assert.Equal(t, dflt, config)
	})
}

// CONSUL_HTTP_ADDR is conventionally host:port, which url.Parse reads as a
// scheme unless a default is applied.
func TestConsulKVURL(t *testing.T) {
	for _, tt := range []struct {
		addr string
		want string
	}{
		{addr: "consul.example.internal:8500", want: "http://consul.example.internal:8500/v1/kv/database/example/"},
		{addr: "http://consul.example.internal:8500", want: "http://consul.example.internal:8500/v1/kv/database/example/"},
		{addr: "https://consul.example.internal:8501/", want: "https://consul.example.internal:8501/v1/kv/database/example/"},
		{addr: "127.0.0.1:8500", want: "http://127.0.0.1:8500/v1/kv/database/example/"},
	} {
		t.Run(tt.addr, func(t *testing.T) {
			got, err := consulKVURL(tt.addr, "database/example/")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	for _, addr := range []string{"", "ftp://consul:8500", "http://"} {
		t.Run("invalid "+addr, func(t *testing.T) {
			_, err := consulKVURL(addr, "key")
			require.ErrorContains(t, err, "invalid CONSUL_HTTP_ADDR")
		})
	}
}

// Each path is read with a plain GET of that exact key.
func TestFetchConsulKV(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/kv/app/example/config", r.URL.Path)
		assert.Empty(t, r.URL.RawQuery)
		assert.Equal(t, "test-token", r.Header.Get("X-Consul-Token"))
		fmt.Fprint(w, consulKeyResponse(`{"A":"1"}`))
	}))
	defer server.Close()

	value, err := fetchConsulKV(context.Background(), server.Client(), server.URL, "test-token", "app/example/config")
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"A":"1"}`), value)
}

func TestFetchConsulKVErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "missing key", status: http.StatusNotFound, want: `Consul KV key "k" was not found`},
		{name: "denied", status: http.StatusForbidden, want: "check CONSUL_HTTP_TOKEN"},
		{name: "server error", status: http.StatusInternalServerError, want: "unexpected status"},
		{name: "invalid response", body: `{`, want: "decode Consul KV response"},
		{name: "empty value", body: `[{"Key":"k","Value":null}]`, want: `Consul KV key "k" is empty`},
		{name: "invalid base64", body: `[{"Key":"k","Value":"%%%"}]`, want: `decode Consul KV value "k"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.status != 0 {
					w.WriteHeader(tt.status)
					return
				}
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			_, err := fetchConsulKV(context.Background(), server.Client(), server.URL, "token", "k")
			require.ErrorContains(t, err, tt.want)
		})
	}
}

// The HTTP helper accepts host:port and omits empty token headers; the public
// loader still requires both address and token before using Consul.
func TestFetchConsulKVWithoutToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasToken := r.Header["X-Consul-Token"]
		assert.False(t, hasToken, "no empty token header should be sent")
		fmt.Fprint(w, consulKeyResponse("{}"))
	}))
	defer server.Close()

	value, err := fetchConsulKV(context.Background(), server.Client(), strings.TrimPrefix(server.URL, "http://"), "", "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("{}"), value)
}

func TestParseClickHouseLogDSNHostList(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{"single", "192.0.2.20", "192.0.2.20:9000"},
		{"multiple", "192.0.2.10,192.0.2.11,192.0.2.12", "192.0.2.10:9000"},
		{"whitespace", " 192.0.2.10 , 192.0.2.11 ", "192.0.2.10:9000"},
		{"ipv6", "2001:db8::1,2001:db8::2", "[2001:db8::1]:9000"},
		{"empty first", ",192.0.2.11", ""},
		{"blank first", "  ,192.0.2.11", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"clickhouse":{"cht_maas_log":{"database":"logs","host":%q,"password":"secret","port":9000,"user":"logger"}}}`, tt.host)
			dsn, err := parseClickHouseLogDSN(context.Background(), []byte(payload), noProbe)
			if tt.want == "" {
				require.ErrorContains(t, err, "is incomplete")
				return
			}
			require.NoError(t, err)
			parsed, err := url.Parse(dsn)
			require.NoError(t, err)
			assert.Equal(t, tt.want, parsed.Host)
		})
	}
}

func TestParseClickHouseLogDSNConfiguredKey(t *testing.T) {
	for _, key := range []string{"prod_logs_key", " prod_logs_key ", "missing"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("CONSUL_CLICKHOUSE_CONFIG_KEY", key)
			payload := `{"clickhouse":{"prod_logs_key":{"database":"prod_logs","host":"192.0.2.10,192.0.2.11","password":"secret","port":9000,"user":"logger"},"cht_maas_log":{"database":"test_logs","host":"localhost","password":"secret","port":9000,"user":"logger"}}}`
			dsn, err := parseClickHouseLogDSN(context.Background(), []byte(payload), noProbe)
			if key == "missing" {
				require.ErrorContains(t, err, "CONSUL_CLICKHOUSE_CONFIG_KEY")
				return
			}
			require.NoError(t, err)
			parsed, err := url.Parse(dsn)
			require.NoError(t, err)
			assert.Equal(t, "192.0.2.10:9000", parsed.Host)
			assert.Equal(t, "/prod_logs", parsed.Path)
		})
	}
}

func TestSelectClickHouseConfigBlankKey(t *testing.T) {
	t.Setenv("CONSUL_CLICKHOUSE_CONFIG_KEY", "  ")
	name, _, err := selectClickHouseConfig(map[string]clickHouseConfig{"prod_logs_key": {}})
	require.NoError(t, err)
	assert.Equal(t, "prod_logs_key", name)
}

// ckdriver.OpenDB rejects pool limits passed through Options and returns a
// *sql.DB that fails without dialing, which made every startup probe fail
// instantly regardless of host reachability.
func TestPingConsulClickHouseDials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	dialed := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case dialed <- struct{}{}:
			default:
			}
			conn.Close()
		}
	}()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	portNumber, err := strconv.Atoi(port)
	require.NoError(t, err)

	err = pingConsulClickHouse(context.Background(), clickHouseConfig{
		Database: "logs", Host: host, Password: "secret", Port: portNumber, User: "logger",
	})
	// The listener hangs up during the handshake, so the ping must fail, but it
	// must fail after reaching the address instead of on settings validation.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "invalid settings")

	select {
	case <-dialed:
	case <-time.After(5 * time.Second):
		t.Fatal("probe never connected to the configured address")
	}
}

func TestSelectReachableClickHouseHost(t *testing.T) {
	for _, tt := range []struct {
		name      string
		hosts     []string
		reachable string
		want      string
		calls     int
	}{
		{"single skips probe", []string{"192.0.2.1"}, "", "192.0.2.1", 0},
		{"first alive", []string{"192.0.2.1", "192.0.2.2"}, "192.0.2.1", "192.0.2.1", 1},
		{"second alive", []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"}, "192.0.2.2", "192.0.2.2", 2},
		{"all fail", []string{"192.0.2.1", "192.0.2.2"}, "", "192.0.2.1", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			config := clickHouseConfig{Database: "logs", User: "u", Password: "p", Port: 9000}
			got := selectReachableClickHouseHost(context.Background(), tt.hosts, config, func(ctx context.Context, c clickHouseConfig) error {
				require.Equal(t, tt.hosts[calls], c.Host)
				calls++
				_, ok := ctx.Deadline()
				require.True(t, ok)
				assert.Equal(t, config.Password, c.Password)
				assert.Equal(t, config.Database, c.Database)
				if c.Host == tt.reachable {
					return nil
				}
				return fmt.Errorf("unavailable")
			})
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.calls, calls)
		})
	}
}

// Zero values in Consul keep the environment value; the string "false" or "0"
// is the way to override a non-zero environment value with a zero.
func TestApplyConsulEnvConfig(t *testing.T) {
	env := map[string]string{
		"SQL_DSN":              "postgres://env",
		"REDIS_POOL_SIZE":      "10",
		"RELAY_TIMEOUT":        "30",
		"DEBUG":                "false",
		"MEMORY_CACHE_ENABLED": "true",
		"FORCE_STREAM_OPTION":  "true",
		"SQLITE_PATH":          "/env.db",
		"SESSION_SECRET":       "env-secret",
		"ONLY_IN_ENV":          "kept",
	}
	for key, value := range env {
		t.Setenv(key, value)
	}

	payload := `{"SQL_DSN":"postgres://consul","REDIS_POOL_SIZE":20,"RELAY_TIMEOUT":0,"DEBUG":true,"MEMORY_CACHE_ENABLED":false,"FORCE_STREAM_OPTION":"false","SQLITE_PATH":"","SESSION_SECRET":null}`
	require.NoError(t, applyConsulEnvConfig([]byte(payload)))

	for key, want := range map[string]string{
		"SQL_DSN":              "postgres://consul",
		"REDIS_POOL_SIZE":      "20",
		"RELAY_TIMEOUT":        "30",
		"DEBUG":                "true",
		"MEMORY_CACHE_ENABLED": "true",
		"FORCE_STREAM_OPTION":  "false",
		"SQLITE_PATH":          "/env.db",
		"SESSION_SECRET":       "env-secret",
		"ONLY_IN_ENV":          "kept",
	} {
		assert.Equal(t, want, os.Getenv(key), key)
	}
}

func TestApplyConsulEnvConfigKeepsEnvOnError(t *testing.T) {
	for _, tt := range []struct {
		name    string
		payload string
		want    string
	}{
		{name: "not an object", payload: `["SQL_DSN"]`, want: "decode Consul config"},
		{name: "nested value", payload: `{"SQL_DSN":"postgres://consul","RELAY_TIMEOUT":{"v":1}}`, want: `"RELAY_TIMEOUT" must be a string, number or boolean`},
		{name: "connection key", payload: `{"SQL_DSN":"postgres://consul","CONSUL_HTTP_TOKEN":"other"}`, want: `"CONSUL_HTTP_TOKEN" is not allowed`},
		{name: "path key", payload: `{"SQL_DSN":"postgres://consul","CONSUL_KV_DB_PATH":"other"}`, want: `"CONSUL_KV_DB_PATH" is not allowed`},
		{name: "invalid key", payload: `{"SQL_DSN":"postgres://consul","A=B":"x"}`, want: `"A=B" is not allowed`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SQL_DSN", "postgres://env")
			t.Setenv("CONSUL_HTTP_TOKEN", "token")
			require.ErrorContains(t, applyConsulEnvConfig([]byte(tt.payload)), tt.want)
			assert.Equal(t, "postgres://env", os.Getenv("SQL_DSN"))
			assert.Equal(t, "token", os.Getenv("CONSUL_HTTP_TOKEN"))
		})
	}
}

func TestLoadConsulConfig(t *testing.T) {
	const configKey = "app/example/config"
	const dbKey = "database/example/clickhouse/maas_logs"
	const ckPayload = `{"clickhouse":{"cht_maas_log":{"database":"logs","host":"127.0.0.1","password":"secret","port":9000,"user":"logger"}}}`
	const ckDSN = "clickhouse://logger:secret@127.0.0.1:9000/logs"
	bothKeys := []string{"/v1/kv/" + configKey, "/v1/kv/" + dbKey}

	for _, tt := range []struct {
		name         string
		configPath   string
		dbPath       string
		status       int
		values       map[string]string
		token        string
		envLogDSN    string
		wantDSN      string
		wantErr      string
		wantSQLDSN   string
		wantRequests []string
	}{
		{
			name:         "config then clickhouse in two requests",
			configPath:   configKey,
			dbPath:       dbKey,
			values:       map[string]string{configKey: `{"SQL_DSN":"postgres://consul"}`, dbKey: ckPayload},
			envLogDSN:    "env-log",
			wantDSN:      ckDSN,
			wantSQLDSN:   "postgres://consul",
			wantRequests: bothKeys,
		},
		{
			name:         "LOG_SQL_DSN from config when clickhouse key is missing",
			configPath:   configKey,
			dbPath:       dbKey,
			values:       map[string]string{configKey: `{"LOG_SQL_DSN":"consul-log"}`},
			envLogDSN:    "env-log",
			wantDSN:      "consul-log",
			wantSQLDSN:   "postgres://env",
			wantRequests: bothKeys,
		},
		{
			name:       "no log database anywhere",
			configPath: configKey,
			dbPath:     dbKey,
			values:     map[string]string{configKey: `{"SQL_DSN":"postgres://consul"}`},
			wantErr:    `Consul KV key "database/example/clickhouse/maas_logs" was not found`,
		},
		{
			name:         "invalid config keeps env but still loads clickhouse",
			configPath:   configKey,
			dbPath:       dbKey,
			values:       map[string]string{configKey: `{"SQL_DSN":"postgres://consul","X":[1]}`, dbKey: ckPayload},
			wantDSN:      ckDSN,
			wantSQLDSN:   "postgres://env",
			wantRequests: bothKeys,
		},
		{
			name:         "missing config key still loads clickhouse",
			configPath:   configKey,
			dbPath:       dbKey,
			values:       map[string]string{dbKey: ckPayload},
			wantDSN:      ckDSN,
			wantSQLDSN:   "postgres://env",
			wantRequests: bothKeys,
		},
		{
			name:         "invalid clickhouse falls back to LOG_SQL_DSN",
			configPath:   configKey,
			dbPath:       dbKey,
			values:       map[string]string{dbKey: `{"clickhouse":{}}`},
			envLogDSN:    "env-log",
			wantDSN:      "env-log",
			wantSQLDSN:   "postgres://env",
			wantRequests: bothKeys,
		},
		{
			name:         "Consul error falls back to LOG_SQL_DSN",
			configPath:   configKey,
			dbPath:       dbKey,
			status:       http.StatusInternalServerError,
			envLogDSN:    "env-log",
			wantDSN:      "env-log",
			wantSQLDSN:   "postgres://env",
			wantRequests: bothKeys,
		},
		{
			name:       "Consul error without LOG_SQL_DSN fails startup",
			configPath: configKey,
			dbPath:     dbKey,
			status:     http.StatusInternalServerError,
			wantErr:    "LOG_SQL_DSN is empty",
		},
		{
			name:       "missing token falls back to LOG_SQL_DSN",
			configPath: configKey,
			dbPath:     dbKey,
			token:      " ",
			envLogDSN:  "env-log",
			wantDSN:    "env-log",
			wantSQLDSN: "postgres://env",
		},
		{
			name:         "only CONSUL_KV_DB_PATH reads only clickhouse",
			dbPath:       dbKey,
			values:       map[string]string{dbKey: ckPayload},
			wantDSN:      ckDSN,
			wantSQLDSN:   "postgres://env",
			wantRequests: []string{"/v1/kv/" + dbKey},
		},
		{
			name:         "only CONSUL_KV_CONFIG_PATH uses LOG_SQL_DSN from config",
			configPath:   configKey,
			values:       map[string]string{configKey: `{"LOG_SQL_DSN":"consul-log"}`},
			envLogDSN:    "env-log",
			wantDSN:      "consul-log",
			wantSQLDSN:   "postgres://env",
			wantRequests: []string{"/v1/kv/" + configKey},
		},
		{
			name:         "only CONSUL_KV_CONFIG_PATH without LOG_SQL_DSN uses the main database",
			configPath:   configKey,
			status:       http.StatusInternalServerError,
			wantDSN:      "",
			wantSQLDSN:   "postgres://env",
			wantRequests: []string{"/v1/kv/" + configKey},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				if tt.status != 0 {
					w.WriteHeader(tt.status)
					return
				}
				value, ok := tt.values[strings.TrimPrefix(r.URL.Path, "/v1/kv/")]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				fmt.Fprint(w, consulKeyResponse(value))
			}))
			defer server.Close()

			token := "token"
			if tt.token != "" {
				token = tt.token
			}
			t.Setenv("CONSUL_HTTP_ADDR", server.URL)
			t.Setenv("CONSUL_HTTP_TOKEN", token)
			t.Setenv("CONSUL_KV_CONFIG_PATH", "")
			t.Setenv("CONSUL_KV_DB_PATH", "")
			if tt.configPath != "" {
				t.Setenv("CONSUL_KV_CONFIG_PATH", "/"+tt.configPath+"/")
			}
			if tt.dbPath != "" {
				t.Setenv("CONSUL_KV_DB_PATH", "/"+tt.dbPath+"/")
			}
			t.Setenv("LOG_SQL_DSN", tt.envLogDSN)
			t.Setenv("SQL_DSN", "postgres://env")

			dsn, err := LoadConsulConfig()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantDSN, dsn)
			assert.Equal(t, tt.wantSQLDSN, os.Getenv("SQL_DSN"))
			assert.Equal(t, tt.wantRequests, requests)
		})
	}
}

// CONSUL_CLICKHOUSE_CONFIG_KEY may come from the config key, which is read
// before the ClickHouse key; an empty value there keeps the environment value.
func TestLoadConsulConfigClickHouseConfigKey(t *testing.T) {
	const ckPayload = `{"clickhouse":{"prod_logs_key":{"database":"prod_logs","host":"127.0.0.1","password":"secret","port":9000,"user":"logger"},"cht_maas_log":{"database":"test_logs","host":"127.0.0.1","password":"secret","port":9000,"user":"logger"}}}`

	for _, tt := range []struct {
		name   string
		config string
		env    string
		want   string
	}{
		{name: "from Consul config", config: `{"CONSUL_CLICKHOUSE_CONFIG_KEY":"prod_logs_key"}`, env: "cht_maas_log", want: "/prod_logs"},
		{name: "empty in Consul uses env", config: `{"CONSUL_CLICKHOUSE_CONFIG_KEY":""}`, env: "prod_logs_key", want: "/prod_logs"},
		{name: "unset everywhere uses default name", config: `{}`, want: "/test_logs"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				value := ckPayload
				if r.URL.Path == "/v1/kv/app/example/config" {
					value = tt.config
				}
				fmt.Fprint(w, consulKeyResponse(value))
			}))
			defer server.Close()

			t.Setenv("CONSUL_HTTP_ADDR", server.URL)
			t.Setenv("CONSUL_HTTP_TOKEN", "token")
			t.Setenv("CONSUL_KV_CONFIG_PATH", "app/example/config")
			t.Setenv("CONSUL_KV_DB_PATH", "database/example/clickhouse/maas_logs")
			t.Setenv("CONSUL_CLICKHOUSE_CONFIG_KEY", tt.env)

			dsn, err := LoadConsulConfig()
			require.NoError(t, err)
			parsed, err := url.Parse(dsn)
			require.NoError(t, err)
			assert.Equal(t, tt.want, parsed.Path)
		})
	}
}

func TestLoadConsulConfigWithoutPaths(t *testing.T) {
	t.Setenv("CONSUL_KV_CONFIG_PATH", " / ")
	t.Setenv("CONSUL_KV_DB_PATH", "")
	t.Setenv("CONSUL_HTTP_ADDR", "")
	t.Setenv("LOG_SQL_DSN", "env-log")

	dsn, err := LoadConsulConfig()
	require.NoError(t, err)
	assert.Equal(t, "env-log", dsn)
}
