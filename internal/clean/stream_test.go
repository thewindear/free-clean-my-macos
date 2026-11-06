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

// makeItem creates a cache directory of the given size and returns the
// Candidate a Cleanup would be handed for it.
func makeItem(t *testing.T, dir, name string, size int64, risk rules.Risk, rebuild rules.Rebuild) clean.Candidate {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blob.bin"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return clean.Candidate{Path: path, Size: size, Owner: "com.example.App", Risk: risk, Rebuild: rebuild}
}

// TestRunEmitsStartedThenFinishedForEveryItem is the Cleanup Progress seam: the
// list screen marks each row from the events, so every item announces itself
// before it is removed and reports what happened after.
func TestRunEmitsStartedThenFinishedForEveryItem(t *testing.T) {
	dir := t.TempDir()
	cheap := makeItem(t, dir, "cheap", 1024, rules.RiskSafe, rules.RebuildLow)
	dear := makeItem(t, dir, "dear", 2048, rules.RiskCaution, rules.RebuildNone)

	var events []clean.Event
	result, err := clean.Run(context.Background(), []clean.Candidate{cheap, dear}, clean.Options{
		TrashDir: filepath.Join(dir, "trash"),
		StateDir: filepath.Join(dir, "state"),
		Emit:     func(ev clean.Event) { events = append(events, ev) },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(events) != 4 {
		t.Fatalf("got %d events, want started+finished for each of the 2 items: %+v", len(events), events)
	}
	for i, want := range []struct {
		kind clean.EventKind
		path string
	}{
		{clean.EventItemStarted, cheap.Path},
		{clean.EventItemFinished, cheap.Path},
		{clean.EventItemStarted, dear.Path},
		{clean.EventItemFinished, dear.Path},
	} {
		if events[i].Kind != want.kind || events[i].Item.Path != want.path {
			t.Errorf("event %d = %s/%s, want %s/%s", i, events[i].Kind, events[i].Item.Path, want.kind, want.path)
		}
	}

	if got := events[0].Item.Outcome; got != "" {
		t.Errorf("a started event should not claim an Outcome yet, got %q", got)
	}
	if got := events[1].Item.Outcome; got != clean.OutcomeDeleted {
		t.Errorf("finished outcome = %q, want deleted", got)
	}
	if got := events[3].Item.Outcome; got != clean.OutcomeTrashed {
		t.Errorf("finished outcome = %q, want trashed", got)
	}
	if got := events[3].Summary.FreedBytes; got != cheap.Size {
		t.Errorf("final event freed = %d, want the deleted item's %d", got, cheap.Size)
	}
	if got := events[3].Summary.MovedBytes; got != dear.Size {
		t.Errorf("final event moved = %d, want the trashed item's %d", got, dear.Size)
	}
	if result.Summary.FreedBytes+result.Summary.MovedBytes != events[3].Summary.FreedBytes+events[3].Summary.MovedBytes {
		t.Errorf("the last event's Summary disagrees with the Result: %+v vs %+v", events[3].Summary, result.Summary)
	}
}

// TestRunEmitsFailures keeps a failed item observable while it happens, so the
// row can turn red instead of silently vanishing from the count.
func TestRunEmitsFailures(t *testing.T) {
	dir := t.TempDir()
	gone := clean.Candidate{
		Path: filepath.Join(dir, "never-existed"), Size: 4096,
		Owner: "com.example.App", Risk: rules.RiskSafe, Rebuild: rules.RebuildLow,
	}

	var finished []clean.Event
	_, err := clean.Run(context.Background(), []clean.Candidate{gone}, clean.Options{
		TrashDir: filepath.Join(dir, "trash"),
		StateDir: filepath.Join(dir, "state"),
		Emit: func(ev clean.Event) {
			if ev.Kind == clean.EventItemFinished {
				finished = append(finished, ev)
			}
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(finished) != 1 {
		t.Fatalf("finished events = %d, want 1", len(finished))
	}
	if got := finished[0].Item.Outcome; got != clean.OutcomeFailed {
		t.Errorf("outcome = %q, want failed", got)
	}
	if got := finished[0].Item.Error; got == "" {
		t.Error("a failed event should carry the reason")
	}
	if got := finished[0].Summary; got.FreedBytes != 0 || got.MovedBytes != 0 || len(got.Failed) != 1 {
		t.Errorf("failed item counted as space: %+v", got)
	}
}

// TestRunOnlyTouchesTheGivenSelection is the retry seam: the list passes back
// exactly the items still selected, so a retry cannot re-delete what is already
// gone.
func TestRunOnlyTouchesTheGivenSelection(t *testing.T) {
	dir := t.TempDir()
	keep := makeItem(t, dir, "keep", 1024, rules.RiskSafe, rules.RebuildLow)
	remove := makeItem(t, dir, "remove", 1024, rules.RiskSafe, rules.RebuildLow)

	result, err := clean.Run(context.Background(), []clean.Candidate{remove}, clean.Options{
		TrashDir: filepath.Join(dir, "trash"),
		StateDir: filepath.Join(dir, "state"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Path != remove.Path {
		t.Fatalf("cleaned %+v, want only %q", result.Items, remove.Path)
	}
	if _, err := os.Lstat(keep.Path); err != nil {
		t.Errorf("an item outside the selection was touched: %v", err)
	}
	if _, err := os.Lstat(remove.Path); !os.IsNotExist(err) {
		t.Errorf("the selected item should be gone, Lstat returned %v", err)
	}
}

// TestRunWithAnEmptySelectionDoesNothing keeps the "nothing to do" case from
// creating a log or a Trash directory.
func TestRunWithAnEmptySelectionDoesNothing(t *testing.T) {
	dir := t.TempDir()
	var events []clean.Event
	result, err := clean.Run(context.Background(), nil, clean.Options{
		TrashDir: filepath.Join(dir, "trash"),
		StateDir: filepath.Join(dir, "state"),
		Emit:     func(ev clean.Event) { events = append(events, ev) },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("emitted %d events for an empty selection", len(events))
	}
	if len(result.Items) != 0 || result.Summary.LogPath != "" {
		t.Errorf("empty run produced %+v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); err == nil {
		t.Error("an empty run created the state directory")
	}
}

// TestSelectThenRunIsTheCLIPath pins the composition the command line uses: the
// scan Report is narrowed by Kind and then cleaned.
func TestSelectThenRunIsTheCLIPath(t *testing.T) {
	dir := t.TempDir()
	keep := makeItem(t, dir, "keep", 1024, rules.RiskSafe, rules.RebuildLow)

	report := engine.Report{Owners: []engine.Owner{{ID: "com.example.App", Items: []engine.Item{
		{Path: keep.Path, Size: keep.Size, Kind: rules.KindCache, Risk: rules.RiskSafe, Rebuild: rules.RebuildLow},
		{Path: filepath.Join(dir, "logfile"), Size: 10, Kind: rules.KindLog, Risk: rules.RiskSafe, Rebuild: rules.RebuildNone},
	}}}}

	selected := clean.Select(report, []rules.Kind{rules.KindCache})
	if len(selected) != 1 || selected[0].Path != keep.Path {
		t.Fatalf("selection = %+v, want only the cache", selected)
	}
	result, err := clean.Run(context.Background(), selected, clean.Options{
		TrashDir: filepath.Join(dir, "trash"),
		StateDir: filepath.Join(dir, "state"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v, want 1", result.Items)
	}
}
