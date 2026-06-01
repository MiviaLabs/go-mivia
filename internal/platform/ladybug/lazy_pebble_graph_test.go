package ladybug

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
)

func TestLazyPebbleGraphOpensOnFirstUseAndReopensAfterLRUClose(t *testing.T) {
	ctx := context.Background()
	manager := NewPebbleGraphLRU(1)
	graph := NewLazyPebbleGraph(t.TempDir(), manager)
	if graph.IsOpenForTest() {
		t.Fatal("expected graph closed before first use")
	}
	if err := graph.PutNode(ctx, Node{
		Label:      "RepoFile",
		ID:         "project:file",
		Properties: map[string]string{"project_id": "project"},
	}); err != nil {
		t.Fatalf("put node: %v", err)
	}
	if !graph.IsOpenForTest() {
		t.Fatal("expected graph open after first use")
	}
	if err := manager.CloseAll(); err != nil {
		t.Fatalf("close all: %v", err)
	}
	if graph.IsOpenForTest() {
		t.Fatal("expected graph closed after manager close")
	}
	if _, err := graph.GetNode(ctx, "RepoFile", "project:file"); !errors.Is(err, ErrLazyPebbleGraphClosing) {
		t.Fatalf("expected closed graph to reject new leases, got %v", err)
	}
}

func TestPebbleGraphLRUClosesOldestIdleGraph(t *testing.T) {
	ctx := context.Background()
	manager := NewPebbleGraphLRU(1)
	first := NewLazyPebbleGraph(t.TempDir(), manager)
	second := NewLazyPebbleGraph(t.TempDir(), manager)
	if err := first.PutNode(ctx, Node{
		Label:      "RepoFile",
		ID:         "first:file",
		Properties: map[string]string{"project_id": "first"},
	}); err != nil {
		t.Fatalf("put first: %v", err)
	}
	if !first.IsOpenForTest() {
		t.Fatal("expected first graph open")
	}
	if err := second.PutNode(ctx, Node{
		Label:      "RepoFile",
		ID:         "second:file",
		Properties: map[string]string{"project_id": "second"},
	}); err != nil {
		t.Fatalf("put second: %v", err)
	}
	if first.IsOpenForTest() {
		t.Fatal("expected first idle graph closed by LRU")
	}
	if !second.IsOpenForTest() {
		t.Fatal("expected second graph to remain open")
	}
	if _, err := first.GetNode(ctx, "RepoFile", "first:file"); err != nil {
		t.Fatalf("expected first graph to reopen and read persisted node: %v", err)
	}
}

func TestLazyPebbleGraphBatchAbortDoesNotCommit(t *testing.T) {
	ctx := context.Background()
	graph := NewLazyPebbleGraph(t.TempDir(), NewPebbleGraphLRU(1))
	err := graph.Batch(ctx, func(batch Graph) error {
		if err := batch.PutNode(ctx, Node{
			Label:      "RepoFile",
			ID:         "project:file",
			Properties: map[string]string{"project_id": "project"},
		}); err != nil {
			return err
		}
		return errors.New("abort")
	})
	if err == nil {
		t.Fatal("expected batch error")
	}
	if _, err := graph.GetNode(ctx, "RepoFile", "project:file"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("expected aborted batch not committed, got %v", err)
	}
}

func TestPebbleGraphLRUCloseAllWaitsForActiveLease(t *testing.T) {
	ctx := context.Background()
	manager := NewPebbleGraphLRU(1)
	graph := NewLazyPebbleGraph(t.TempDir(), manager)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- graph.Batch(ctx, func(batch Graph) error {
			close(entered)
			<-release
			return batch.PutNode(ctx, Node{
				Label:      "RepoFile",
				ID:         "project:file",
				Properties: map[string]string{"project_id": "project"},
			})
		})
	}()
	<-entered
	closed := make(chan error, 1)
	go func() {
		closed <- manager.CloseAll()
	}()
	select {
	case err := <-closed:
		t.Fatalf("CloseAll returned before active lease released: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("batch: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("close all: %v", err)
	}
	if graph.IsOpenForTest() {
		t.Fatal("expected graph closed after active lease drained")
	}
	if _, err := graph.GetNode(ctx, "RepoFile", "project:file"); !errors.Is(err, ErrLazyPebbleGraphClosing) {
		t.Fatalf("expected closing graph to reject new leases, got %v", err)
	}
}

func TestPebbleGraphLRUEnforcesAfterOperationError(t *testing.T) {
	ctx := context.Background()
	manager := NewPebbleGraphLRU(1)
	first := NewLazyPebbleGraph(t.TempDir(), manager)
	second := NewLazyPebbleGraph(t.TempDir(), manager)
	if err := first.PutNode(ctx, Node{
		Label:      "RepoFile",
		ID:         "first:file",
		Properties: map[string]string{"project_id": "first"},
	}); err != nil {
		t.Fatalf("put first: %v", err)
	}

	err := second.Batch(ctx, func(batch Graph) error {
		if err := batch.PutNode(ctx, Node{
			Label:      "RepoFile",
			ID:         "second:file",
			Properties: map[string]string{"project_id": "second"},
		}); err != nil {
			return err
		}
		return errors.New("abort")
	})
	if err == nil {
		t.Fatal("expected batch error")
	}
	if first.IsOpenForTest() {
		t.Fatal("expected LRU enforcement to close oldest idle graph after operation error")
	}
	if !second.IsOpenForTest() {
		t.Fatal("expected failed operation graph to remain open as most recent idle graph")
	}
}

func TestPebbleGraphLRUDoesNotCloseActiveLease(t *testing.T) {
	ctx := context.Background()
	manager := NewPebbleGraphLRU(1)
	graph := NewLazyPebbleGraph(t.TempDir(), manager)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- graph.Batch(ctx, func(batch Graph) error {
			if err := batch.PutNode(ctx, Node{
				Label:      "RepoFile",
				ID:         "project:file",
				Properties: map[string]string{"project_id": "project"},
			}); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
	}()

	<-entered
	closed := make(chan error, 1)
	go func() { closed <- manager.CloseAll() }()
	select {
	case err := <-closed:
		t.Fatalf("CloseAll returned before active lease released: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("finish batch: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("close all after lease release: %v", err)
	}
	if diagnostics := graph.Diagnostics(); diagnostics.Open || diagnostics.CloseTotal != 1 {
		t.Fatalf("expected released graph to close, got %#v", diagnostics)
	}
}

func TestLazyPebbleGraphBootstrapDoesNotOpenUntilFirstGraphUse(t *testing.T) {
	ctx := context.Background()
	graph := NewLazyPebbleGraph(t.TempDir(), NewPebbleGraphLRU(1))
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if graph.IsOpenForTest() {
		t.Fatal("expected bootstrap to keep graph closed")
	}
	if err := graph.PutNode(ctx, Node{
		Label:      "RepoFile",
		ID:         "project:file",
		Properties: map[string]string{"project_id": "project"},
	}); err != nil {
		t.Fatalf("put node: %v", err)
	}
	if !graph.IsOpenForTest() {
		t.Fatal("expected first graph use to open graph")
	}
}

func TestPebbleGraphLRUDiagnosticsDoNotExposePaths(t *testing.T) {
	ctx := context.Background()
	manager := NewPebbleGraphLRU(2)
	graph := NewLazyPebbleGraph(t.TempDir(), manager)
	if err := graph.PutNode(ctx, Node{
		Label:      "RepoFile",
		ID:         "project:file",
		Properties: map[string]string{"project_id": "project"},
	}); err != nil {
		t.Fatalf("put node: %v", err)
	}

	diagnostics := manager.Diagnostics()
	if diagnostics.MaxOpen != 2 || diagnostics.Tracked != 1 || diagnostics.Open != 1 || diagnostics.IdleOpen != 1 || diagnostics.OpenTotal != 1 {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
}
