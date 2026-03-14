package internal

import (
	"context"

	"bextract/internal/api"
	"bextract/internal/config"
	"bextract/internal/runner"
	"bextract/internal/tier3"
	"bextract/internal/tier4"
	"bextract/pkg/logger"
	"bextract/pkg/store"
)

// Run initialises all tiers, wires the cascade runner into the HTTP handler,
// and starts the server on addr (e.g. "0.0.0.0:8080").
func Run(cfg *config.Config, log logger.Logger, st store.Store, addr string) error {
	ctx := context.Background()

	r3, err := tier3.New(cfg.Tier3, cfg.Tier2, log)
	if err != nil {
		log.Warn(ctx, "server: tier3 renderer unavailable", logger.Field{Key: "error", Value: err.Error()})
		r3 = nil
	}

	r4, err := tier4.New(cfg.Tier4, cfg.Tier2, log)
	if err != nil {
		log.Warn(ctx, "server: tier4 renderer unavailable", logger.Field{Key: "error", Value: err.Error()})
		r4 = nil
	}

	r := runner.New(cfg.Tier1, cfg.Tier2, r3, r4, log, st)
	h := api.New(r.Run)
	engine := api.NewRouter(h)

	return engine.Run(addr)
}
