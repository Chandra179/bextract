package api

import (
	"net/http"
	"time"

	"bextract/internal/pipeline"
	"bextract/pkg/store"
)

// Tier1ResultToResponse reconstructs a pipeline.Response from a stored Tier1Result.
func Tier1ResultToResponse(t *store.Tier1Result, req *pipeline.Request) *pipeline.Response {
	headers := make(http.Header, len(t.Headers))
	for k, v := range t.Headers {
		headers[k] = []string{v}
	}
	return &pipeline.Response{
		OriginalRequest: req,
		StatusCode:      t.StatusCode,
		Headers:         headers,
		Body:            []byte(t.Body),
		FinalURL:        t.FinalURL,
		ContentType:     t.ContentType,
		Elapsed:         time.Duration(t.ElapsedMS) * time.Millisecond,
	}
}

// Tier2ResultToAnalysis reconstructs a pipeline.AnalysisResult from a stored Tier2Result.
func Tier2ResultToAnalysis(t *store.Tier2Result) *pipeline.AnalysisResult {
	fields := make(map[string]pipeline.ExtractedField, len(t.Fields))
	for k, f := range t.Fields {
		fields[k] = pipeline.ExtractedField{
			Value:      f.Value,
			Source:     f.Source,
			Confidence: f.Confidence,
			Priority:   f.Priority,
		}
	}
	return &pipeline.AnalysisResult{
		Decision:           parseDecision(t.Decision),
		PageType:           pipeline.PageType(t.PageType),
		PageTypeConfidence: t.PageTypeConfidence,
		HollowScore:        t.HollowScore,
		TechHints: pipeline.TechHints{
			IsNextJS:     t.TechHints.IsNextJS,
			IsCloudflare: t.TechHints.IsCloudflare,
			CFChallenge:  t.TechHints.CFChallenge,
			IsJSON:       t.TechHints.IsJSON,
			IsPHP:        t.TechHints.IsPHP,
		},
		Fields:  fields,
		Elapsed: time.Duration(t.ElapsedMS) * time.Millisecond,
	}
}

func parseDecision(s string) pipeline.Decision {
	switch s {
	case "Done":
		return pipeline.DecisionDone
	case "Escalate":
		return pipeline.DecisionEscalate
	case "Abort":
		return pipeline.DecisionAbort
	case "Backoff":
		return pipeline.DecisionBackoff
	default:
		return pipeline.DecisionEscalate
	}
}
