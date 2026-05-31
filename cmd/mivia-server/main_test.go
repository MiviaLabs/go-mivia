package main

import "testing"

func TestEffectiveInitialScanOnStartSkipsWhenRestartRecoveryQueued(t *testing.T) {
	if effectiveInitialScanOnStart(true, 3) {
		t.Fatalf("expected restart recovery scans to suppress live initial scans")
	}
}

func TestEffectiveInitialScanOnStartKeepsConfiguredValueWithoutRecovery(t *testing.T) {
	if !effectiveInitialScanOnStart(true, 0) {
		t.Fatalf("expected configured initial scan to remain enabled")
	}
	if effectiveInitialScanOnStart(false, 0) {
		t.Fatalf("expected configured disabled initial scan to remain disabled")
	}
}
