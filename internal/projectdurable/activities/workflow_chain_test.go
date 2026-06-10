package activities

import "testing"

func TestValidateChainShadowFieldValueAllowsSafeRefFieldsWithLongDigitRuns(t *testing.T) {
	for _, key := range []string{
		"stage_00_plan_id",
		"stage_00_ref",
		"current_pull_request_ref",
	} {
		if err := validateChainShadowFieldValue(key, "work_plan_123456789"); err != nil {
			t.Fatalf("expected %s to accept safe ref value: %v", key, err)
		}
	}

	if err := validateChainShadowFieldValue("final_status", "work_plan_123456789"); err == nil {
		t.Fatalf("expected non-ref field to reject long digit run")
	}
}
