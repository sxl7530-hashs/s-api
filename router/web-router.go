package router

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

// WebAssets holds the embedded dashboard frontend assets.
type WebAssets struct {
	BuildFS          fs.FS
	IndexPage        []byte
	ClassicBuildFS   fs.FS
	ClassicIndexPage []byte
}

func SetWebRouter(router *gin.Engine, assets WebAssets, pluginDispatcher gin.HandlerFunc) {
	frontendFS := common.EmbedFolder(assets.BuildFS, "web/dist")
	classicFS := common.EmbedFolder(assets.ClassicBuildFS, "web/classic/dist")
	classicFileServer := http.StripPrefix("/classic", http.FileServer(classicFS))
	classicHandlers := []gin.HandlerFunc{
		middleware.RouteTag("web"),
		gzip.Gzip(gzip.DefaultCompression),
		middleware.GlobalWebRateLimit(),
		middleware.Cache(),
		func(c *gin.Context) {
			filePath := strings.TrimPrefix(c.Request.URL.Path, "/classic")
			if filePath != "" && filePath != "/" && classicFS.Exists("", filePath) {
				classicFileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.ClassicIndexPage)
		},
	}
	router.GET("/classic", classicHandlers...)
	router.GET("/classic/*filepath", classicHandlers...)
	router.HEAD("/classic", classicHandlers...)
	router.HEAD("/classic/*filepath", classicHandlers...)

	router.NoRoute(
		pluginDispatcher,
		middleware.RouteTag("web"),
		gzip.Gzip(gzip.DefaultCompression),
		middleware.GlobalWebRateLimit(),
		middleware.Cache(),
		static.Serve("/", frontendFS),
		func(c *gin.Context) {
			if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
				controller.RelayNotFound(c)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.IndexPage)
		},
	)
}
