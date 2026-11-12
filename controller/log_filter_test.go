/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLogSpecialFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		query          string
		allowRetry     bool
		expectedStream bool
		expectedRetry  bool
		expectedOK     bool
		expectedStatus int
	}{
		{
			name:           "valid admin filters",
			query:          "stream_error=true&retry=true",
			allowRetry:     true,
			expectedStream: true,
			expectedRetry:  true,
			expectedOK:     true,
		},
		{
			name:           "invalid boolean",
			query:          "stream_error=yes",
			allowRetry:     true,
			expectedOK:     false,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "retry forbidden in self view",
			query:          "retry=true",
			allowRetry:     false,
			expectedOK:     false,
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			request, err := http.NewRequest(http.MethodGet, "/api/log?"+test.query, nil)
			require.NoError(t, err)
			ctx.Request = request

			streamError, retry, ok := parseLogSpecialFilters(ctx, test.allowRetry)

			assert.Equal(t, test.expectedStream, streamError)
			assert.Equal(t, test.expectedRetry, retry)
			assert.Equal(t, test.expectedOK, ok)
			if test.expectedStatus != 0 {
				assert.Equal(t, test.expectedStatus, recorder.Code)
			}
		})
	}
}

// auto_routed only narrows a search, so a value that is not a boolean must keep every log
// rather than fail the request or silently pick a side.
func TestLogFilterFromQueryReadsAutoRouted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	yes, no := true, false

	for _, test := range []struct {
		query string
		want  *bool
	}{
		{"auto_routed=true", &yes},
		{"auto_routed=false", &no},
		{"", nil},
		{"auto_routed=", nil},
		{"auto_routed=maybe", nil},
	} {
		t.Run(test.query, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			request, err := http.NewRequest(http.MethodGet, "/api/log?"+test.query, nil)
			require.NoError(t, err)
			ctx.Request = request

			assert.Equal(t, test.want, logFilterFromQuery(ctx, true).AutoRouted)
			assert.Equal(t, test.want, logFilterFromQuery(ctx, false).AutoRouted, "users filter their own logs the same way")
		})
	}
}

func TestValidateLogSpecialFilterTimeRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		streamError    bool
		retry          bool
		startTimestamp int64
		endTimestamp   int64
		expectedOK     bool
	}{
		{
			name:       "ordinary log query does not require time range",
			expectedOK: true,
		},
		{
			name:           "special filter with valid time range",
			streamError:    true,
			startTimestamp: 100,
			endTimestamp:   200,
			expectedOK:     true,
		},
		{
			name:       "retry without time range is rejected",
			retry:      true,
			expectedOK: false,
		},
		{
			name:           "reversed time range is rejected",
			streamError:    true,
			startTimestamp: 200,
			endTimestamp:   100,
			expectedOK:     false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)

			ok := validateLogSpecialFilterTimeRange(
				ctx,
				test.streamError,
				test.retry,
				test.startTimestamp,
				test.endTimestamp,
			)

			assert.Equal(t, test.expectedOK, ok)
			if !test.expectedOK {
				assert.Equal(t, http.StatusBadRequest, recorder.Code)
			}
		})
	}
}
