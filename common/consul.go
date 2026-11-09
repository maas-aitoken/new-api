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
	// Keys read under CONSUL_KV_PREFIX (e.g. database/autom/maas).
	consulConfigKey     = "config"
	consulClickHouseKey = "clickhouse/maas_logs"
	// The only CONSUL_* setting <prefix>/config may set; it selects a ClickHouse
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

// LoadConsulConfig loads the startup configuration under CONSUL_KV_PREFIX in a
// single request and returns the log database DSN. It must run before InitEnv.
//
// <prefix>/config overrides environment variables so every existing env reader
// prefers Consul; zero values ("", 0, false, null) keep the environment value.
// <prefix>/clickhouse provides the log database. When Consul cannot supply the
// log database, LOG_SQL_DSN is used, and startup fails only if it is also empty.
func LoadConsulConfig() (string, error) {
	prefix := strings.Trim(strings.TrimSpace(os.Getenv("CONSUL_KV_PREFIX")), "/")
	if prefix == "" {
		if strings.TrimSpace(os.Getenv("CONSUL_KV_DB_PATH")) != "" {
			return "", fmt.Errorf("CONSUL_KV_DB_PATH is no longer supported; store the ClickHouse config at <CONSUL_KV_PREFIX>/%s and set CONSUL_KV_PREFIX", consulClickHouseKey)
		}
		return os.Getenv("LOG_SQL_DSN"), nil
	}

	logDSN, err := loadConsulStartupConfig(context.Background(), prefix, pingConsulClickHouse)
	if err == nil {
		return logDSN, nil
	}
	SysError(LogText("failed to load Consul config, using environment variables: %s", err.Error()))
	if logDSN = os.Getenv("LOG_SQL_DSN"); logDSN != "" {
		return logDSN, nil
	}
	return "", fmt.Errorf("log database unavailable from Consul and LOG_SQL_DSN is empty: %w", err)
}

func loadConsulStartupConfig(ctx context.Context, prefix string, probe func(context.Context, clickHouseConfig) error) (string, error) {
	addr := strings.TrimSpace(os.Getenv("CONSUL_HTTP_ADDR"))
	token := strings.TrimSpace(os.Getenv("CONSUL_HTTP_TOKEN"))
	if addr == "" || token == "" {
		return "", fmt.Errorf("CONSUL_KV_PREFIX requires CONSUL_HTTP_ADDR and CONSUL_HTTP_TOKEN")
	}
	client := &http.Client{Timeout: defaultConsulTimeoutSeconds * time.Second}
	values, err := fetchConsulKVPrefix(ctx, client, addr, token, prefix)
	if err != nil {
		return "", err
	}
	if payload, ok := values[consulConfigKey]; ok {
		// A bad config key must not block the log database, so it is only logged.
		if err := applyConsulEnvConfig(payload); err != nil {
			SysError(LogText("failed to load Consul config, using environment variables: %s", err.Error()))
		}
	}
	payload, ok := values[consulClickHouseKey]
	if !ok {
		return "", fmt.Errorf("Consul KV key %q was not found", prefix+"/"+consulClickHouseKey)
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

// fetchConsulKVPrefix reads every key under prefix in one request and returns
// the base64-decoded values keyed by their path relative to prefix.
func fetchConsulKVPrefix(ctx context.Context, client *http.Client, addr, token, prefix string) (map[string][]byte, error) {
	endpoint, err := consulKVURL(addr, prefix+"/")
	if err != nil {
		return nil, err
	}
	endpoint += "?recurse=true"

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
		return nil, fmt.Errorf("Consul KV prefix %q has no keys", prefix)
	case http.StatusForbidden, http.StatusUnauthorized:
		return nil, fmt.Errorf("Consul KV prefix %q denied (%s); check CONSUL_HTTP_TOKEN", prefix, resp.Status)
	default:
		return nil, fmt.Errorf("request Consul KV: unexpected status %s", resp.Status)
	}

	var entries []consulKVEntry
	if err := DecodeJson(io.LimitReader(resp.Body, consulKVMaxResponseBytes), &entries); err != nil {
		return nil, fmt.Errorf("decode Consul KV response: %w", err)
	}
	values := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		key, ok := strings.CutPrefix(entry.Key, prefix+"/")
		// Folder entries and empty values carry no configuration.
		if !ok || key == "" || entry.Value == "" {
			continue
		}
		payload, err := base64.StdEncoding.DecodeString(entry.Value)
		if err != nil {
			return nil, fmt.Errorf("decode Consul KV value %q: %w", entry.Key, err)
		}
		values[key] = payload
	}
	return values, nil
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
