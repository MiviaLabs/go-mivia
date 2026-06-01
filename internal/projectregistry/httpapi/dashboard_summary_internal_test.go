package httpapi

import (
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
