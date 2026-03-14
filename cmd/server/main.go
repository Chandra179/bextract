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

	"bextract/internal/api/router"
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

	// Allow env override for ArangoDB password.
	if pw := os.Getenv("ARANGO_PASSWORD"); pw != "" {
		cfg.ArangoDB.Password = pw
	}

	env := os.Getenv("APP_ENV")
	appLog := logger.NewLogger(env)

	ctx := context.Background()
	var st store.Store
	if cfg.ArangoDB.Enabled {
		st, err = store.NewArangoStore(ctx, cfg.ArangoDB.Host, cfg.ArangoDB.Database,
			cfg.ArangoDB.Username, cfg.ArangoDB.Password)
		if err != nil {
			log.Fatalf("failed to connect to ArangoDB at %s: %v", cfg.ArangoDB.Host, err)
		}
	} else {
		st = &store.NoopStore{}
	}

	r := router.New(cfg, appLog, st)
	r.Run("0.0.0.0:8080")
}
