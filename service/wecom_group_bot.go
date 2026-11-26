package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	weComGroupBotHost = "qyapi.weixin.qq.com"
	weComGroupBotPath = "/cgi-bin/webhook/send"
	// weComGroupBotTextLimit is the byte limit WeCom puts on one text message.
	weComGroupBotTextLimit = 2048
	weComGroupBotTimeout   = 10 * time.Second
)

var weComGroupBotHTTPClient = GetHttpClient

type weComGroupBotText struct {
	Content             string   `json:"content"`
	MentionedList       []string `json:"mentioned_list,omitempty"`
	MentionedMobileList []string `json:"mentioned_mobile_list,omitempty"`
}

type weComGroupBotMessage struct {
	MsgType string            `json:"msgtype"`
	Text    weComGroupBotText `json:"text"`
}

// ValidateWeComGroupBotURL accepts only the WeCom group bot webhook address
// (https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...). The console lets
// administrators set this URL, so pinning the host keeps the server from
// posting to arbitrary addresses.
func ValidateWeComGroupBotURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != weComGroupBotHost ||
		parsed.Path != weComGroupBotPath || parsed.Query().Get("key") == "" {
		return fmt.Errorf("webhook URL must look like https://%s%s?key=...", weComGroupBotHost, weComGroupBotPath)
	}
	return nil
}

// MaskWeComGroupBotURL hides the bot key, the only secret part of the URL.
func MaskWeComGroupBotURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || raw == "" {
		return ""
	}
	key := parsed.Query().Get("key")
	if len(key) > 4 {
		key = key[len(key)-4:]
	} else {
		key = ""
	}
	return fmt.Sprintf("%s://%s%s?key=****%s", parsed.Scheme, parsed.Host, parsed.Path, key)
}

// splitWeComMentions sorts the configured mentions into user IDs and mobile
// numbers, the two lists a text message takes. "@all" goes with the user IDs.
func splitWeComMentions(mentions string) (users []string, mobiles []string) {
	fields := strings.FieldsFunc(mentions, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '|' || r == ' ' || r == '\n' || r == '\t'
	})
	for _, field := range fields {
		isMobile := len(field) == 11 && strings.Trim(field, "0123456789") == ""
		if isMobile {
			mobiles = append(mobiles, field)
		} else {
			users = append(users, field)
		}
	}
	return users, mobiles
}

// SendWeComGroupBotText posts one text message straight to a WeCom group bot.
// Errors never include the webhook URL, because it carries the bot key.
func SendWeComGroupBotText(ctx context.Context, webhookURL string, content string, mentions string) error {
	if err := ValidateWeComGroupBotURL(webhookURL); err != nil {
		return err
	}
	users, mobiles := splitWeComMentions(mentions)
	payload, err := common.Marshal(weComGroupBotMessage{
		MsgType: "text",
		Text:    weComGroupBotText{Content: content, MentionedList: users, MentionedMobileList: mobiles},
	})
	if err != nil {
		return fmt.Errorf("marshal WeCom group bot message: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, weComGroupBotTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return errors.New("create WeCom group bot request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	client := weComGroupBotHTTPClient()
	if client == nil {
		return errors.New("HTTP client is not initialized")
	}
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error prints the request URL; keep only the cause.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("send WeCom group bot message: %w", err)
	}
	defer CloseResponseBodyGracefully(resp)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("read WeCom group bot response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("WeCom group bot returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := common.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode WeCom group bot response: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("WeCom group bot returned errcode %d: %s", result.ErrCode, result.ErrMsg)
	}
	return nil
}
