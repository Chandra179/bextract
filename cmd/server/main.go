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
	"context"
	"log"
	"os"

	internal "bextract/internal"
	"bextract/internal/config"
	"bextract/pkg/logger"
	"bextract/pkg/store"
)

func main() {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "./config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("failed to load config from %s: %v", configPath, err)
	}

	if host := os.Getenv("ARANGO_HOST"); host != "" {
		cfg.ArangoDB.Host = host
	}
	if pw := os.Getenv("ARANGO_PASSWORD"); pw != "" {
		cfg.ArangoDB.Password = pw
	}

	appLog := logger.NewLogger(os.Getenv("APP_ENV"))

	ctx := context.Background()
	var st store.Store

	st, err = store.NewArangoStore(ctx, cfg.ArangoDB.Host, cfg.ArangoDB.Database,
		cfg.ArangoDB.Username, cfg.ArangoDB.Password)
	if err != nil {
		log.Fatalf("failed to connect to ArangoDB at %s: %v", cfg.ArangoDB.Host, err)
	}

	if err := internal.Run(cfg, appLog, st, "0.0.0.0:8080"); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
