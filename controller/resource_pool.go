package controller

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func isValidResourcePoolProvider(provider string) bool {
	return provider != "" && slices.Contains(model.ResourcePoolProviders, provider)
}

// resourcePoolSourcePayload is the console-facing source row. The admin
// password is masked on the way out; an empty value means "keep the stored one".
type resourcePoolSourcePayload struct {
	Id            int64  `json:"id"`
	Provider      string `json:"provider"`
	Name          string `json:"name"`
	Endpoint      string `json:"endpoint"`
	AdminPassword string `json:"admin_password"`
	GroupName     string `json:"group_name"`
	Enabled       bool   `json:"enabled"`
	SnapshotAt    int64  `json:"snapshot_at"`
}

// GetResourcePoolSources returns the configured sources of one provider.
func GetResourcePoolSources(c *gin.Context) {
	provider := c.Query("provider")
	if provider != "" && !isValidResourcePoolProvider(provider) {
		common.ApiErrorMsg(c, "unsupported provider")
		return
	}

	sources, err := model.GetResourcePoolSources(provider)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	payload := make([]resourcePoolSourcePayload, 0, len(sources))
	for _, source := range sources {
		payload = append(payload, resourcePoolSourcePayload{
			Id:            source.Id,
			Provider:      source.Provider,
			Name:          source.Name,
			Endpoint:      source.Endpoint,
			AdminPassword: model.MaskAdminPassword(source.AdminPassword),
			GroupName:     source.GroupName,
			Enabled:       source.Enabled,
			SnapshotAt:    source.SnapshotAt,
		})
	}
	common.ApiSuccess(c, payload)
}

type saveResourcePoolSourceRequest struct {
	Id            int64  `json:"id"`
	Name          string `json:"name"`
	Endpoint      string `json:"endpoint"`
	AdminPassword string `json:"admin_password"`
	GroupName     string `json:"group_name"`
	Enabled       bool   `json:"enabled"`
}

type saveResourcePoolSourcesRequest struct {
	Provider string                          `json:"provider"`
	Data     []saveResourcePoolSourceRequest `json:"data"`
}

// SaveResourcePoolSources replaces the source list of one provider.
func SaveResourcePoolSources(c *gin.Context) {
	var req saveResourcePoolSourcesRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if !isValidResourcePoolProvider(req.Provider) {
		common.ApiErrorMsg(c, "unsupported provider")
		return
	}

	// Reload masked passwords so an unchanged form keeps the stored secret.
	existing, err := model.GetResourcePoolSources(req.Provider)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	existingById := make(map[int64]*model.ResourcePoolSource, len(existing))
	for _, source := range existing {
		existingById[source.Id] = source
	}

	sources := make([]*model.ResourcePoolSource, 0, len(req.Data))
	for _, item := range req.Data {
		password := item.AdminPassword
		if item.Id > 0 {
			// A row id the client echoes back must belong to this provider.
			// Otherwise the request is stale (for example the user switched
			// providers with unsaved edits) and would silently no-op.
			stored, ok := existingById[item.Id]
			if !ok {
				common.ApiErrorMsg(c, fmt.Sprintf("source %d does not belong to provider %s", item.Id, req.Provider))
				return
			}
			if password == "" || isMaskedSecret(password) {
				password = stored.AdminPassword
			}
		}
		sources = append(sources, &model.ResourcePoolSource{
			Id:            item.Id,
			Name:          item.Name,
			Endpoint:      item.Endpoint,
			AdminPassword: password,
			GroupName:     item.GroupName,
			Enabled:       item.Enabled,
		})
	}

	if err := model.SaveResourcePoolSources(req.Provider, sources); err != nil {
		common.ApiError(c, err)
		return
	}
	service.InvalidateResourcePoolUsage(req.Provider)
	common.ApiSuccess(c, nil)
}

// isMaskedSecret reports whether the console echoed back a masked value.
// MaskAdminPassword always starts with four asterisks, including the short
// "****" it returns for secrets of four characters or fewer.
func isMaskedSecret(value string) bool {
	return len(value) >= 4 && value[:4] == "****"
}

// GetResourcePoolUsage returns the flattened account list plus per-source
// status of one provider. Polls are cached; refresh=true forces a new poll.
func GetResourcePoolUsage(c *gin.Context) {
	provider := c.Query("provider")
	if !isValidResourcePoolProvider(provider) {
		common.ApiErrorMsg(c, "unsupported provider")
		return
	}

	force, _ := strconv.ParseBool(c.Query("refresh"))
	result, err := service.GetResourcePoolUsage(provider, force)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

// DeleteResourcePoolSource removes one source row.
func DeleteResourcePoolSource(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		common.ApiErrorMsg(c, "invalid source id")
		return
	}
	if err := model.DeleteResourcePoolSource(id); err != nil {
		common.ApiError(c, err)
		return
	}
	service.InvalidateResourcePoolUsage("")
	common.ApiSuccess(c, nil)
}

// GetResourcePoolAlert returns the water level alert configuration. The
// webhook URL is masked.
func GetResourcePoolAlert(c *gin.Context) {
	common.ApiSuccess(c, service.GetResourcePoolAlertConfig())
}

// SaveResourcePoolAlert stores the water level alert configuration. An empty
// webhook URL keeps the stored one.
func SaveResourcePoolAlert(c *gin.Context) {
	var config service.ResourcePoolAlertConfig
	if err := common.DecodeJson(c.Request.Body, &config); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if err := service.SaveResourcePoolAlertConfig(config); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, service.GetResourcePoolAlertConfig())
}

// TestResourcePoolAlert sends a test message to the submitted webhook URL, or
// to the stored one when the URL is empty.
func TestResourcePoolAlert(c *gin.Context) {
	var request struct {
		WebhookURL string `json:"webhook_url"`
		Mentions   string `json:"mentions"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if err := service.SendResourcePoolAlertTest(c.Request.Context(), request.WebhookURL, request.Mentions); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
