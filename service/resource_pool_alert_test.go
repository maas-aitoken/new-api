package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWeComWebhook = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=bot-key-12345678"

// setupResourcePoolAlertTest gives the test an empty alert setting backed by
// the options table and captures every message sent to the WeCom group bot.
// failNext makes the bot reject that many following messages.
func setupResourcePoolAlertTest(t *testing.T) (sent *[]weComGroupBotMessage, failNext *atomic.Int32) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.Option{}, &model.ResourcePoolSource{}))

	stored := config.GlobalConfig.Get("resource_pool_alert").(*operation_setting.ResourcePoolAlertSetting)
	common.OptionMapRWMutex.Lock()
	previousSetting, previousOptions := *stored, common.OptionMap
	common.OptionMap = map[string]string{}
	*stored = operation_setting.ResourcePoolAlertSetting{}
	common.OptionMapRWMutex.Unlock()
	previousClient, previousLanguage, previousName := weComGroupBotHTTPClient, common.DefaultLanguage, common.SystemName
	common.DefaultLanguage, common.SystemName = "zh-CN", "MaaS"

	sent, failNext = &[]weComGroupBotMessage{}, &atomic.Int32{}
	weComGroupBotHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, testWeComWebhook, req.URL.String())
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			var message weComGroupBotMessage
			require.NoError(t, common.Unmarshal(body, &message))
			reply := `{"errcode":0,"errmsg":"ok"}`
			if failNext.Load() > 0 {
				failNext.Add(-1)
				reply = `{"errcode":93000,"errmsg":"invalid webhook url"}`
			} else {
				*sent = append(*sent, message)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(reply))}, nil
		})}
	}
	t.Cleanup(func() {
		require.NoError(t, model.DB.Exec("DELETE FROM options").Error)
		common.OptionMapRWMutex.Lock()
		*stored, common.OptionMap = previousSetting, previousOptions
		common.OptionMapRWMutex.Unlock()
		weComGroupBotHTTPClient = previousClient
		common.DefaultLanguage, common.SystemName = previousLanguage, previousName
	})
	return sent, failNext
}

// seedResourcePoolAlertSource points one provider at an upstream whose reply
// the test can swap between checks.
func seedResourcePoolAlertSource(t *testing.T, provider string, name string) *atomic.Value {
	t.Helper()
	reply := &atomic.Value{}
	reply.Store(`{"accounts":[]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := reply.Load().(string)
		if body == "" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	require.NoError(t, model.SaveResourcePoolSources(provider, []*model.ResourcePoolSource{
		{Name: name, Endpoint: server.URL, Enabled: true},
	}))
	t.Cleanup(func() {
		require.NoError(t, model.SaveResourcePoolSources(provider, nil))
		InvalidateResourcePoolUsage(provider)
	})
	return reply
}

func alertRules(enabled map[string]float64) map[string]operation_setting.ResourcePoolAlertRule {
	rules := make(map[string]operation_setting.ResourcePoolAlertRule)
	for _, provider := range model.ResourcePoolProviders {
		threshold, ok := enabled[provider]
		if !ok {
			threshold = operation_setting.DefaultResourcePoolAlertThreshold
		}
		rules[provider] = operation_setting.ResourcePoolAlertRule{Enabled: ok, Threshold: threshold}
	}
	return rules
}

// Claude is judged as a whole and per account group on used / limit credits,
// and per source only while channel monitoring is switched on. The first
// check above the threshold notifies once, later checks stay silent, and
// dropping back below it sends one recovery notice.
func TestCheckResourcePoolWaterLevelsNotifiesOnceAndOnRecovery(t *testing.T) {
	sent, failNext := setupResourcePoolAlertTest(t)
	upstream := seedResourcePoolAlertSource(t, model.ResourcePoolClaude, "anthropic1")
	require.NoError(t, SaveResourcePoolAlertConfig(ResourcePoolAlertConfig{
		Enabled: true, WebhookURL: testWeComWebhook, Mentions: "13800000000, zhangsan,@all",
		NotifyStart: "09:00", NotifyEnd: "21:00",
		Rules: alertRules(map[string]float64{model.ResourcePoolClaude: 80}),
	}))
	high := `{"accounts":[
		{"id":"a1","enabled":true,"groups":["aws1"],"usage_current":900,"usage_limit":1000},
		{"id":"a2","enabled":true,"groups":["aws2"],"usage_current":800,"usage_limit":1000},
		{"id":"a3","enabled":true,"groups":["aws2"],"usage_current":400,"usage_limit":1000}
	]}`
	upstream.Store(high)

	// A rejected send leaves the state unsaved, so the next check retries.
	failNext.Store(1)
	_, err := CheckResourcePoolWaterLevels(context.Background())
	require.ErrorContains(t, err, "errcode 93000")
	assert.NotContains(t, err.Error(), "bot-key")
	assert.Empty(t, GetResourcePoolAlertConfig().Alerting)

	_, err = CheckResourcePoolWaterLevels(context.Background())
	require.NoError(t, err)
	require.Len(t, *sent, 1)
	// aws1 is 900/1000 = 90%; aws2 is 1200/2000 = 60%; the whole pool is 2100/3000 = 70%.
	lines := strings.Split((*sent)[0].Text.Content, "\n")
	require.Len(t, lines, 2)
	assert.True(t, strings.HasPrefix(lines[0], "【资源池水位告警】MaaS 20"), lines[0])
	assert.Equal(t, "[告警] Claude 分组 aws1 水位 90.0%，已达到阈值 80%", lines[1])
	assert.Equal(t, []string{"zhangsan", "@all"}, (*sent)[0].Text.MentionedList)
	assert.Equal(t, []string{"13800000000"}, (*sent)[0].Text.MentionedMobileList)
	alerting := GetResourcePoolAlertConfig().Alerting
	require.Len(t, alerting, 1)
	assert.Equal(t, "aws1", alerting[0].Name)

	// Still above the threshold: silent. An upstream outage keeps the state.
	_, err = CheckResourcePoolWaterLevels(context.Background())
	require.NoError(t, err)
	upstream.Store("")
	_, err = CheckResourcePoolWaterLevels(context.Background())
	require.NoError(t, err)
	assert.Len(t, *sent, 1)
	assert.Len(t, GetResourcePoolAlertConfig().Alerting, 1)

	// The whole pool rises above the threshold while aws1 recovers. The
	// source is at the same level, but channels are not monitored by default.
	upstream.Store(`{"accounts":[
		{"id":"a1","enabled":true,"groups":["aws1"],"usage_current":100,"usage_limit":1000},
		{"id":"a2","enabled":true,"groups":["aws2"],"usage_current":1000,"usage_limit":1000},
		{"id":"a3","enabled":false,"groups":["aws2"]}
	]}`)
	_, err = CheckResourcePoolWaterLevels(context.Background())
	require.NoError(t, err)
	require.Len(t, *sent, 2)
	// A disabled account without a quota counts as 10000/10000 used up.
	assert.Equal(t, []string{
		"[告警] Claude 分组 aws2 水位 100.0%，已达到阈值 80%",
		"[告警] Claude 整体 水位 92.5%，已达到阈值 80%",
		"[恢复] Claude 分组 aws1 水位 10.0%，已回落到阈值 80% 以下",
	}, strings.Split((*sent)[1].Text.Content, "\n")[1:])
	assert.Len(t, GetResourcePoolAlertConfig().Alerting, 2)

	// Switching channel monitoring on reports the source as well.
	config := GetResourcePoolAlertConfig()
	assert.False(t, config.ClaudeSourceEnabled)
	config.ClaudeSourceEnabled = true
	require.NoError(t, SaveResourcePoolAlertConfig(config))
	_, err = CheckResourcePoolWaterLevels(context.Background())
	require.NoError(t, err)
	require.Len(t, *sent, 3)
	assert.Equal(t, []string{"[告警] Claude 渠道 anthropic1 水位 92.5%，已达到阈值 80%"},
		strings.Split((*sent)[2].Text.Content, "\n")[1:])
	assert.Len(t, GetResourcePoolAlertConfig().Alerting, 3)

	// Switching it back off drops the source without a recovery notice, even
	// when the upstream cannot be polled at that moment.
	config = GetResourcePoolAlertConfig()
	assert.True(t, config.ClaudeSourceEnabled)
	config.ClaudeSourceEnabled = false
	require.NoError(t, SaveResourcePoolAlertConfig(config))
	upstream.Store("")
	_, err = CheckResourcePoolWaterLevels(context.Background())
	require.NoError(t, err)
	assert.Len(t, *sent, 3)
	alerting = GetResourcePoolAlertConfig().Alerting
	require.Len(t, alerting, 2)
	assert.Equal(t, []string{"group", "pool"}, []string{alerting[0].Kind, alerting[1].Kind})

	// Turning alerts off forgets what was reported.
	config = GetResourcePoolAlertConfig()
	config.Enabled = false
	require.NoError(t, SaveResourcePoolAlertConfig(config))
	assert.Empty(t, GetResourcePoolAlertConfig().Alerting)
}

// GPT / Gemini / Grok are judged per source on the average 7d usage of the
// accounts whose window numbers are current.
func TestCheckResourcePoolWaterLevelsAveragesLiveWindowAccounts(t *testing.T) {
	sent, _ := setupResourcePoolAlertTest(t)
	upstream := seedResourcePoolAlertSource(t, model.ResourcePoolGrok, "grok")
	require.NoError(t, SaveResourcePoolAlertConfig(ResourcePoolAlertConfig{
		Enabled: true, WebhookURL: testWeComWebhook, NotifyStart: "00:00", NotifyEnd: "00:00",
		Rules: alertRules(map[string]float64{model.ResourcePoolGrok: 60.5}),
	}))
	future := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	upstream.Store(fmt.Sprintf(`{"data":[
		{"id":"g1","enabled":true,"status":"active","usage_percent_7d":90,"reset_7d_at":%[1]q},
		{"id":"g2","enabled":true,"status":"active","weekly_percent":40,"usage_percent_7d":99,"reset_7d_at":%[1]q},
		{"id":"g3","enabled":true,"status":"active","usage_percent_7d":100,"reset_7d_at":%[2]q},
		{"id":"g4","enabled":true,"status":"banned","usage_percent_7d":100,"reset_7d_at":%[1]q},
		{"id":"g5","enabled":false,"status":"active","usage_percent_7d":100,"reset_7d_at":%[1]q}
	]}`, future, past))

	_, err := CheckResourcePoolWaterLevels(context.Background())
	require.NoError(t, err)
	require.Len(t, *sent, 1)
	// Only g1 and g2 are live: (90 + 40) / 2 = 65.
	assert.Equal(t, []string{"[告警] Grok 渠道 grok（7 天窗口平均） 水位 65.0%，已达到阈值 60.5%"},
		strings.Split((*sent)[0].Text.Content, "\n")[1:])

	// With no live account left there is no level to judge; the target is
	// dropped without a recovery notice.
	upstream.Store(fmt.Sprintf(`{"data":[{"id":"g3","enabled":true,"usage_percent_7d":100,"reset_7d_at":%q}]}`, past))
	_, err = CheckResourcePoolWaterLevels(context.Background())
	require.NoError(t, err)
	assert.Len(t, *sent, 1)
	assert.Empty(t, GetResourcePoolAlertConfig().Alerting)
}

func TestResourcePoolAlertRunsHourlyInsideNotifyWindow(t *testing.T) {
	setupResourcePoolAlertTest(t)
	at := func(clock string) time.Time {
		parsed, err := time.ParseInLocation(time.DateTime, "2026-11-26 "+clock, time.Local)
		require.NoError(t, err)
		return parsed
	}
	save := func(enabled bool, start string, end string, rules map[string]float64) {
		require.NoError(t, SaveResourcePoolAlertConfig(ResourcePoolAlertConfig{
			Enabled: enabled, WebhookURL: testWeComWebhook, NotifyStart: start, NotifyEnd: end, Rules: alertRules(rules),
		}))
	}
	claude := map[string]float64{model.ResourcePoolClaude: 80}

	save(true, "09:00", "21:00", claude)
	for clock, due := range map[string]bool{
		"08:59:59": false, "09:00:00": true, "15:30:00": true, "21:00:30": true, "21:01:00": false, "02:00:00": false,
	} {
		assert.Equal(t, due, ResourcePoolAlertDue(at(clock)), clock)
	}
	// A period that crosses midnight, and equal ends meaning all day.
	save(true, "22:00", "06:00", claude)
	assert.True(t, ResourcePoolAlertDue(at("23:00:00")))
	assert.True(t, ResourcePoolAlertDue(at("05:59:00")))
	assert.False(t, ResourcePoolAlertDue(at("12:00:00")))
	save(true, "00:00", "00:00", claude)
	assert.True(t, ResourcePoolAlertDue(at("03:00:00")))
	// Off, or no provider monitored: never due.
	save(true, "00:00", "00:00", nil)
	assert.False(t, ResourcePoolAlertDue(at("12:00:00")))
	save(false, "00:00", "00:00", claude)
	assert.False(t, ResourcePoolAlertDue(at("12:00:00")))

	// The scheduler runs a task whose last run is at least Interval old, so
	// the interval reaches back to just before the start of the hour: a run
	// earlier in this hour is not due, one from the previous hour is.
	now := at("10:20:15")
	interval := ResourcePoolAlertInterval(now)
	assert.Less(t, now.Sub(at("10:00:00")), interval)
	assert.GreaterOrEqual(t, now.Sub(at("09:59:59")), interval)
}

func TestSaveResourcePoolAlertConfigValidatesAndKeepsWebhook(t *testing.T) {
	setupResourcePoolAlertTest(t)
	valid := ResourcePoolAlertConfig{
		Enabled: true, WebhookURL: testWeComWebhook, NotifyStart: "09:00", NotifyEnd: "21:00",
		Rules: alertRules(map[string]float64{model.ResourcePoolGPT: 90}),
	}
	for name, mutate := range map[string]func(*ResourcePoolAlertConfig){
		"other host":      func(c *ResourcePoolAlertConfig) { c.WebhookURL = "https://example.com/cgi-bin/webhook/send?key=k" },
		"plain http":      func(c *ResourcePoolAlertConfig) { c.WebhookURL = strings.Replace(testWeComWebhook, "https", "http", 1) },
		"missing key":     func(c *ResourcePoolAlertConfig) { c.WebhookURL = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send" },
		"enabled, no url": func(c *ResourcePoolAlertConfig) { c.WebhookURL = "" },
		"bad clock":       func(c *ResourcePoolAlertConfig) { c.NotifyEnd = "25:00" },
		"zero threshold": func(c *ResourcePoolAlertConfig) {
			c.Rules[model.ResourcePoolGPT] = operation_setting.ResourcePoolAlertRule{Enabled: true}
		},
		"threshold too big": func(c *ResourcePoolAlertConfig) {
			c.Rules[model.ResourcePoolGPT] = operation_setting.ResourcePoolAlertRule{Threshold: 101}
		},
	} {
		config := valid
		config.Rules = alertRules(map[string]float64{model.ResourcePoolGPT: 90})
		mutate(&config)
		assert.Error(t, SaveResourcePoolAlertConfig(config), name)
	}

	require.NoError(t, SaveResourcePoolAlertConfig(valid))
	saved := GetResourcePoolAlertConfig()
	assert.Equal(t, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=****5678", saved.WebhookURL)
	assert.True(t, saved.WebhookConfigured)
	assert.Equal(t, operation_setting.ResourcePoolAlertRule{Enabled: true, Threshold: 90}, saved.Rules[model.ResourcePoolGPT])
	assert.Equal(t, operation_setting.ResourcePoolAlertRule{Threshold: 80}, saved.Rules[model.ResourcePoolClaude])

	// An empty URL or the echoed mask keeps the stored webhook.
	for _, echoed := range []string{"", saved.WebhookURL} {
		saved.WebhookURL = echoed
		require.NoError(t, SaveResourcePoolAlertConfig(saved))
		assert.Equal(t, testWeComWebhook, operation_setting.GetResourcePoolAlertSetting().WebhookURL)
	}
}
