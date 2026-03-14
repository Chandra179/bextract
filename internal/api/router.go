package api

import "github.com/gin-gonic/gin"

// NewRouter wires the handler into a gin Engine and returns it.
func NewRouter(h *Handler) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())

	v1 := engine.Group("/api/v1")
	v1.POST("/extract", h.Extract)

	return engine
}
