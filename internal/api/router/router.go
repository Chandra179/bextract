package router

import (
	_ "bextract/docs" // generated Swagger docs
	tier1handler "bextract/internal/api/tier1"
	tier2handler "bextract/internal/api/tier2"
	tier3handler "bextract/internal/api/tier3"
	"bextract/internal/config"
	"bextract/pkg/logger"
	"bextract/pkg/store"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginswagger "github.com/swaggo/gin-swagger"
)

// New builds and returns the root Gin engine with all routes registered.
func New(cfg *config.Config, log logger.Logger, st store.Store) *gin.Engine {
	r := gin.Default()

	// Swagger UI at /swagger/index.html
	r.GET("/swagger/*any", ginswagger.WrapHandler(swaggerfiles.Handler))

	t1 := tier1handler.New(cfg.Tier1.TimeoutMs, st)
	t2 := tier2handler.New(cfg.Tier1, cfg.Tier2, log, st)

	v1 := r.Group("/api/v1")
	{
		v1.POST("/tier1/fetch", t1.Fetch)
		v1.POST("/tier2/analyze", t2.Analyze)
		if t3, err := tier3handler.New(cfg.Tier1, cfg.Tier2, cfg.Tier3, log, st); err == nil {
			v1.POST("/tier3/render", t3.Render)
		}
	}

	return r
}
