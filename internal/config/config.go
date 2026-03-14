package config

import (
	"os"

	"gopkg.in/yaml.v2"
)

// Config is the top-level configuration for the bextract pipeline.
type Config struct {
	Tier1    Tier1Config    `yaml:"tier1"`
	Tier2    Tier2Config    `yaml:"tier2"`
	Tier3    Tier3Config    `yaml:"tier3"`
	ArangoDB ArangoDBConfig `yaml:"arangodb"`
}

// ArangoDBConfig holds connection parameters for the ArangoDB persistence layer.
type ArangoDBConfig struct {
	Host     string `yaml:"host"`     // e.g. "http://localhost:8529"
	Database string `yaml:"database"` // e.g. "bextract"
	Username string `yaml:"username"`
	Password string `yaml:"password"` // override via ARANGO_PASSWORD env var
	Enabled  bool   `yaml:"enabled"`
}

// Tier1Config holds Tier 1 (plain HTTP fetch) tuning parameters.
type Tier1Config struct {
	TimeoutMs int `yaml:"timeout_ms"` // default 15000
}

// Tier2Config holds Tier 2 (extraction pipeline) tuning parameters.
type Tier2Config struct {
	ExtractionTimeoutMs int                       `yaml:"extraction_timeout_ms"` // default 5000
	Hollow              HollowConfig              `yaml:"hollow"`
	Merge               MergeConfig               `yaml:"merge"`
	Extractors          map[string]ExtractorConfig `yaml:"extractors"`
}

// HollowConfig holds parameters for the hollow/page-type classifier.
type HollowConfig struct {
	Threshold        float64            `yaml:"threshold"`           // 0.70
	LinkRichMinLinks int                `yaml:"link_rich_min_links"` // 10
	TinyBodyBytes    int                `yaml:"tiny_body_bytes"`     // 5120
	TextDensityRatio float64            `yaml:"text_density_ratio"`  // 0.05
	Penalties        map[string]float64 `yaml:"penalties"`
}

// MergeConfig holds parameters for the Stage 5 merge logic.
type MergeConfig struct {
	MinConfidence float64 `yaml:"min_confidence"` // 0.50
}

// ExtractorConfig allows enabling/disabling and overriding confidence for each extractor.
type ExtractorConfig struct {
	Enabled    bool    `yaml:"enabled"`
	Confidence float64 `yaml:"confidence"`
}

// Tier3Config holds Tier 3 (headless Chrome) tuning parameters.
type Tier3Config struct {
	PoolSize        int      `yaml:"pool_size"`         // 2
	RenderTimeoutMs int      `yaml:"render_timeout_ms"` // 8000
	BlockResources  []string `yaml:"block_resources"`   // stylesheet, image, media, font
	BlockDomains    []string `yaml:"block_domains"`
}

// Defaults returns a Config populated with all hardcoded defaults.
func Defaults() *Config {
	return &Config{
		Tier1: Tier1Config{
			TimeoutMs: 15000,
		},
		Tier2: Tier2Config{
			ExtractionTimeoutMs: 5000,
			Hollow: HollowConfig{
				Threshold:        0.70,
				LinkRichMinLinks: 10,
				TinyBodyBytes:    5120,
				TextDensityRatio: 0.05,
				Penalties: map[string]float64{
					"cf-challenge":    1.00,
					"captcha":         0.95,
					"noscript-message": 0.90,
					"empty-app-shell": 0.85,
					"low-text-density": 0.70,
					"tiny-body":       0.50,
				},
			},
			Merge: MergeConfig{
				MinConfidence: 0.50,
			},
			Extractors: map[string]ExtractorConfig{
				"json-ld":      {Enabled: true, Confidence: 0.95},
				"next-data":    {Enabled: true, Confidence: 0.92},
				"globals":      {Enabled: true, Confidence: 0.85},
				"inline-var":   {Enabled: true, Confidence: 0.75},
				"meta-tags":    {Enabled: true, Confidence: 0.88},
				"microdata":    {Enabled: true, Confidence: 0.82},
				"data-attr":    {Enabled: true, Confidence: 0.78},
				"hidden-input": {Enabled: true, Confidence: 0.72},
				"css-hidden":   {Enabled: true, Confidence: 0.60},
				"dom-text":     {Enabled: true, Confidence: 0.55},
			},
		},
		ArangoDB: ArangoDBConfig{
			Host:     "http://localhost:8529",
			Database: "bextract",
			Username: "root",
			Enabled:  false,
		},
		Tier3: Tier3Config{
			PoolSize:        2,
			RenderTimeoutMs: 8000,
			BlockResources:  []string{"stylesheet", "image", "media", "font"},
			BlockDomains: []string{
				"*google-analytics.com*",
				"*googletagmanager.com*",
				"*doubleclick.net*",
				"*facebook.com/tr*",
				"*hotjar.com*",
			},
		},
	}
}

// Load reads a YAML config file from path and merges it over defaults.
// If the file does not exist, defaults are returned without error.
func Load(path string) (*Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	// Ensure nested maps have defaults if missing from file.
	if cfg.Tier2.Hollow.Penalties == nil {
		cfg.Tier2.Hollow.Penalties = Defaults().Tier2.Hollow.Penalties
	}
	if cfg.Tier2.Extractors == nil {
		cfg.Tier2.Extractors = Defaults().Tier2.Extractors
	}
	return cfg, nil
}
