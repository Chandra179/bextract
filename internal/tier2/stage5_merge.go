package tier2

import (
	"sort"

	"bextract/internal/pipeline"
)

const minConfidence = 0.50

// scriptTagSources are the Priority values of extractors in Category A
// (script-tag sources). A hollow page with data from any of these is Done.
var scriptTagPriorities = map[int]bool{
	1: true, // JSON-LD
	2: true, // __NEXT_DATA__
	3: true, // state globals
	4: true, // inline variables
}

// merge combines extractor results into a single AnalysisResult.
// It applies the priority-merge rules and emits the final Decision.
func merge(
	results []pipeline.ExtractorResult,
	hints pipeline.TechHints,
	hollow hollowResult,
	req *pipeline.Request,
) *pipeline.AnalysisResult {
	// Filter: discard low-confidence or errored results.
	valid := results[:0]
	for _, r := range results {
		if r.Err == nil && r.Confidence >= minConfidence && len(r.Fields) > 0 {
			valid = append(valid, r)
		}
	}

	// Sort by priority ascending (lower number = higher precedence).
	sort.Slice(valid, func(i, j int) bool {
		return valid[i].Priority < valid[j].Priority
	})

	// First-write-wins merge: highest-priority source sets each field first.
	merged := make(map[string]pipeline.ExtractedField)
	hasScriptTagData := false
	for _, r := range valid {
		if scriptTagPriorities[r.Priority] {
			hasScriptTagData = true
		}
		for k, v := range r.Fields {
			if _, exists := merged[k]; !exists {
				merged[k] = pipeline.ExtractedField{
					Value:      v,
					Source:     r.Source,
					Confidence: r.Confidence,
					Priority:   r.Priority,
				}
			}
		}
	}

	// Determine decision.
	decision := decideOutcome(merged, hollow, hasScriptTagData, req)

	return &pipeline.AnalysisResult{
		Decision:    decision,
		IsHollow:    hollow.IsHollow,
		HollowScore: hollow.Score,
		Fields:      merged,
		TechHints:   hints,
	}
}

func decideOutcome(
	merged map[string]pipeline.ExtractedField,
	hollow hollowResult,
	hasScriptTagData bool,
	req *pipeline.Request,
) pipeline.Decision {
	// Hollow page with no valid extractions at all → escalate.
	if hollow.IsHollow && len(merged) == 0 {
		return pipeline.DecisionEscalate
	}

	// Hollow page but data was found in script tags → done.
	if hollow.IsHollow && hasScriptTagData {
		return pipeline.DecisionDone
	}

	// Check required fields.
	if len(req.TargetFields) > 0 {
		for _, field := range req.TargetFields {
			if _, ok := merged[field]; !ok {
				return pipeline.DecisionEscalate
			}
		}
	}

	// All required fields present (or none required).
	return pipeline.DecisionDone
}
