package main

import (
	"fmt"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

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

func TestProjectPersistentGraphMaxOpenDerivesFromConfiguredProjects(t *testing.T) {
	projects := make([]config.Project, 0, ladybug.DefaultPebbleGraphMaxOpen+4)
	for index := 0; index < ladybug.DefaultPebbleGraphMaxOpen+4; index++ {
		id := fmt.Sprintf("project-%02d", index)
		projects = append(projects, config.Project{
			ID:             id,
			DisplayName:    "Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: id,
			GraphStorage:   projectregistry.GraphStoragePersistent,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		})
	}
	registry, err := projectregistry.NewRegistry(projects, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  t.TempDir(),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if got := projectPersistentGraphMaxOpen(registry); got != ladybug.DefaultPebbleGraphMaxOpen {
		t.Fatalf("expected cap %d, got %d", ladybug.DefaultPebbleGraphMaxOpen, got)
	}
}

func TestProjectPersistentGraphMaxOpenUsesConfiguredCountBelowCap(t *testing.T) {
	registry, err := projectregistry.NewRegistry([]config.Project{
		{
			ID:             "first",
			DisplayName:    "First",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "first",
			GraphStorage:   projectregistry.GraphStoragePersistent,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		},
		{
			ID:             "memory",
			DisplayName:    "Memory",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "memory",
			GraphStorage:   projectregistry.GraphStorageInMemory,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		},
	}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  t.TempDir(),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if got := projectPersistentGraphMaxOpen(registry); got != 1 {
		t.Fatalf("expected one persistent content graph project, got %d", got)
	}
}
