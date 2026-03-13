// @title           bextract API
// @version         1.0
// @description     Multi-tier web data extraction pipeline. Tier 1 performs plain static HTTP fetches.
// @termsOfService  http://swagger.io/terms/

// @contact.name   API Support
// @contact.url    http://github.com/bextract

// @license.name  MIT

// @host      localhost:8080
// @BasePath  /api/v1

// @schemes http https
package main

import (
	"bextract/internal/api/router"
)

func main() {
	r := router.New()
	r.Run(":8080")
}
