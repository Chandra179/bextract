package store

import (
	"context"
	"time"
)

// JobStatus represents the lifecycle stage of an extraction job.
type JobStatus string

const (
	JobStatusCreated       JobStatus = "created"
	JobStatusTier1Complete JobStatus = "tier1_complete"
	JobStatusTier2Complete JobStatus = "tier2_complete"
	JobStatusTier3Complete JobStatus = "tier3_complete"
	JobStatusTier4Complete JobStatus = "tier4_complete"
	JobStatusAborted       JobStatus = "aborted"
)

// StoredField mirrors pipeline.ExtractedField for persistence.
type StoredField struct {
	Value      string  `json:"value"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
	Priority   int     `json:"priority"`
}

// StoredTechHints mirrors pipeline.TechHints for persistence.
type StoredTechHints struct {
	IsNextJS     bool `json:"is_nextjs"`
	IsCloudflare bool `json:"is_cloudflare"`
	CFChallenge  bool `json:"cf_challenge"`
	IsJSON       bool `json:"is_json"`
	IsPHP        bool `json:"is_php"`
}

// Tier1Result is the persisted output of Tier 1.
type Tier1Result struct {
	StatusCode  int               `json:"status_code"`
	FinalURL    string            `json:"final_url"`
	ContentType string            `json:"content_type"`
	ElapsedMS   int64             `json:"elapsed_ms"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	FetchedAt   time.Time         `json:"fetched_at"`
}

// Tier2Result is the persisted output of Tier 2.
type Tier2Result struct {
	Decision           string                 `json:"decision"`
	PageType           string                 `json:"page_type"`
	PageTypeConfidence float64                `json:"page_type_confidence"`
	HollowScore        float64                `json:"hollow_score"`
	TechHints          StoredTechHints        `json:"tech_hints"`
	Fields             map[string]StoredField `json:"fields"`
	ElapsedMS          int64                  `json:"elapsed_ms"`
	AnalyzedAt         time.Time              `json:"analyzed_at"`
}

// Tier3Result is the persisted output of Tier 3.
type Tier3Result struct {
	Decision           string                 `json:"decision"`
	PageType           string                 `json:"page_type"`
	PageTypeConfidence float64                `json:"page_type_confidence"`
	HollowScore        float64                `json:"hollow_score"`
	EscalationReason   string                 `json:"escalation_reason"`
	Fields             map[string]StoredField `json:"fields"`
	ElapsedMS          int64                  `json:"elapsed_ms"`
	RenderedAt         time.Time              `json:"rendered_at"`
}

// Tier4Result is the persisted output of Tier 4.
type Tier4Result struct {
	Decision           string                 `json:"decision"`
	PageType           string                 `json:"page_type"`
	PageTypeConfidence float64                `json:"page_type_confidence"`
	HollowScore        float64                `json:"hollow_score"`
	EscalationReason   string                 `json:"escalation_reason"`
	Fields             map[string]StoredField `json:"fields"`
	ElapsedMS          int64                  `json:"elapsed_ms"`
	RenderedAt         time.Time              `json:"rendered_at"`
}

// ExtractionJob is the top-level ArangoDB document stored in the "extractions" collection.
type ExtractionJob struct {
	Key           string                 `json:"_key"`
	URL           string                 `json:"url"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
	Status        JobStatus              `json:"status"`
	Tier1         *Tier1Result           `json:"tier1,omitempty"`
	Tier2         *Tier2Result           `json:"tier2,omitempty"`
	Tier3         *Tier3Result           `json:"tier3,omitempty"`
	Tier4         *Tier4Result           `json:"tier4,omitempty"`
	FinalDecision string                 `json:"final_decision,omitempty"`
	FinalFields   map[string]StoredField `json:"final_fields,omitempty"`
}

// Store is the persistence interface for extraction jobs.
type Store interface {
	// CreateJob creates a new job document and returns its ID.
	CreateJob(ctx context.Context, url string) (jobID string, err error)
	// SaveTier1 persists the Tier 1 result for the given job (PATCH).
	SaveTier1(ctx context.Context, jobID string, r *Tier1Result) error
	// SaveTier2 persists the Tier 2 result for the given job (PATCH).
	SaveTier2(ctx context.Context, jobID string, r *Tier2Result) error
	// SaveTier3 persists the Tier 3 result for the given job (PATCH).
	SaveTier3(ctx context.Context, jobID string, r *Tier3Result) error
	// SaveTier4 persists the Tier 4 result for the given job (PATCH).
	SaveTier4(ctx context.Context, jobID string, r *Tier4Result) error
	// GetJob retrieves a job document by ID.
	GetJob(ctx context.Context, jobID string) (*ExtractionJob, error)
}
