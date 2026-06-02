package httpapi

import (
	"context"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
)

func TestDashboardFileExtensionCountableRequiresEligibleSafePath(t *testing.T) {
	if !dashboardFileExtensionCountable(projectingestion.FileMetadata{
		Status:         string(projectingestion.FileStatusEligible),
		RelativePathOK: true,
		Extension:      ".go",
	}) {
		t.Fatalf("expected eligible safe file extension to be countable")
	}
	for _, file := range []projectingestion.FileMetadata{
		{Status: string(projectingestion.FileStatusSkipped), RelativePathOK: true, Extension: ".go"},
		{Status: string(projectingestion.FileStatusEligible), RelativePathOK: false, Extension: ".7dc8d75be757"},
		{Status: string(projectingestion.FileStatusSkipped), RelativePathOK: false, Extension: ".a87670a31775"},
	} {
		if dashboardFileExtensionCountable(file) {
			t.Fatalf("expected unsafe or skipped extension not to be counted: %#v", file)
		}
	}
}

func TestDashboardSymbolsUsesSingleBoundedPage(t *testing.T) {
	ingestion := &dashboardSymbolPageIngestion{
		page: projectingestion.SymbolList{
			Symbols: []projectingestion.SymbolMetadata{
				{
					Name:         "Serve",
					Kind:         string(projectingestion.SymbolKindFunction),
					PackageName:  "httpapi",
					RelativePath: "internal/projectregistry/httpapi/dashboard_summary.go",
					Extension:    ".go",
					FileID:       "file-1",
				},
			},
			NextPageToken: "next",
		},
	}
	warnings := []string{}

	got := dashboardSymbols(context.Background(), ingestion, "project-1", 42, &warnings)

	if ingestion.calls != 1 {
		t.Fatalf("ListSymbols calls = %d, want 1", ingestion.calls)
	}
	if got.SampledCount != 1 {
		t.Fatalf("SampledCount = %d, want 1", got.SampledCount)
	}
	if got.TotalCount != 42 {
		t.Fatalf("TotalCount = %d, want indexed symbol count 42", got.TotalCount)
	}
	if !got.SampleTruncated {
		t.Fatalf("SampleTruncated = false, want true")
	}
	if len(warnings) != 1 || warnings[0] != "symbols_sample_truncated" {
		t.Fatalf("warnings = %#v, want symbols_sample_truncated", warnings)
	}
}

func TestSymbolConcentrationBasisUsesSampledDenominatorWhenTotalUnknown(t *testing.T) {
	got := symbolConcentrationBasis(dashboardSymbolSummary{
		SampledCount:    5,
		TotalCount:      5,
		SampleTruncated: true,
		ByCodeArea:      []dashboardCount{{Key: "internal", Count: 5}},
	}, 0, 0, 0, 0, false)

	if got.Source != "relative_path_bucket" {
		t.Fatalf("Source = %q, want relative_path_bucket", got.Source)
	}
	if got.Denominator != "indexed_symbols" {
		t.Fatalf("Denominator = %q, want indexed_symbols", got.Denominator)
	}
	if got.DenominatorCount != 5 {
		t.Fatalf("DenominatorCount = %d, want 5", got.DenominatorCount)
	}
}

type dashboardSymbolPageIngestion struct {
	projectingestion.API
	calls int
	page  projectingestion.SymbolList
}

func (ingestion *dashboardSymbolPageIngestion) ListSymbols(context.Context, string, projectingestion.SymbolFilter, projectingestion.Pagination) (projectingestion.SymbolList, error) {
	ingestion.calls++
	return ingestion.page, nil
}
