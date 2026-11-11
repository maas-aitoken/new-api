package common

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	ckdriver "github.com/ClickHouse/clickhouse-go/v2"
)

type consulKVEntry struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type consulDatabaseConfig struct {
	ClickHouse map[string]clickHouseConfig `json:"clickhouse"`
}

const (
	// Prefer this entry when the "clickhouse" object contains multiple configs.
	defaultConsulClickHouseConfigName = "cht_maas_log"
	consulKVMaxResponseBytes          = 1 << 20
	defaultConsulTimeoutSeconds       = 10
	// The only CONSUL_* setting the config key may set; it selects a ClickHouse
	// entry and is not needed to reach Consul.
	consulClickHouseConfigNameEnv = "CONSUL_CLICKHOUSE_CONFIG_KEY"
)

type clickHouseConfig struct {
	Database string `json:"database"`
	Host     string `json:"host"`
	Password string `json:"password"`
	Port     int    `json:"port"`
	User     string `json:"user"`
}

// LoadConsulConfig reads the startup configuration from Consul and returns the
// log database DSN. It must run before InitEnv.
//
// CONSUL_KV_CONFIG_PATH names a flat JSON key that overrides environment
// variables so every existing env reader prefers Consul; zero values ("", 0,
// false, null) keep the environment value. CONSUL_KV_DB_PATH names the
// ClickHouse log database key. Each path is optional and read with its own
// request. When Consul cannot supply the log database, LOG_SQL_DSN is used, and
// startup fails only if CONSUL_KV_DB_PATH is set and LOG_SQL_DSN is empty.
func LoadConsulConfig() (string, error) {
	configPath := strings.Trim(strings.TrimSpace(os.Getenv("CONSUL_KV_CONFIG_PATH")), "/")
	dbPath := strings.Trim(strings.TrimSpace(os.Getenv("CONSUL_KV_DB_PATH")), "/")
	if configPath == "" && dbPath == "" {
		return os.Getenv("LOG_SQL_DSN"), nil
	}

	logDSN, err := loadConsulStartupConfig(context.Background(), configPath, dbPath, pingConsulClickHouse)
	if err == nil {
		return logDSN, nil
	}
	SysError(LogText("failed to load Consul config, using environment variables: %s", err.Error()))
	if logDSN = os.Getenv("LOG_SQL_DSN"); logDSN != "" || dbPath == "" {
		return logDSN, nil
	}
	return "", fmt.Errorf("log database unavailable from Consul and LOG_SQL_DSN is empty: %w", err)
}

// loadConsulStartupConfig reads the config key first so it can set
// CONSUL_CLICKHOUSE_CONFIG_KEY and LOG_SQL_DSN, then the ClickHouse key. The
// config key uses CONSUL_KV_CONFIG_TOKEN when set, otherwise CONSUL_HTTP_TOKEN.
func loadConsulStartupConfig(ctx context.Context, configPath, dbPath string, probe func(context.Context, clickHouseConfig) error) (string, error) {
	addr := strings.TrimSpace(os.Getenv("CONSUL_HTTP_ADDR"))
	if addr == "" {
		return "", fmt.Errorf("CONSUL_KV_CONFIG_PATH and CONSUL_KV_DB_PATH require CONSUL_HTTP_ADDR")
	}
	token := strings.TrimSpace(os.Getenv("CONSUL_HTTP_TOKEN"))
	client := &http.Client{Timeout: defaultConsulTimeoutSeconds * time.Second}
	if configPath != "" {
		configToken := strings.TrimSpace(os.Getenv("CONSUL_KV_CONFIG_TOKEN"))
		if configToken == "" {
			configToken = token
		}
		// A config failure must not block the log database, so it is only logged.
		var payload []byte
		err := fmt.Errorf("CONSUL_KV_CONFIG_PATH requires CONSUL_KV_CONFIG_TOKEN or CONSUL_HTTP_TOKEN")
		if configToken != "" {
			payload, err = fetchConsulKV(ctx, client, addr, configToken, configPath)
		}
		if err == nil {
			err = applyConsulEnvConfig(payload)
		}
		if err != nil {
			SysError(LogText("failed to load Consul config, using environment variables: %s", err.Error()))
		}
	}
	if dbPath == "" {
		return os.Getenv("LOG_SQL_DSN"), nil
	}
	if token == "" {
		return "", fmt.Errorf("CONSUL_KV_DB_PATH requires CONSUL_HTTP_TOKEN")
	}
	payload, err := fetchConsulKV(ctx, client, addr, token, dbPath)
	if err != nil {
		return "", err
	}
	return parseClickHouseLogDSN(ctx, payload, probe)
}

// applyConsulEnvConfig overrides environment variables with a flat JSON object.
// Zero values keep the environment value; on any error nothing is changed.
func applyConsulEnvConfig(payload []byte) error {
	var raw map[string]RawMessage
	if err := Unmarshal(payload, &raw); err != nil {
		return fmt.Errorf("decode Consul config: %w", err)
	}
	values := make(map[string]string, len(raw))
	for key, value := range raw {
		// Consul connection settings can only come from the real environment.
		if key == "" || strings.ContainsAny(key, "=\x00") || (strings.HasPrefix(key, "CONSUL_") && key != consulClickHouseConfigNameEnv) {
			return fmt.Errorf("Consul config key %q is not allowed", key)
		}
		text := JsonRawMessageToString(value)
		switch GetJsonType(value) {
		case "null":
		case "string":
			if text != "" {
				values[key] = text
			}
		case "boolean":
			if text == "true" {
				values[key] = text
			}
		case "number":
			if number, err := strconv.ParseFloat(text, 64); err != nil || number != 0 {
				values[key] = text
			}
		default:
			return fmt.Errorf("Consul config key %q must be a string, number or boolean", key)
		}
	}
	for key, value := range values {
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s from Consul config: %w", key, err)
		}
	}
	SysLog(LogText("loaded %d settings from Consul KV", len(values)))
	return nil
}

// fetchConsulKV reads a single key and returns its base64-decoded value.
func fetchConsulKV(ctx context.Context, client *http.Client, addr, token, key string) ([]byte, error) {
	endpoint, err := consulKVURL(addr, key)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Consul request: %w", err)
	}
	if token != "" {
		req.Header.Set("X-Consul-Token", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Consul KV: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("Consul KV key %q was not found", key)
	case http.StatusForbidden, http.StatusUnauthorized:
		return nil, fmt.Errorf("Consul KV key %q denied (%s); check CONSUL_HTTP_TOKEN", key, resp.Status)
	default:
		return nil, fmt.Errorf("request Consul KV: unexpected status %s", resp.Status)
	}

	var entries []consulKVEntry
	if err := DecodeJson(io.LimitReader(resp.Body, consulKVMaxResponseBytes), &entries); err != nil {
		return nil, fmt.Errorf("decode Consul KV response: %w", err)
	}
	if len(entries) == 0 || entries[0].Value == "" {
		return nil, fmt.Errorf("Consul KV key %q is empty", key)
	}
	payload, err := base64.StdEncoding.DecodeString(entries[0].Value)
	if err != nil {
		return nil, fmt.Errorf("decode Consul KV value %q: %w", key, err)
	}
	return payload, nil
}

// FetchConsulKVValue reads and decodes one Consul KV value.
func FetchConsulKVValue(ctx context.Context, addr, token, kvPath string) ([]byte, error) {
	path := strings.Trim(kvPath, "/")
	endpoint, err := consulKVURL(addr, path)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Consul request: %w", err)
	}
	if token != "" {
		request.Header.Set("X-Consul-Token", token)
	}
	client := &http.Client{Timeout: defaultConsulTimeoutSeconds * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request Consul KV value: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Consul KV value %q returned %s", kvPath, response.Status)
	}
	var entries []consulKVEntry
	if err := DecodeJson(io.LimitReader(response.Body, consulKVMaxResponseBytes), &entries); err != nil {
		return nil, fmt.Errorf("decode Consul KV value %q: %w", path, err)
	}
	for _, entry := range entries {
		if entry.Key != path || entry.Value == "" {
			continue
		}
		value, decodeErr := base64.StdEncoding.DecodeString(entry.Value)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode Consul KV value %q: %w", path, decodeErr)
		}
		return value, nil
	}
	return nil, fmt.Errorf("Consul KV value %q was not found", path)
}

func parseClickHouseLogDSN(ctx context.Context, payload []byte, probe func(context.Context, clickHouseConfig) error) (string, error) {
	var config consulDatabaseConfig
	if err := Unmarshal(payload, &config); err != nil {
		return "", fmt.Errorf("decode ClickHouse configuration: %w", err)
	}
	name, clickHouse, err := selectClickHouseConfig(config.ClickHouse)
	if err != nil {
		return "", err
	}
	hosts := strings.Split(clickHouse.Host, ",")
	for i := range hosts {
		hosts[i] = strings.TrimSpace(hosts[i])
		if hosts[i] == "" {
			return "", fmt.Errorf("ClickHouse configuration %q is incomplete: empty host", name)
		}
	}
	clickHouse.Host = hosts[0]
	if clickHouse.Host == "" || clickHouse.User == "" || clickHouse.Password == "" || clickHouse.Database == "" || clickHouse.Port < 1 || clickHouse.Port > 65535 {
		return "", fmt.Errorf("ClickHouse configuration %q is incomplete", name)
	}

	clickHouse.Host = selectReachableClickHouseHost(ctx, hosts, clickHouse, probe)
	dsn := &url.URL{
		Scheme: "clickhouse",
		User:   url.UserPassword(clickHouse.User, clickHouse.Password),
		Host:   net.JoinHostPort(clickHouse.Host, strconv.Itoa(clickHouse.Port)),
		Path:   "/" + clickHouse.Database,
	}
	return dsn.String(), nil
}

func consulKVURL(addr, kvPath string) (string, error) {
	addr = strings.TrimSpace(addr)
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	endpoint, err := url.Parse(strings.TrimRight(addr, "/"))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return "", fmt.Errorf("invalid CONSUL_HTTP_ADDR %q: expected [http://|https://]host:port", addr)
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/kv/" + strings.TrimLeft(kvPath, "/")
	return endpoint.String(), nil
}

func selectClickHouseConfig(configs map[string]clickHouseConfig) (string, clickHouseConfig, error) {
	if len(configs) == 0 {
		return "", clickHouseConfig{}, fmt.Errorf("Consul KV value contains no clickhouse configuration")
	}
	if name := strings.TrimSpace(os.Getenv(consulClickHouseConfigNameEnv)); name != "" {
		config, ok := configs[name]
		if !ok {
			return "", clickHouseConfig{}, fmt.Errorf("Consul KV value contains no clickhouse configuration for CONSUL_CLICKHOUSE_CONFIG_KEY=%q", name)
		}
		return name, config, nil
	}
	if config, ok := configs[defaultConsulClickHouseConfigName]; ok {
		return defaultConsulClickHouseConfigName, config, nil
	}
	if len(configs) == 1 {
		for name, config := range configs {
			return name, config, nil
		}
	}
	return "", clickHouseConfig{}, fmt.Errorf("Consul KV value must contain %q when multiple clickhouse configurations exist", defaultConsulClickHouseConfigName)
}

// selectReachableClickHouseHost probes only multi-host configurations at startup.
// If none respond, retain the first host so normal database initialization handles failure.
func selectReachableClickHouseHost(ctx context.Context, hosts []string, config clickHouseConfig, probe func(context.Context, clickHouseConfig) error) string {
	if len(hosts) == 1 {
		return hosts[0]
	}
	for i, host := range hosts {
		if ctx.Err() != nil {
			break
		}
		config.Host = host
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := probe(probeCtx, config)
		cancel()
		if err == nil {
			SysLog(fmt.Sprintf("ClickHouse startup probe selected address %d/%d", i+1, len(hosts)))
			return host
		}
		SysLog(fmt.Sprintf("ClickHouse startup probe failed for address %d/%d: %v", i+1, len(hosts), err))
	}
	SysLog("ClickHouse startup probes failed; using first address for normal initialization")
	return hosts[0]
}

func pingConsulClickHouse(ctx context.Context, config clickHouseConfig) error {
	conn, err := ckdriver.Open(&ckdriver.Options{
		Addr:         []string{net.JoinHostPort(config.Host, strconv.Itoa(config.Port))},
		Auth:         ckdriver.Auth{Database: config.Database, Username: config.User, Password: config.Password},
		DialTimeout:  3 * time.Second,
		ReadTimeout:  3 * time.Second,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Ping(ctx)
}
