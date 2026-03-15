package api

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "bextract/docs"
)

// NewRouter wires the handler into a gin Engine and returns it.
func NewRouter(h *Handler) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())

	engine.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	v1 := engine.Group("/api/v1")
	v1.POST("/extract", h.Extract)

	return engine
}
