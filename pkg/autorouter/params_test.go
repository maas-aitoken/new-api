package autorouter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestParamsKeepsEverythingButTheConversation(t *testing.T) {
	tests := []struct {
		name     string
		protocol Protocol
		body     string
		want     string
	}{
		{
			name:     "chat keeps sampling knobs and merged extra_body fields",
			protocol: ProtocolChat,
			body:     `{"model":"auto","experiment_id":"tau3_bench_1","temperature":0.2,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}]}`,
			want:     `{"model":"auto","experiment_id":"tau3_bench_1","temperature":0.2}`,
		},
		{
			name:     "anthropic also drops the system prompt",
			protocol: ProtocolAnthropic,
			body:     `{"system":"sys","messages":[],"max_tokens":8,"metadata":{"user_id":"u1"}}`,
			want:     `{"max_tokens":8,"metadata":{"user_id":"u1"}}`,
		},
		{
			name:     "responses drops instructions and input",
			protocol: ProtocolResponses,
			body:     `{"instructions":"sys","input":"hi","reasoning":{"effort":"high"}}`,
			want:     `{"reasoning":{"effort":"high"}}`,
		},
		{
			name:     "large integers keep their digits",
			protocol: ProtocolChat,
			body:     `{"run_id":9007199254740993,"messages":[]}`,
			want:     `{"run_id":9007199254740993}`,
		},
		{name: "only conversation fields", protocol: ProtocolChat, body: `{"messages":[],"tools":[]}`, want: ""},
		{name: "unknown protocol", protocol: Protocol("gemini"), body: `{"temperature":1}`, want: ""},
		{name: "not an object", protocol: ProtocolChat, body: `[1,2]`, want: ""},
		{name: "not json", protocol: ProtocolChat, body: `nope`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, RequestParams([]byte(tt.body), tt.protocol, 2048))
		})
	}
}

// 超出上限的字段整个丢掉，后面放得下的照样保留，结果始终是合法 JSON。
func TestRequestParamsDropsWholeFieldsThatDoNotFit(t *testing.T) {
	body := `{"a":1,"big":"` + strings.Repeat("x", 100) + `","b":2,"messages":[]}`

	got := RequestParams([]byte(body), ProtocolChat, 30)

	assert.Equal(t, `{"a":1,"b":2}`, got)
	assert.Empty(t, RequestParams([]byte(body), ProtocolChat, 5), "nothing fits")
}
