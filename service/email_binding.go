package service

import (
	"crypto/rand"
	"errors"
	"fmt"
	"html"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
)

var (
	ErrAccountEmailInvalid    = errors.New("Please enter a valid email address")
	ErrAccountEmailRestricted = errors.New("This email address is not allowed by the administrator's email policy.")
	ErrEmailBindingDelivery   = errors.New("Verification email could not be sent. Start email verification again.")
)

type EmailBindingData struct {
	FlowToken           string `json:"flow_token"`
	Email               string `json:"email"`
	CurrentEmail        string `json:"current_email,omitempty"`
	OldEmailRequired    bool   `json:"old_email_required"`
	ExpiresAt           int64  `json:"expires_at"`
	ResendAt            int64  `json:"resend_at"`
	NotificationWarning bool   `json:"notification_warning"`
}

// ValidateAccountEmail is shared with registration mail delivery so binding
// cannot bypass the site's existing domain, alias and address-length rules.
func ValidateAccountEmail(email string) (string, error) {
	email = model.NormalizeEmail(email)
	if common.Validate.Var(email, "required,email,max=50") != nil {
		return "", ErrAccountEmailInvalid
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "", ErrAccountEmailInvalid
	}
	if common.EmailDomainRestrictionEnabled {
		allowed := slices.Contains(common.EmailDomainWhitelist, parts[1])
		if !allowed {
			return "", ErrAccountEmailRestricted
		}
	}
	if common.EmailAliasRestrictionEnabled && strings.ContainsAny(parts[0], "+.") {
		return "", ErrAccountEmailRestricted
	}
	return email, nil
}

func StartEmailBinding(identity AuthIdentity, authorization *model.AuthFlowAuthorization, email string, lang string) (*EmailBindingData, error) {
	email, err := ValidateAccountEmail(email)
	if err != nil {
		return nil, err
	}
	context, err := common.Marshal(AccountBindingContext{Provider: "email", Email: email})
	if err != nil {
		return nil, err
	}
	if err := ValidateFlowAuthorization(identity, VerificationOperation{Scope: VerificationScopeAccountBind, Context: context}, authorization); err != nil {
		return nil, err
	}
	user, err := model.GetUserById(identity.UserID, false)
	if err != nil {
		return nil, err
	}
	state := model.EmailBindingState{Authorization: authorization, CurrentEmail: model.NormalizeEmail(user.Email), Email: email, ResendAt: time.Now().Add(model.EmailBindingResendDelay).Unix()}
	requireOld := state.CurrentEmail != "" && authorization.Method != VerificationMethodTwoFA && authorization.Method != VerificationMethodPasskey
	codes, err := generateEmailBindingCodes(requireOld)
	if err != nil {
		return nil, err
	}
	state.NewCodeHash, state.OldCodeHash = codes.NewHash, codes.OldHash
	token, flow, err := model.CreateEmailBinding(identity, state)
	if err != nil {
		return nil, err
	}
	if err := sendEmailBindingCodes(lang, state, codes); err != nil {
		// A partially delivered pair must not leave a usable change request.
		_, _ = model.ConsumeAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeEmailBinding, UserId: identity.UserID, SessionId: identity.SessionID})
		return nil, ErrEmailBindingDelivery
	}
	data := emailBindingData(token, flow, &state)
	if state.CurrentEmail != "" && !requireOld {
		data.NotificationWarning = NotifyAccountSecurityChange(lang, state.CurrentEmail, i18n.MsgSecurityEventEmailChangeRequested) != nil
	}
	return data, nil
}

func ResendAccountEmailBinding(identity AuthIdentity, token string, lang string) (*EmailBindingData, error) {
	_, state, err := model.GetEmailBinding(identity, token)
	if err != nil {
		return nil, err
	}
	context, err := common.Marshal(AccountBindingContext{Provider: "email", Email: state.Email})
	if err != nil {
		return nil, err
	}
	if err := ValidateFlowAuthorization(identity, VerificationOperation{Scope: VerificationScopeAccountBind, Context: context}, state.Authorization); err != nil {
		return nil, err
	}
	if state.ResendAt > time.Now().Unix() {
		return nil, model.ErrEmailBindingResendWait
	}
	if state.FailedAttempts >= model.EmailBindingMaxAttempts {
		return nil, model.ErrEmailBindingLocked
	}
	codes, err := generateEmailBindingCodes(state.OldCodeHash != "")
	if err != nil {
		return nil, err
	}
	flow, state, err := model.ResendEmailBinding(identity, token, codes.NewHash, codes.OldHash)
	if err != nil {
		return nil, err
	}
	if err := sendEmailBindingCodes(lang, *state, codes); err != nil {
		_, _ = model.ConsumeAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeEmailBinding, UserId: identity.UserID, SessionId: identity.SessionID})
		return nil, ErrEmailBindingDelivery
	}
	return emailBindingData(token, flow, state), nil
}

func FinishEmailBinding(identity AuthIdentity, token, newCode, oldCode string) (*model.EmailBindingState, error) {
	_, state, err := model.GetEmailBinding(identity, token)
	if err != nil {
		return nil, err
	}
	context, err := common.Marshal(AccountBindingContext{Provider: "email", Email: state.Email})
	if err != nil {
		return nil, err
	}
	if err := ValidateFlowAuthorization(identity, VerificationOperation{Scope: VerificationScopeAccountBind, Context: context}, state.Authorization); err != nil {
		return nil, err
	}
	if _, err := ValidateAccountEmail(state.Email); err != nil {
		return nil, err
	}
	return model.CompleteEmailBinding(identity, token, state.Email, newCode, oldCode)
}

type emailBindingCodes struct {
	New, Old         string
	NewHash, OldHash string
}

func generateEmailBindingCodes(requireOld bool) (emailBindingCodes, error) {
	var codes emailBindingCodes
	count := 1
	if requireOld {
		count = 2
	}
	for index := 0; index < count; {
		number, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
		if err != nil {
			return codes, err
		}
		code := fmt.Sprintf("%06d", number.Int64())
		if code == codes.New {
			continue
		}
		hash, err := common.Password2Hash(code)
		if err != nil {
			return codes, err
		}
		if index == 0 {
			codes.New, codes.NewHash = code, hash
		} else {
			codes.Old, codes.OldHash = code, hash
		}
		index++
	}
	return codes, nil
}

func sendEmailBindingCodes(lang string, state model.EmailBindingState, codes emailBindingCodes) error {
	subject := i18n.Translate(lang, i18n.MsgEmailBindingSubject, map[string]any{"SystemName": common.SystemName})
	minutes := map[string]any{"Minutes": int(model.EmailBindingTTL / time.Minute)}
	content := fmt.Sprintf("<p>%s</p><p>%s</p><p>%s</p>",
		i18n.Translate(lang, i18n.MsgEmailBindingNewIntro),
		i18n.Translate(lang, i18n.MsgEmailBindingCode, map[string]any{"Code": html.EscapeString(codes.New)}),
		i18n.Translate(lang, i18n.MsgEmailBindingNewValidity, minutes))
	if err := common.SendEmail(subject, state.Email, content); err != nil {
		return err
	}
	if codes.Old != "" {
		content = fmt.Sprintf("<p>%s</p><p>%s</p><p>%s</p>",
			i18n.Translate(lang, i18n.MsgEmailBindingOldIntro),
			i18n.Translate(lang, i18n.MsgEmailBindingCode, map[string]any{"Code": html.EscapeString(codes.Old)}),
			i18n.Translate(lang, i18n.MsgEmailBindingOldValidity, minutes))
		return common.SendEmail(subject, state.CurrentEmail, content)
	}
	return nil
}

func emailBindingData(token string, flow *model.AuthFlow, state *model.EmailBindingState) *EmailBindingData {
	return &EmailBindingData{
		FlowToken: token, Email: state.Email, CurrentEmail: common.MaskEmail(state.CurrentEmail),
		OldEmailRequired: state.OldCodeHash != "", ExpiresAt: flow.ExpiresAt.Unix(), ResendAt: state.ResendAt,
	}
}

// EmailVerificationData describes a pending email security verification. Email
// is masked; the code only travels by mail.
type EmailVerificationData struct {
	FlowToken string `json:"flow_token"`
	Email     string `json:"email"`
	ExpiresAt int64  `json:"expires_at"`
	ResendAt  int64  `json:"resend_at"`
}

// StartEmailVerification sends a code bound to one operation to the account's
// bound email address. The address never comes from the request.
func StartEmailVerification(identity AuthIdentity, operation VerificationOperation, lang string) (*EmailVerificationData, error) {
	binding, err := BindVerificationOperation(operation)
	if err != nil {
		return nil, err
	}
	if _, err := RequireVerificationMethod(identity, binding.Scope, VerificationMethodEmail); err != nil {
		return nil, err
	}
	user, err := model.GetUserById(identity.UserID, false)
	if err != nil {
		return nil, err
	}
	email := model.NormalizeEmail(user.Email)
	if email == "" {
		return nil, ErrVerificationUnavailable
	}
	codes, err := generateEmailBindingCodes(false)
	if err != nil {
		return nil, err
	}
	state := model.EmailStepUpState{
		AuthSessionIdentity: identity, Scope: binding.Scope, ContextHash: binding.ContextHash,
		Email: email, CodeHash: codes.NewHash, ResendAt: time.Now().Add(model.EmailBindingResendDelay).Unix(),
	}
	token, flow, err := model.CreateEmailStepUp(identity, state)
	if err != nil {
		return nil, err
	}
	if err := sendEmailVerificationCode(lang, email, codes.New); err != nil {
		// An undelivered code must not leave a usable verification behind.
		_, _ = model.ConsumeAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeEmailStepUp, UserId: identity.UserID, SessionId: identity.SessionID})
		return nil, ErrEmailBindingDelivery
	}
	return emailVerificationData(token, flow, &state), nil
}

func ResendEmailVerification(identity AuthIdentity, token string, lang string) (*EmailVerificationData, error) {
	_, state, err := model.GetEmailStepUp(identity, token)
	if err != nil {
		return nil, err
	}
	if state.ResendAt > time.Now().Unix() {
		return nil, model.ErrEmailBindingResendWait
	}
	if state.FailedAttempts >= model.EmailBindingMaxAttempts {
		return nil, model.ErrEmailBindingLocked
	}
	// Recheck the policy so a factor enrolled meanwhile stops further mail.
	if _, err := RequireVerificationMethod(identity, state.Scope, VerificationMethodEmail); err != nil {
		return nil, err
	}
	codes, err := generateEmailBindingCodes(false)
	if err != nil {
		return nil, err
	}
	flow, state, err := model.ResendEmailStepUp(identity, token, codes.NewHash)
	if err != nil {
		return nil, err
	}
	if err := sendEmailVerificationCode(lang, state.Email, codes.New); err != nil {
		_, _ = model.ConsumeAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeEmailStepUp, UserId: identity.UserID, SessionId: identity.SessionID})
		return nil, ErrEmailBindingDelivery
	}
	return emailVerificationData(token, flow, state), nil
}

// FinishEmailVerification consumes the flow with a correct code and issues a
// proof for the operation bound at start.
func FinishEmailVerification(identity AuthIdentity, token, code string) (*SecurityProof, error) {
	state, err := model.CompleteEmailStepUp(identity, token, code)
	if err != nil {
		return nil, err
	}
	return CompleteSecurityVerification(identity, VerificationBinding{Scope: state.Scope, ContextHash: state.ContextHash}, VerificationMethodEmail)
}

func sendEmailVerificationCode(lang, email, code string) error {
	subject := i18n.Translate(lang, i18n.MsgEmailStepUpSubject, map[string]any{"SystemName": common.SystemName})
	minutes := map[string]any{"Minutes": int(model.EmailBindingTTL / time.Minute)}
	content := fmt.Sprintf("<p>%s</p><p>%s</p><p>%s</p>",
		i18n.Translate(lang, i18n.MsgEmailStepUpIntro),
		i18n.Translate(lang, i18n.MsgEmailBindingCode, map[string]any{"Code": html.EscapeString(code)}),
		i18n.Translate(lang, i18n.MsgEmailBindingNewValidity, minutes))
	return common.SendEmail(subject, email, content)
}

func emailVerificationData(token string, flow *model.AuthFlow, state *model.EmailStepUpState) *EmailVerificationData {
	return &EmailVerificationData{FlowToken: token, Email: common.MaskEmail(state.Email), ExpiresAt: flow.ExpiresAt.Unix(), ResendAt: state.ResendAt}
}
