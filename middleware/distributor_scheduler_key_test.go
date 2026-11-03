package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetModelFromJSONBodyReadsOptionalWorkload(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m","group":"vip","workload":"coding"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	got, err := getModelFromJSONBody(c)
	require.NoError(t, err)
	require.Equal(t, &ModelRequest{Model: "m", Group: "vip", Workload: "coding"}, got)
}
