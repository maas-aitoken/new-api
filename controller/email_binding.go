package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type emailBindRequest struct {
	Email     string `json:"email"`
	FlowToken string `json:"flow_token"`
	NewCode   string `json:"new_code"`
	OldCode   string `json:"old_code"`
}

func EmailBindStart(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	succeeded, notificationFailed := false, false
	defer func() {
		recordUserSecurityAudit(c, identity.UserID, "user.binding_start", map[string]any{"provider": "email", "success": succeeded, "notification_failed": notificationFailed})
	}()
	var request emailBindRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		writeSecurityOperationError(c, service.ErrVerificationContextInvalid)
		return
	}
	email, err := service.ValidateAccountEmail(request.Email)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	context, err := common.Marshal(service.AccountBindingContext{Provider: "email", Email: email})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	// Refuse SSO accounts before the proof is consumed, so a doomed request
	// does not burn the user's verification. The model rechecks in the flow.
	user, err := model.GetUserById(identity.UserID, false)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if user.HoidcId != "" {
		writeSecurityOperationError(c, model.ErrSsoEmailManaged)
		return
	}
	authorization := middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: context})
	if authorization == nil {
		return
	}
	data, err := service.StartEmailBinding(identity, authorization, email, service.AccountMailLanguage(c, identity.UserID))
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	succeeded, notificationFailed = true, data.NotificationWarning
	common.ApiSuccess(c, data)
}

func EmailBindResend(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	succeeded := false
	defer func() {
		recordUserSecurityAudit(c, identity.UserID, "user.email_binding_resend", map[string]any{"success": succeeded})
	}()
	var request emailBindRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.FlowToken == "" {
		writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
		return
	}
	data, err := service.ResendAccountEmailBinding(identity, request.FlowToken, service.AccountMailLanguage(c, identity.UserID))
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	succeeded = true
	common.ApiSuccess(c, data)
}

func EmailBind(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	succeeded, notificationFailed := false, false
	defer func() {
		recordUserSecurityAudit(c, identity.UserID, "user.binding_bind", map[string]any{"provider": "email", "success": succeeded, "notification_failed": notificationFailed})
	}()
	var request emailBindRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.FlowToken == "" {
		writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
		return
	}
	state, err := service.FinishEmailBinding(identity, request.FlowToken, request.NewCode, request.OldCode)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	succeeded = true
	lang := service.AccountMailLanguage(c, identity.UserID)
	notificationFailed = service.NotifyAccountSecurityChange(lang, state.CurrentEmail, i18n.MsgSecurityEventEmailChanged) != nil
	if err := service.NotifyAccountSecurityChange(lang, state.Email, i18n.MsgSecurityEventEmailConfirmed); err != nil {
		notificationFailed = true
	}
	if err := model.PublishUserAuthCache(identity.UserID); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"notification_warning": notificationFailed}})
}

type emailVerifyRequest struct {
	FlowToken string `json:"flow_token"`
	Code      string `json:"code"`
}

// EmailVerifyStart mails a security verification code bound to the requested
// operation to the account's bound email address.
func EmailVerifyStart(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	var operation service.VerificationOperation
	if err := common.DecodeJson(c.Request.Body, &operation); err != nil {
		writeSecurityOperationError(c, service.ErrVerificationContextInvalid)
		return
	}
	data, err := service.StartEmailVerification(identity, operation, service.AccountMailLanguage(c, identity.UserID))
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

func EmailVerifyResend(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	var request emailVerifyRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.FlowToken == "" {
		writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
		return
	}
	data, err := service.ResendEmailVerification(identity, request.FlowToken, service.AccountMailLanguage(c, identity.UserID))
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

// EmailVerifyFinish exchanges a correct code for a security proof. The audit
// record names the method and scope, never the code or the address.
func EmailVerifyFinish(c *gin.Context) {
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeSecurityOperationError(c, service.ErrAuthTokenInvalid)
		return
	}
	var request emailVerifyRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.FlowToken == "" {
		writeSecurityOperationError(c, model.ErrAuthFlowInvalid)
		return
	}
	proof, err := service.FinishEmailVerification(identity, request.FlowToken, request.Code)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	recordUserSecurityAudit(c, identity.UserID, "user.security_verify", map[string]any{"method": proof.Method, "scope": proof.Scope})
	common.ApiSuccess(c, proof)
}
