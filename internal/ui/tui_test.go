// This file drives the Bubble Tea shell with synthetic messages and a real
// fixture directory. There is no terminal: Bubble Tea's key and window messages
// are values, so the wiring — keypress, background Cleanup, events, Summary —
// is exercisable in a plain test.
package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thewindear/free-clean-my-macos/internal/clean"
	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

func TestScreenCleansTheSelectionAndShowsTheSummary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Cache", "com.example.App")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "blob.bin"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	report := engine.Report{Owners: []engine.Owner{{
		ID: "com.example.App", Name: "App", Total: 2048, Items: []engine.Item{{
			Path: target, Size: 2048, Kind: rules.KindCache, Risk: rules.RiskSafe,
			Rebuild: rules.RebuildLow, Recommended: true, Rule: "caches-generic",
		}},
	}}, Summary: engine.Summary{Items: 1, TotalBytes: 2048}}

	screen := newScreen(engine.Options{Home: dir}, clean.Options{
		TrashDir: filepath.Join(dir, "trash"),
		StateDir: filepath.Join(dir, "state"),
	})
	screen.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	screen.model.FinishScan(report, nil)

	// `c` on a cheap safe selection starts the Cleanup with no gate.
	if _, cmd := screen.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); cmd == nil {
		t.Fatal("c must keep the message pump alive")
	}
	if !screen.model.Cleaning() {
		t.Fatal("the Cleanup did not start")
	}

	// Pump the worker messages the shell would normally read until the Summary
	// is up.
	deadline := time.After(10 * time.Second)
	for {
		if _, ok := screen.model.CleanSummary(); ok {
			break
		}
		select {
		case msg := <-screen.msgs:
			screen.Update(msg)
		case <-deadline:
			t.Fatal("the Cleanup never finished")
		}
	}

	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("the Junk Item was not removed, Lstat returned %v", err)
	}
	if state, ok := screen.model.CleanState(target); !ok || state != StateDone {
		t.Errorf("item state = %v (%v), want done", state, ok)
	}
	summary, _ := screen.model.CleanSummary()
	if summary.Summary.FreedBytes != 2048 {
		t.Errorf("Space Freed = %d, want 2048", summary.Summary.FreedBytes)
	}
	if summary.Summary.LogPath == "" {
		t.Error("the Summary has no Cleanup Log path")
	}
	if _, err := os.Stat(summary.Summary.LogPath); err != nil {
		t.Errorf("the Cleanup Log was not written: %v", err)
	}
}

// TestScreenRetriesAFailedItem is the acceptance path: a locked item fails, the
// row stays in the list, the cause is removed, and `c` on the Summary retries
// exactly that item and succeeds.
func TestScreenRetriesAFailedItem(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Cache", "com.example.Locked")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "blob.bin"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	// An unreadable child makes RemoveAll fail part way through.
	if err := os.Chmod(target, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o755) })

	report := engine.Report{Owners: []engine.Owner{{
		ID: "com.example.Locked", Name: "Locked", Total: 2048, Items: []engine.Item{{
			Path: target, Size: 2048, Kind: rules.KindCache, Risk: rules.RiskSafe,
			Rebuild: rules.RebuildLow, Recommended: true, Rule: "caches-generic",
		}},
	}}, Summary: engine.Summary{Items: 1, TotalBytes: 2048}}

	screen := newScreen(engine.Options{Home: dir}, clean.Options{
		TrashDir: filepath.Join(dir, "trash"),
		StateDir: filepath.Join(dir, "state"),
	})
	screen.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	screen.model.FinishScan(report, nil)

	screen.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	pumpUntilDone(t, screen)

	summary, ok := screen.model.CleanSummary()
	if !ok {
		t.Fatal("the Summary did not open after the failed run")
	}
	if len(summary.Summary.Failed) != 1 {
		t.Fatalf("Summary failures = %+v, want the locked item", summary.Summary.Failed)
	}
	if state, _ := screen.model.CleanState(target); state != StateFailed {
		t.Fatalf("item state = %q, want failed", state)
	}
	if items, _ := screen.model.Selection(); items != 1 {
		t.Fatalf("selection = %d items, want the failed item left selected", items)
	}

	// Remove the cause, then retry from the Summary with `c`.
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	screen.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if !screen.model.Cleaning() {
		t.Fatal("c on the Summary should retry the failed item")
	}
	pumpUntilDone(t, screen)

	if state, _ := screen.model.CleanState(target); state != StateDone {
		t.Errorf("item state after the retry = %q, want done", state)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("the retried item is still on disk: %v", err)
	}
}

// pumpUntilDone reads worker messages until the running Cleanup finishes.
func pumpUntilDone(t *testing.T, screen *screen) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for screen.model.Cleaning() {
		select {
		case msg := <-screen.msgs:
			screen.Update(msg)
		case <-deadline:
			t.Fatal("the Cleanup never finished")
		}
	}
}

// TestScreenDeclineLeavesTheDiskAlone is the safety property of the gate: while
// the confirmation is up nothing has run, and declining runs nothing.
func TestScreenDeclineLeavesTheDiskAlone(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Cache", "com.example.Big")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	report := engine.Report{Owners: []engine.Owner{{
		ID: "com.example.Big", Name: "Big", Total: 6 * 1024 * 1024 * 1024, Items: []engine.Item{{
			Path: target, Size: 6 * 1024 * 1024 * 1024, Kind: rules.KindCache,
			Risk: rules.RiskSafe, Rebuild: rules.RebuildLow, Recommended: true,
		}},
	}}, Summary: engine.Summary{Items: 1, TotalBytes: 6 * 1024 * 1024 * 1024}}

	screen := newScreen(engine.Options{Home: dir}, clean.Options{
		TrashDir: filepath.Join(dir, "trash"),
		StateDir: filepath.Join(dir, "state"),
	})
	screen.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	screen.model.FinishScan(report, nil)

	// A 6 GB item opens the gate instead of cleaning.
	screen.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if _, ok := screen.model.PendingConfirmation(); !ok {
		t.Fatal("the gate did not open for a 6 GB item")
	}
	if screen.model.Cleaning() {
		t.Fatal("nothing may run before the user confirms")
	}

	screen.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if _, ok := screen.model.PendingConfirmation(); ok {
		t.Error("declining should close the gate")
	}
	if _, err := os.Lstat(target); err != nil {
		t.Errorf("declining must leave the item on disk: %v", err)
	}
}

func TestScreenPermanentModeGatesAndBypassesTheTrash(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Cache", "com.example.Caution")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "blob.bin"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	report := engine.Report{Owners: []engine.Owner{{
		ID: "com.example.Caution", Name: "Caution", Total: 2048, Items: []engine.Item{{
			Path: target, Size: 2048, Kind: rules.KindCache, Risk: rules.RiskSafe,
			Rebuild: rules.RebuildLow, Recommended: true, Rule: "caches-generic",
		}},
	}}, Summary: engine.Summary{Items: 1, TotalBytes: 2048}}

	screen := newScreen(engine.Options{Home: dir}, clean.Options{
		TrashDir:  filepath.Join(dir, "trash"),
		StateDir:  filepath.Join(dir, "state"),
		Permanent: true,
	})
	screen.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	screen.model.FinishScan(report, nil)

	// A cheap safe selection would normally clean immediately, but Permanent
	// Delete always stops for confirmation.
	screen.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if _, ok := screen.model.PendingConfirmation(); !ok {
		t.Fatal("--permanent did not open the confirmation gate")
	}
	if screen.model.Cleaning() {
		t.Fatal("nothing may run before the user confirms")
	}

	screen.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	pumpUntilDone(t, screen)
	summary, _ := screen.model.CleanSummary()
	if len(summary.Items) != 1 || summary.Items[0].Mode != clean.ModePermanent {
		t.Fatalf("cleanup items = %+v, want one permanent delete", summary.Items)
	}
	if summary.Summary.FreedBytes != 2048 {
		t.Errorf("Space Freed = %d, want 2048", summary.Summary.FreedBytes)
	}
	if _, err := os.Stat(filepath.Join(dir, "trash")); err == nil {
		t.Error("Permanent Delete created the Trash directory")
	}
}

func TestScreenEscCancelsTheCleanupAndShowsThePartialSummary(t *testing.T) {
	dir := t.TempDir()
	var targets []string
	for _, name := range []string{"com.example.One", "com.example.Two"} {
		target := filepath.Join(dir, "Cache", name)
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "blob.bin"), make([]byte, 2048), 0o644); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	var items []engine.Item
	for _, target := range targets {
		items = append(items, engine.Item{
			Path: target, Size: 2048, Kind: rules.KindCache, Risk: rules.RiskSafe,
			Rebuild: rules.RebuildLow, Recommended: true, Rule: "caches-generic",
		})
	}
	report := engine.Report{Owners: []engine.Owner{{
		ID: "com.example.App", Name: "App", Total: 4096, Items: items,
	}}, Summary: engine.Summary{Items: 2, TotalBytes: 4096}}

	screen := newScreen(engine.Options{Home: dir}, clean.Options{
		TrashDir: filepath.Join(dir, "trash"),
		StateDir: filepath.Join(dir, "state"),
	})
	screen.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	screen.model.FinishScan(report, nil)
	screen.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	screen.Update(tea.KeyMsg{Type: tea.KeyEsc})

	pumpUntilDone(t, screen)
	summary, ok := screen.model.CleanSummary()
	if !ok {
		t.Fatal("the partial Summary did not open after esc")
	}
	if summary.Summary.NotAttempted == 0 {
		t.Fatalf("Summary = %+v, want at least one not-attempted item", summary.Summary)
	}
	if !screen.model.CleanupIncomplete() {
		t.Error("the screen should report an incomplete Cleanup for the exit code")
	}
}
