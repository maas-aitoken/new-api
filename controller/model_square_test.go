package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModelSquareControllerTest(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Model{},
		&model.Vendor{},
		&model.Channel{},
		&model.Ability{},
		&model.ChannelModelCapability{},
	))
	model.DB = db
	model.InvalidatePricingCache()
	t.Cleanup(func() {
		model.DB = previousDB
		model.InvalidatePricingCache()
		model.InvalidateCapabilityCache()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func seedModelSquareModel(t *testing.T, db *gorm.DB, id int, name string, capabilities string) {
	t.Helper()
	require.NoError(t, db.Create(&model.Model{
		Id:           id,
		ModelName:    name,
		Status:       1,
		NameRule:     model.NameRuleExact,
		Capabilities: capabilities,
	}).Error)
	require.NoError(t, db.Create(&model.Channel{
		Id:     id,
		Name:   fmt.Sprintf("channel-%d", id),
		Group:  "default",
		Models: name,
		Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     name,
		ChannelId: id,
		Enabled:   true,
	}).Error)
}

func TestGetModelSquareSupportsPagination(t *testing.T) {
	db := setupModelSquareControllerTest(t)
	for index := 1; index <= 3; index++ {
		seedModelSquareModel(t, db, index, fmt.Sprintf("catalog-model-%d", index), "")
	}
	model.InvalidatePricingCache()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/model-square?offset=1&limit=1", nil)

	GetModelSquare(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Items  []modelSquareItem `json:"items"`
			Total  int64             `json:"total"`
			Offset int               `json:"offset"`
			Limit  int               `json:"limit"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, 2, response.Data.Items[0].ID)
	assert.Equal(t, "catalog-model-2", response.Data.Items[0].ModelName)
	assert.Equal(t, int64(3), response.Data.Total)
	assert.Equal(t, 1, response.Data.Offset)
	assert.Equal(t, 1, response.Data.Limit)
}

func TestGetModelSquareDetailExposesCapabilityDerivedFields(t *testing.T) {
	db := setupModelSquareControllerTest(t)
	capabilities := `{
		"context": {"max_output_tokens": 8192},
		"modalities": {"input": {"text": true, "image": {"supported": true}}, "output": ["text"]},
		"tools": {"supported": true, "tool_choice": ["auto", "required"]},
		"structured_output": {"json_schema": true},
		"reasoning": {"supported": true, "efforts": ["low", "high"]},
		"params": {"temperature": {"min": 0, "max": 1.5}, "top_p": {"min": 0, "max": 1}, "n": {"max": 2}},
		"streaming": {"supported": true}
	}`
	seedModelSquareModel(t, db, 1, "capability-model", capabilities)
	model.InvalidatePricingCache()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: "1"}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/model-square/1", nil)

	GetModelSquareDetail(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool            `json:"success"`
		Data    modelSquareItem `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.Contains(t, response.Data.Capabilities, "structured_output")
	assert.Contains(t, response.Data.Capabilities, "reasoning")
	assert.Contains(t, response.Data.InputModalities, "image")
	assert.Equal(t, int64(8192), *response.Data.MaxOutputTokens)
	parameters := make(map[string]struct{ Type, Range string }, len(response.Data.Parameters))
	for _, parameter := range response.Data.Parameters {
		parameters[parameter.Name] = struct{ Type, Range string }{Type: parameter.Type, Range: parameter.Range}
	}
	assert.Equal(t, "number", parameters["temperature"].Type)
	assert.Equal(t, "0 ~ 1.5", parameters["temperature"].Range)
	assert.Equal(t, "integer", parameters["n"].Type)
	assert.Equal(t, "1 ~ 2", parameters["n"].Range)
}

func TestGetModelSquareDetailRejectsUnknownModel(t *testing.T) {
	setupModelSquareControllerTest(t)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: "404"}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/model-square/404", nil)

	GetModelSquareDetail(context)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "model not found", response.Message)
}

func TestGetModelSquareListsRuleMatchedModelsWithChannels(t *testing.T) {
	db := setupModelSquareControllerTest(t)
	require.NoError(t, db.Create(&model.Model{
		Id:           10,
		ModelName:    "family-",
		Status:       1,
		NameRule:     model.NameRulePrefix,
		Capabilities: `{"params":{"top_p":{"min":0,"max":1}},"tools":{"supported":true}}`,
	}).Error)
	setting := `{"cost_multiplier":{"family-model-1":1.5}}`
	require.NoError(t, db.Create(&model.Channel{
		Id: 7, Name: "seven", Group: "default", Models: "family-model-1",
		Status: common.ChannelStatusEnabled, Setting: &setting,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "family-model-1", ChannelId: 7, Enabled: true,
	}).Error)
	require.NoError(t, model.InitCapabilityCache())
	require.NoError(t, model.SetChannelModelCapability(7, "family-model-1", `{"params":{"top_p":{"min":0,"max":0.5}}}`))
	model.InvalidatePricingCache()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/model-square", nil)
	GetModelSquare(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Items []modelSquareItem `json:"items"`
			Total int64             `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, int64(1), response.Data.Total)
	require.Len(t, response.Data.Items, 1)
	item := response.Data.Items[0]
	assert.Equal(t, "family-model-1", item.ModelName)
	assert.True(t, strings.HasPrefix(item.CatalogKey, "m_"), "详情路由键按模型名编码")
	assert.Equal(t, 10, item.ID, "元数据取命中的前缀声明行")
	require.NotNil(t, item.Pricing, "列表条目带美元单价")
	assert.Contains(t, item.Capabilities, "function_calling")
	require.Len(t, item.Channels, 1)
	assert.Equal(t, 7, item.Channels[0].ID)
	assert.Equal(t, 1.5, item.Channels[0].CostMultiplier)
	topP := ""
	for _, parameter := range item.Channels[0].Parameters {
		if parameter.Name == "top_p" {
			topP = parameter.Range
		}
	}
	assert.Equal(t, "0 ~ 0.5", topP, "渠道收窄后的生效能力")

	detail, err := getModelSquareItemByCatalogKey(item.CatalogKey)
	require.NoError(t, err)
	assert.Equal(t, "family-model-1", detail.ModelName)
	assert.Equal(t, item.CatalogKey, detail.CatalogKey)
	require.Len(t, detail.Channels, 1)
	assert.Equal(t, 1.5, detail.Channels[0].CostMultiplier)

	// 带斜杠的模型名必须编码成单个路径段，否则详情路由无法匹配。
	slashed := modelSquareCatalogKey("vendor/model-x")
	assert.NotContains(t, slashed, "/")
	decodedName, ok := decodeModelSquareCatalogKey(slashed)
	require.True(t, ok)
	assert.Equal(t, "vendor/model-x", decodedName)
}

func TestModelSquarePricingFromPricing(t *testing.T) {
	cacheRatio := 0.5
	tests := []struct {
		name    string
		pricing model.Pricing
		want    string
	}{
		{
			name:    "token priced with cache",
			pricing: model.Pricing{ModelRatio: 1.5, CompletionRatio: 2, CacheRatio: &cacheRatio},
			want:    `{"input_price":3,"output_price":6,"cache_read_price":1.5}`,
		},
		{
			name:    "token priced without cache",
			pricing: model.Pricing{ModelRatio: 1.5, CompletionRatio: 2},
			want:    `{"input_price":3,"output_price":6}`,
		},
		{
			name:    "per call priced ignores ratios",
			pricing: model.Pricing{QuotaType: 1, ModelPrice: 0.04, ModelRatio: 9, CompletionRatio: 9, CacheRatio: &cacheRatio},
			want:    `{"input_price":0.04,"output_price":0.04}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := common.Marshal(modelSquarePricingFromPricing(tt.pricing))
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}
