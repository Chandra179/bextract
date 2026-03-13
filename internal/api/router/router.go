package router

import (
	_ "bextract/docs" // generated Swagger docs
	tier1handler "bextract/internal/api/tier1"
	tier2handler "bextract/internal/api/tier2"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginswagger "github.com/swaggo/gin-swagger"
)

// New builds and returns the root Gin engine with all routes registered.
func New() *gin.Engine {
	r := gin.Default()

	// Swagger UI at /swagger/index.html
	r.GET("/swagger/*any", ginswagger.WrapHandler(swaggerfiles.Handler))

	t1 := tier1handler.New(5) // 0 → 15 s default timeout
	t2 := tier2handler.New(5, 5)

	v1 := r.Group("/api/v1")
	{
		v1.POST("/tier1/fetch", t1.Fetch)
		v1.POST("/tier2/fetch", t2.Analyze)
	}

	return r
}
