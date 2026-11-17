package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebRouterServesDefaultAndClassicFrontends(t *testing.T) {
	gin.SetMode(gin.TestMode)
	frontendFiles := fstest.MapFS{
		"web/dist/index.html":                {Data: []byte("default-index")},
		"web/dist/assets/default.js":         {Data: []byte("default-asset")},
		"web/classic/dist/index.html":        {Data: []byte("classic-index")},
		"web/classic/dist/assets/classic.js": {Data: []byte("classic-asset")},
	}
	server := gin.New()
	SetWebRouter(server, WebAssets{
		BuildFS:          frontendFiles,
		IndexPage:        []byte("default-index"),
		ClassicBuildFS:   frontendFiles,
		ClassicIndexPage: []byte("classic-index"),
	}, func(c *gin.Context) { c.Next() })

	tests := []struct {
		path        string
		wantBody    string
		contentType string
	}{
		{path: "/", wantBody: "default-index", contentType: "text/html; charset=utf-8"},
		{path: "/dashboard", wantBody: "default-index", contentType: "text/html; charset=utf-8"},
		{path: "/assets/default.js", wantBody: "default-asset", contentType: "text/javascript; charset=utf-8"},
		{path: "/classic", wantBody: "classic-index", contentType: "text/html; charset=utf-8"},
		{path: "/classic/console/token", wantBody: "classic-index", contentType: "text/html; charset=utf-8"},
		{path: "/classic/assets/classic.js", wantBody: "classic-asset", contentType: "text/javascript; charset=utf-8"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, tt.wantBody, response.Body.String())
			assert.Equal(t, tt.contentType, response.Header().Get("Content-Type"))
		})
	}

	headRequest := httptest.NewRequest(http.MethodHead, "/classic/assets/classic.js", nil)
	headResponse := httptest.NewRecorder()
	server.ServeHTTP(headResponse, headRequest)
	require.Equal(t, http.StatusOK, headResponse.Code)
	assert.Empty(t, headResponse.Body.String())

	apiRequest := httptest.NewRequest(http.MethodGet, "/api/not-a-real-route", nil)
	apiResponse := httptest.NewRecorder()
	server.ServeHTTP(apiResponse, apiRequest)
	require.Equal(t, http.StatusNotFound, apiResponse.Code)
	assert.NotEqual(t, "default-index", apiResponse.Body.String())
	assert.NotEqual(t, "classic-index", apiResponse.Body.String())
}
