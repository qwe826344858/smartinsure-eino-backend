package schema

import "strings"

const (
	IntentKnowledgeExplain      = "knowledge_explain"
	IntentProductQuery          = "product_query"
	IntentProductRecommendation = "product_recommendation"
	IntentClauseExplain         = "clause_explain"
	IntentComparison            = "comparison"
	IntentUnderwritingBasic     = "underwriting_basic"
	IntentOutOfScope            = "out_of_scope"
)

var validIntentValues = map[string]struct{}{
	IntentKnowledgeExplain:      {},
	IntentProductQuery:          {},
	IntentProductRecommendation: {},
	IntentClauseExplain:         {},
	IntentComparison:            {},
	IntentUnderwritingBasic:     {},
	IntentOutOfScope:            {},
}

func ValidateIntent(data any) IntentResult {
	fields, ok := data.(map[string]any)
	if !ok {
		fields = map[string]any{}
	}

	intent, _ := fields["intent"].(string)
	intent = strings.TrimSpace(intent)
	if _, ok := validIntentValues[intent]; !ok {
		intent = IntentOutOfScope
	}

	needsFollowup, ok := fields["needs_followup"].(bool)
	if !ok {
		needsFollowup = false
	}

	reason, ok := fields["reason"].(string)
	if !ok {
		reason = ""
	}

	return IntentResult{
		Intent:        intent,
		NeedsFollowup: needsFollowup,
		MissingSlots:  compactStringList(fields["missing_slots"]),
		Reason:        reason,
	}
}

func ValidateQuery(data any) QueryResult {
	fields, ok := data.(map[string]any)
	if !ok {
		fields = map[string]any{}
	}

	return QueryResult{
		Queries: compactStringList(fields["queries"]),
	}
}

func IsValidIntent(intent string) bool {
	_, ok := validIntentValues[strings.TrimSpace(intent)]
	return ok
}

func compactStringList(value any) []string {
	var raw []string
	switch typed := value.(type) {
	case []string:
		raw = typed
	case []any:
		raw = make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				raw = append(raw, s)
			}
		}
	default:
		return []string{}
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
