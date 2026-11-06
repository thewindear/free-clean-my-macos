package clean_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thewindear/free-clean-my-macos/internal/clean"
	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

// reportFor builds the scan Report a Cleanup consumes, so a test can name a
// path that no longer exists on disk.
func reportFor(items ...engine.Item) engine.Report {
	return engine.Report{Owners: []engine.Owner{{ID: "com.example.App", Items: items}}}
}

// TestVanishedItemIsAFailureNotFreeSpace pins the honesty rule: bytes the tool
// did not touch are never counted, even when the path is simply gone.
func TestVanishedItemIsAFailureNotFreeSpace(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "Caches", "com.example.App")

	result, err := clean.Run(context.Background(), reportFor(engine.Item{
		Path:    gone,
		Size:    64 * 1024 * 1024,
		Kind:    rules.KindCache,
		Risk:    rules.RiskSafe,
		Rebuild: rules.RebuildLow,
	}), clean.Options{
		TrashDir: filepath.Join(dir, "trash"),
		StateDir: filepath.Join(dir, "state"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(result.Summary.Failed) != 1 {
		t.Fatalf("failed = %+v, want one vanished item", result.Summary.Failed)
	}
	if got := result.Summary.Failed[0].Reason; got != engine.ReasonVanished {
		t.Errorf("reason = %q, want %q", got, engine.ReasonVanished)
	}
	if result.Summary.FreedBytes != 0 || result.Summary.MovedBytes != 0 {
		t.Errorf("a vanished item freed nothing: %+v", result.Summary)
	}
	if result.Summary.Items != 0 {
		t.Errorf("items = %d, want 0 successful removals", result.Summary.Items)
	}
	if len(result.Items) != 1 || result.Items[0].Outcome != clean.OutcomeFailed {
		t.Errorf("items = %+v, want one failed entry", result.Items)
	}
}

// TestTwoItemsWithTheSameNameBothSurvive keeps the Trash from silently
// overwriting one recoverable item with another.
func TestTwoItemsWithTheSameNameBothSurvive(t *testing.T) {
	dir := t.TempDir()
	trash := filepath.Join(dir, "trash")
	first := filepath.Join(dir, "a", "Caches")
	second := filepath.Join(dir, "b", "Caches")
	for _, p := range []string{first, second} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "blob.bin"), []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Both are caution items, so both are moved rather than deleted.
	caution := engine.Item{Kind: rules.KindCache, Risk: rules.RiskCaution, Rebuild: rules.RebuildNone}
	one, two := caution, caution
	one.Path, two.Path = first, second
	one.Size, two.Size = 10, 20

	if _, err := clean.Run(context.Background(), reportFor(one, two), clean.Options{
		TrashDir: trash,
		StateDir: filepath.Join(dir, "state"),
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, name := range []string{"Caches", "Caches 2"} {
		if _, err := os.Stat(filepath.Join(trash, name)); err != nil {
			t.Errorf("expected two recoverable items in the Trash, %q is missing: %v", name, err)
		}
	}
}
