package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func formatNotifyType(channelId int, status int) string {
	return fmt.Sprintf("%s_%d_%d", dto.NotifyTypeChannelUpdate, channelId, status)
}

func formatChannelModelNotifyType(channelID int, modelName string, status int) string {
	return fmt.Sprintf("%s_%d_%s_%d", dto.NotifyTypeChannelUpdate, channelID, common.Sha1([]byte(modelName)), status)
}

func DisableChannelModel(channelID int, modelName string, channelName string, reason string, statusCode int, autoBan bool) {
	if !autoBan {
		return
	}
	changed, err := model.DisableChannelModel(channelID, modelName, reason)
	if err != nil {
		common.SysLog(fmt.Sprintf("禁用渠道模型失败：channel_id=%d model=%s error=%v", channelID, modelName, err))
		return
	}
	if !changed {
		return
	}
	ClearChannelModelFailureCounters(channelID, modelName)
	if err := model.RecordChannelModelEvent(&model.ChannelModelEvent{
		ChannelId: channelID, ChannelName: channelName, Model: modelName, Event: model.ChannelModelEventDisabled, StatusCode: statusCode, Reason: reason,
	}); err != nil {
		common.SysError("failed to record channel model disabled event: " + err.Error())
	}
	subject := fmt.Sprintf("通道「%s」（#%d）的模型「%s」已被禁用", channelName, channelID, modelName)
	content := fmt.Sprintf("通道「%s」（#%d）的模型「%s」已被禁用，原因：%s", channelName, channelID, modelName, reason)
	NotifyChannelWatchers(formatChannelModelNotifyType(channelID, modelName, common.ChannelStatusAutoDisabled), func(string) (string, string) { return subject, content })
	notifyChannelModelDisabledWeCom(channelID, modelName, channelName, statusCode, reason)
}

func EnableChannelModel(channelID int, modelName string, channelName string, successCount int) {
	changed, err := model.EnableChannelModel(channelID, modelName)
	if err != nil {
		common.SysLog(fmt.Sprintf("恢复渠道模型失败：channel_id=%d model=%s error=%v", channelID, modelName, err))
		return
	}
	if !changed {
		return
	}
	ClearChannelModelFailureCounters(channelID, modelName)
	if err := model.RecordChannelModelEvent(&model.ChannelModelEvent{
		ChannelId: channelID, ChannelName: channelName, Model: modelName, Event: model.ChannelModelEventRecovered, SuccessCount: successCount,
	}); err != nil {
		common.SysError("failed to record channel model recovered event: " + err.Error())
	}
	subject := fmt.Sprintf("通道「%s」（#%d）的模型「%s」已恢复", channelName, channelID, modelName)
	content := fmt.Sprintf("通道「%s」（#%d）的模型「%s」已恢复路由", channelName, channelID, modelName)
	NotifyChannelWatchers(formatChannelModelNotifyType(channelID, modelName, common.ChannelStatusEnabled), func(string) (string, string) { return subject, content })
	notifyChannelModelRecoveredWeCom(channelID, modelName, channelName, successCount, "")
}

func ResetChannelModelCircuitBreakerState(reason string) error {
	statuses, err := model.GetAllChannelModelStatuses()
	if err != nil {
		return err
	}
	channelIDs := make([]int, 0, len(statuses))
	seen := make(map[int]bool)
	for _, status := range statuses {
		if !seen[status.ChannelId] {
			seen[status.ChannelId] = true
			channelIDs = append(channelIDs, status.ChannelId)
		}
	}
	channels, err := model.GetChannelsByIds(channelIDs)
	if err != nil {
		return err
	}
	channelNames := make(map[int]string, len(channels))
	for _, channel := range channels {
		channelNames[channel.Id] = channel.Name
	}
	if err := model.ClearAllChannelModelStatuses(); err != nil {
		return err
	}
	if err := ClearAllChannelModelFailureCounters(); err != nil {
		return err
	}
	for _, status := range statuses {
		if err := model.RecordChannelModelEvent(&model.ChannelModelEvent{
			ChannelId: status.ChannelId, ChannelName: channelNames[status.ChannelId], Model: status.Model,
			Event: model.ChannelModelEventRecovered, SuccessCount: status.RecoverySuccessCount, Reason: reason,
		}); err != nil {
			common.SysError("failed to record channel model reset event: " + err.Error())
		}
		notifyChannelModelRecoveredWeCom(status.ChannelId, status.Model, channelNames[status.ChannelId], status.RecoverySuccessCount, reason)
	}
	return nil
}

func ResetExcludedChannelModelCircuitBreakerState(channelIDs []int, reason string) error {
	statuses, err := model.GetChannelModelStatusesByChannelIDs(channelIDs)
	if err != nil {
		return err
	}
	channels, err := model.GetChannelsByIds(channelIDs)
	if err != nil {
		return err
	}
	channelNames := make(map[int]string, len(channels))
	for _, channel := range channels {
		channelNames[channel.Id] = channel.Name
	}
	if err := model.ClearChannelModelStatusesByChannelIDs(channelIDs); err != nil {
		return err
	}
	if err := ClearChannelModelFailureCountersByChannelIDs(channelIDs); err != nil {
		return err
	}
	for _, status := range statuses {
		if err := model.RecordChannelModelEvent(&model.ChannelModelEvent{
			ChannelId: status.ChannelId, ChannelName: channelNames[status.ChannelId], Model: status.Model,
			Event: model.ChannelModelEventRecovered, SuccessCount: status.RecoverySuccessCount, Reason: reason,
		}); err != nil {
			common.SysError("failed to record excluded channel model reset event: " + err.Error())
		}
		notifyChannelModelRecoveredWeCom(status.ChannelId, status.Model, channelNames[status.ChannelId], status.RecoverySuccessCount, reason)
	}
	return nil
}

func notifyChannelModelDisabledWeCom(channelID int, modelName string, channelName string, statusCode int, reason string) {
	content := fmt.Sprintf(
		"[渠道模型熔断]\n站点：%s\n渠道：%s (#%d)\n模型：%s\n状态码：%d\n原因：%s\n时间：%s",
		common.SystemName, channelName, channelID, modelName, statusCode, common.LocalLogPreview(reason), time.Now().Format(time.DateTime),
	)
	if err := NotifyChannelModelWeComBot(content); err != nil {
		common.SysError("failed to send channel model disabled WeCom notification: " + err.Error())
	}
}

func notifyChannelModelRecoveredWeCom(channelID int, modelName string, channelName string, successCount int, reason string) {
	content := fmt.Sprintf(
		"[渠道模型恢复]\n站点：%s\n渠道：%s (#%d)\n模型：%s\n连续成功次数：%d\n时间：%s",
		common.SystemName, channelName, channelID, modelName, successCount, time.Now().Format(time.DateTime),
	)
	if reason != "" {
		content += "\n原因：" + common.LocalLogPreview(reason)
	}
	if err := NotifyChannelModelWeComBot(content); err != nil {
		common.SysError("failed to send channel model recovered WeCom notification: " + err.Error())
	}
}

func shouldCloseActiveWebSocketsAfterDisable(channelId int) bool {
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.SysLog(common.LogText("failed to check channel status before closing active websockets: channel_id=%d, error=%v", channelId, err))
		return true
	}
	return channel.Status != common.ChannelStatusEnabled
}

// disable & notify
func DisableChannel(channelError types.ChannelError, reason string) {
	common.SysLog(common.LogText("channel %q (#%d) failed, disabling it, reason: %s", channelError.ChannelName, channelError.ChannelId, common.LocalLogPreview(reason)))

	// 检查是否启用自动禁用功能
	if !channelError.AutoBan {
		common.SysLog(common.LogText("channel %q (#%d) has automatic disabling turned off, skipping", channelError.ChannelName, channelError.ChannelId))
		return
	}

	success := model.UpdateChannelStatus(channelError.ChannelId, channelError.UsingKey, common.ChannelStatusAutoDisabled, reason)
	if success {
		if shouldCloseActiveWebSocketsAfterDisable(channelError.ChannelId) {
			CloseActiveWebSocketsForChannel(channelError.ChannelId, ChannelDisabledCloseReason)
		}
		params := map[string]any{"Name": channelError.ChannelName, "Id": channelError.ChannelId, "Reason": reason}
		NotifyChannelWatchers(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), func(lang string) (string, string) {
			return i18n.Translate(lang, i18n.MsgChannelNotifyDisabledSubject, params), i18n.Translate(lang, i18n.MsgChannelNotifyDisabledContent, params)
		})
	}
}

func EnableChannel(channelId int, usingKey string, channelName string) {
	success := model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusEnabled, "")
	if success {
		params := map[string]any{"Name": channelName, "Id": channelId}
		NotifyChannelWatchers(formatNotifyType(channelId, common.ChannelStatusEnabled), func(lang string) (string, string) {
			return i18n.Translate(lang, i18n.MsgChannelNotifyEnabledSubject, params), i18n.Translate(lang, i18n.MsgChannelNotifyEnabledContent, params)
		})
	}
}

func ShouldDisableChannel(err *types.NewAPIError) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	// 上游拒答是内容问题，渠道本身是好的；错误信息里还带着上游原文，不能拿去匹配禁用关键词。
	if err.GetErrorCode() == types.ErrorCodeUpstreamRefusal {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if operation_setting.ShouldDisableByStatusCode(err.StatusCode) {
		return true
	}

	lowerMessage := strings.ToLower(err.Error())
	search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true)
	return search
}

func ShouldEnableChannel(newAPIError *types.NewAPIError, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if newAPIError != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}
