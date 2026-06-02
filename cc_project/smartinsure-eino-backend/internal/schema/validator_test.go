package schema

import "testing"

func TestValidateIntentDefaultsInvalidValues(t *testing.T) {
	got := ValidateIntent(map[string]any{
		"intent":         "bad",
		"needs_followup": "yes",
		"missing_slots":  "age",
		"reason":         12,
	})

	if got.Intent != IntentOutOfScope {
		t.Fatalf("intent = %q, want %q", got.Intent, IntentOutOfScope)
	}
	if got.NeedsFollowup {
		t.Fatal("needs_followup should default to false")
	}
	if len(got.MissingSlots) != 0 {
		t.Fatalf("missing_slots = %#v, want empty", got.MissingSlots)
	}
	if got.Reason != "" {
		t.Fatalf("reason = %q, want empty", got.Reason)
	}
}

func TestValidateIntentKeepsValidValuesAndFiltersSlots(t *testing.T) {
	got := ValidateIntent(map[string]any{
		"intent":         " product_recommendation ",
		"needs_followup": true,
		"missing_slots":  []any{"age", "", 12, " budget "},
		"reason":         "need more info",
	})

	if got.Intent != IntentProductRecommendation {
		t.Fatalf("intent = %q, want %q", got.Intent, IntentProductRecommendation)
	}
	if !got.NeedsFollowup {
		t.Fatal("needs_followup should remain true")
	}
	if len(got.MissingSlots) != 2 || got.MissingSlots[0] != "age" || got.MissingSlots[1] != "budget" {
		t.Fatalf("missing_slots = %#v, want [age budget]", got.MissingSlots)
	}
	if got.Reason != "need more info" {
		t.Fatalf("reason = %q, want need more info", got.Reason)
	}
}

func TestValidateIntentNonMapUsesDefaults(t *testing.T) {
	got := ValidateIntent("not a json object")
	if got.Intent != IntentOutOfScope || got.NeedsFollowup || len(got.MissingSlots) != 0 || got.Reason != "" {
		t.Fatalf("ValidateIntent(non-map) = %#v, want safe defaults", got)
	}
}

func TestValidateQueryFiltersInvalidAndEmptyValues(t *testing.T) {
	got := ValidateQuery(map[string]any{
		"queries": []any{" 医疗险 ", "", 42, "重疾险"},
	})
	if len(got.Queries) != 2 || got.Queries[0] != "医疗险" || got.Queries[1] != "重疾险" {
		t.Fatalf("queries = %#v, want [医疗险 重疾险]", got.Queries)
	}
}

func TestValidateQueryFallsBackToEmptySlice(t *testing.T) {
	for _, input := range []any{
		nil,
		"not a json object",
		map[string]any{"queries": "医疗险"},
		map[string]any{"queries": []any{"", 1}},
	} {
		got := ValidateQuery(input)
		if got.Queries == nil {
			t.Fatalf("ValidateQuery(%#v).Queries is nil, want empty slice", input)
		}
		if len(got.Queries) != 0 {
			t.Fatalf("ValidateQuery(%#v).Queries = %#v, want empty", input, got.Queries)
		}
	}
}
