package router

import (
	tier1handler "bextract/internal/api/tier1"
	_ "bextract/docs" // generated Swagger docs

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginswagger "github.com/swaggo/gin-swagger"
)

// New builds and returns the root Gin engine with all routes registered.
func New() *gin.Engine {
	r := gin.Default()

	// Swagger UI at /swagger/index.html
	r.GET("/swagger/*any", ginswagger.WrapHandler(swaggerfiles.Handler))

	t1 := tier1handler.New(0) // 0 → 15 s default timeout

	v1 := r.Group("/api/v1")
	{
		v1.POST("/tier1/fetch", t1.Fetch)
	}

	return r
}
