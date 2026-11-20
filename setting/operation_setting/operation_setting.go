package operation_setting

import "strings"

var DemoSiteEnabled = false
var SelfUseModeEnabled = false

var AutomaticDisableKeywords = []string{
	"Your credit balance is too low",
	"This organization has been disabled.",
	"You exceeded your current quota",
	"Permission denied",
	"The security token included in the request is invalid",
	"Operation not allowed",
	"Your account is not authorized",
}

func AutomaticDisableKeywordsToString() string {
	return strings.Join(AutomaticDisableKeywords, "\n")
}

func AutomaticDisableKeywordsFromString(s string) {
	AutomaticDisableKeywords = []string{}
	ak := strings.SplitSeq(s, "\n")
	for k := range ak {
		k = strings.TrimSpace(k)
		k = strings.ToLower(k)
		if k != "" {
			AutomaticDisableKeywords = append(AutomaticDisableKeywords, k)
		}
	}
}

// UpstreamRefusalRetryEnabled 打开后，上游拒答（Claude stop_reason=refusal、OpenAI
// finish_reason=content_filter、Responses incomplete_details.reason=content_filter、
// Gemini blockReason / finishReason=SAFETY 等）只要发生在客户端收到任何正文之前，
// 这次尝试照常按用量计费，但回答不发给客户端，改判为失败后按重试规则换渠道或换模型。
var UpstreamRefusalRetryEnabled = false

// UpstreamRefusalRetryAutoOnly 打开后，上面的拒答重试只对智能路由（auto）的请求生效：
// 锁定模型的请求照原样把拒答返回给客户端（200、照常计费），另记一条错误日志留痕。
var UpstreamRefusalRetryAutoOnly = false

// UpstreamRefusalOpeningTexts 是已知拒答回答的开头。拒答信号在流的最后才到，
// 流式回答的开头和其中某一句对得上时先压着不发，等到结束原因再决定；对不上就立刻放行。
// 匹配时忽略大小写和开头的空白。
var UpstreamRefusalOpeningTexts = []string{
	"The selected model cannot continue this conversation",
}

func UpstreamRefusalOpeningTextsToString() string {
	return strings.Join(UpstreamRefusalOpeningTexts, "\n")
}

func UpstreamRefusalOpeningTextsFromString(s string) {
	texts := []string{}
	for text := range strings.SplitSeq(s, "\n") {
		if text = strings.TrimSpace(text); text != "" {
			texts = append(texts, text)
		}
	}
	UpstreamRefusalOpeningTexts = texts
}
