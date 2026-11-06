package engine_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thewindear/free-clean-my-macos/internal/engine"
)

// addCache writes one cache directory of the given size into a fixture HOME.
func addCache(t *testing.T, home, name string, size int64) string {
	t.Helper()
	path := filepath.Join(home, "Library", "Caches", name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blob.bin"), bytes.Repeat([]byte("x"), int(size)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestScanEmitsProgressForEveryItem is the seam the list screen reads: the
// stats bar must show items and bytes as they are found, and it must never
// compute those numbers itself.
func TestScanEmitsProgressForEveryItem(t *testing.T) {
	home := t.TempDir()
	first := addCache(t, home, "Arc", 2*1024*1024)
	second := addCache(t, home, "com.example.Slack", 3*1024*1024)

	var events []engine.Progress
	opts := scanOptions(home, nil)
	opts.Emit = func(p engine.Progress) { events = append(events, p) }

	report, err := engine.Scan(context.Background(), opts)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(events) < report.Summary.Items {
		t.Fatalf("emitted %d progress events for %d items", len(events), report.Summary.Items)
	}

	if got := events[len(events)-1]; got.Items != report.Summary.Items || got.Bytes != report.Summary.TotalBytes {
		t.Errorf("last progress = %+v, report says %d items / %d bytes", got, report.Summary.Items, report.Summary.TotalBytes)
	}

	// Current names what is being sized right now, so an 11-second du is not a
	// frozen screen. Every candidate is announced before it is sized.
	seen := map[string]bool{}
	for _, ev := range events {
		seen[ev.Current] = true
	}
	for _, want := range []string{first, second} {
		if !seen[want] {
			t.Errorf("no progress event announced %q: %+v", want, events)
		}
	}
}

// TestProgressCountsAreCumulative pins the numbers the stats bar prints: they
// only ever grow, so the bar never jumps backwards mid-scan.
func TestProgressCountsAreCumulative(t *testing.T) {
	home := t.TempDir()
	addCache(t, home, "Arc", 2*1024*1024)
	addCache(t, home, "com.example.Slack", 3*1024*1024)

	opts := scanOptions(home, nil)
	items, totals := 0, int64(0)
	opts.Emit = func(p engine.Progress) {
		if p.Items < items || p.Bytes < totals {
			t.Fatalf("progress went backwards: %+v after %d items / %d bytes", p, items, totals)
		}
		items, totals = p.Items, p.Bytes
	}
	if _, err := engine.Scan(context.Background(), opts); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if items != 2 {
		t.Errorf("final progress counted %d items, want 2", items)
	}
}

// TestCancelledScanKeepsTheItemsFoundSoFar is what esc depends on: cancelling
// must hand back a browsable list of what was already sized, not nothing.
func TestCancelledScanKeepsTheItemsFoundSoFar(t *testing.T) {
	home := t.TempDir()
	first := addCache(t, home, "Arc", 2*1024*1024)
	second := addCache(t, home, "com.example.Slack", 3*1024*1024)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := scanOptions(home, nil)
	// One worker keeps the order fixed: with parallelism the two candidates are
	// in flight at once, and "one finished before the cancel" stops being a
	// meaningful claim.
	opts.Jobs = 1
	opts.Emit = func(p engine.Progress) {
		// Cancel while the second candidate is in flight.
		if p.Current == second {
			cancel()
		}
	}

	report, err := engine.Scan(ctx, opts)
	if err == nil {
		t.Fatal("a cancelled scan must report why it stopped")
	}
	if len(report.Owners) != 1 {
		t.Fatalf("partial report has %d owners, want the 1 finished before the cancel: %+v", len(report.Owners), report.Owners)
	}
	if got := report.Owners[0].Items[0].Path; got != first {
		t.Errorf("partial report kept %q, want %q", got, first)
	}
	if report.Summary.TotalBytes != report.Owners[0].Total {
		t.Errorf("summary.total_bytes = %d, owners say %d", report.Summary.TotalBytes, report.Owners[0].Total)
	}
}

// TestItemCarriesItsMatchedRule feeds the detail panel: the screen must be able
// to name the Scan Rule and show its explanation without re-matching anything.
func TestItemCarriesItsMatchedRule(t *testing.T) {
	home := t.TempDir()
	addCache(t, home, "Arc", 2*1024*1024)

	report, err := engine.Scan(context.Background(), scanOptions(home, nil))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	item := report.Owners[0].Items[0]
	if item.Rule != "caches-generic" {
		t.Errorf("rule = %q, want the embedded rule's name", item.Rule)
	}
	if item.Note == "" {
		t.Error("item has no rule explanation")
	}
}
