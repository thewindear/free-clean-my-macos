package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// --- clean document shape -------------------------------------------------

type cleanItemDoc struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Owner     string `json:"owner"`
	Risk      string `json:"risk"`
	Rebuild   string `json:"rebuild"`
	Mode      string `json:"mode"`
	Outcome   string `json:"outcome"`
	Error     string `json:"error"`
	Timestamp string `json:"timestamp"`
}

type failureDoc struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Error  string `json:"error"`
}

type cleanSummaryDoc struct {
	FreedBytes   int64        `json:"freed_bytes"`
	MovedBytes   int64        `json:"moved_bytes"`
	Items        int          `json:"items"`
	NotAttempted int          `json:"not_attempted"`
	Failed       []failureDoc `json:"failed"`
	LogPath      string       `json:"log_path"`
}

type cleanDoc struct {
	Schema    int             `json:"schema"`
	CleanedAt string          `json:"cleaned_at"`
	DryRun    bool            `json:"dry_run"`
	Cancelled bool            `json:"cancelled"`
	Items     []cleanItemDoc  `json:"items"`
	Summary   cleanSummaryDoc `json:"summary"`
}

// cleanJSON runs a clean and decodes its document, whatever the exit code: 0
// and 3 and 1 are all answers, not test failures.
func (f *fixture) cleanJSON(t *testing.T, args ...string) (cleanDoc, int) {
	t.Helper()
	stdout, stderr, code := f.run(t, append([]string{"clean", "--json"}, args...)...)
	if code != 0 && code != 1 && code != 3 {
		t.Fatalf("clean exit = %d (stderr: %s)", code, stderr)
	}
	var d cleanDoc
	if err := json.Unmarshal([]byte(stdout), &d); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	return d, code
}

// --- fixture helpers ------------------------------------------------------

// trashDir is a Trash inside the fixture: no test ever moves anything into the
// real ~/.Trash.
func (f *fixture) trashDir() string { return filepath.Join(f.home, "trash") }

// addOrphan creates a cache directory that looks like an uninstalled app left
// it behind: a bundle id with no matching app bundle, so risk=caution.
func (f *fixture) addOrphan(t *testing.T, bundleID string, size int64) string {
	t.Helper()
	return f.addCache(t, bundleID, size)
}

// addLockedCache creates a cache directory the Cleanup cannot empty: it stays
// readable so the scan can size it, but its contents cannot be unlinked.
func (f *fixture) addLockedCache(t *testing.T, dir string) string {
	t.Helper()
	path := f.addCache(t, dir, 1024)
	if err := os.Chmod(path, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
	return path
}

// snapshot records every path under root with its kind and size, so a dry run
// can be proven byte-identical.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		out[rel] = fmt.Sprintf("%d:%s", info.Size(), info.Mode())
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotting %s: %v", root, err)
	}
	return out
}

func cleanItemFor(t *testing.T, d cleanDoc, path string) cleanItemDoc {
	t.Helper()
	for _, it := range d.Items {
		if it.Path == path {
			return it
		}
	}
	t.Fatalf("no item for %q in %+v", path, d.Items)
	return cleanItemDoc{}
}

// readLog decodes the Cleanup Log the run reported.
func readLog(t *testing.T, path string) []cleanItemDoc {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the Cleanup Log %s: %v", path, err)
	}
	var out []cleanItemDoc
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec cleanItemDoc
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("Cleanup Log line is not JSON: %v\n%s", err, line)
		}
		out = append(out, rec)
	}
	return out
}

// --- tests ----------------------------------------------------------------

func TestCleanDryRunTouchesNothing(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	cheap := f.addCache(t, "com.example.Slack", 12*1024*1024)
	orphan := f.addOrphan(t, "com.example.Gone", 3*1024*1024)

	before := snapshot(t, f.home)
	d, code := f.cleanJSON(t, "--dry-run", "--trash-dir", f.trashDir())
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !d.DryRun {
		t.Error("dry_run = false, want true")
	}
	if len(d.Items) != 2 {
		t.Fatalf("plan has %d items, want 2 (%+v)", len(d.Items), d.Items)
	}
	for _, it := range d.Items {
		if it.Outcome != "planned" {
			t.Errorf("item %q outcome = %q, want planned", it.Path, it.Outcome)
		}
	}
	if got := cleanItemFor(t, d, cheap).Mode; got != "delete" {
		t.Errorf("cheap cache mode = %q, want delete", got)
	}
	if got := cleanItemFor(t, d, orphan).Mode; got != "trash" {
		t.Errorf("orphan mode = %q, want trash", got)
	}
	if d.Summary.FreedBytes == 0 || d.Summary.MovedBytes == 0 {
		t.Errorf("the plan must project Space Freed and Space Moved: %+v", d.Summary)
	}
	if sum := d.Summary.FreedBytes + d.Summary.MovedBytes; sum != cleanItemFor(t, d, cheap).Size+cleanItemFor(t, d, orphan).Size {
		t.Errorf("planned freed+moved = %d, items total %d", sum,
			cleanItemFor(t, d, cheap).Size+cleanItemFor(t, d, orphan).Size)
	}

	if after := snapshot(t, f.home); !reflect.DeepEqual(before, after) {
		t.Fatalf("dry run changed the fixture:\nbefore %v\nafter  %v", before, after)
	}
	if _, err := os.Lstat(f.trashDir()); err == nil {
		t.Error("dry run created the Trash directory")
	}
	if _, err := os.Stat(filepath.Join(f.home, ".local", "state")); err == nil {
		t.Error("dry run wrote into the state directory")
	}
	if d.Summary.LogPath != "" {
		t.Errorf("dry run reported a Cleanup Log path %q", d.Summary.LogPath)
	}
}

func TestCleanYesDeletesCheapItemsAndTrashesTheRest(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	cheap := f.addCache(t, "com.example.Slack", 12*1024*1024)
	orphan := f.addOrphan(t, "com.example.Gone", 3*1024*1024)

	d, code := f.cleanJSON(t, "--yes", "--trash-dir", f.trashDir())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (summary: %+v)", code, d.Summary)
	}

	if _, err := os.Lstat(cheap); !os.IsNotExist(err) {
		t.Errorf("cheap cache %q should be deleted, Lstat returned %v", cheap, err)
	}
	if _, err := os.Lstat(orphan); !os.IsNotExist(err) {
		t.Errorf("caution item %q should no longer be in the cache, Lstat returned %v", orphan, err)
	}

	trashed := filepath.Join(f.trashDir(), filepath.Base(orphan))
	if _, err := os.Stat(trashed); err != nil {
		t.Fatalf("caution item should be recoverable in the Trash: %v", err)
	}

	deleted := cleanItemFor(t, d, cheap)
	moved := cleanItemFor(t, d, orphan)
	if deleted.Outcome != "deleted" || moved.Outcome != "trashed" {
		t.Fatalf("outcomes = %q/%q, want deleted/trashed", deleted.Outcome, moved.Outcome)
	}
	if d.Summary.FreedBytes != deleted.Size {
		t.Errorf("freed_bytes = %d, want the deleted item's %d", d.Summary.FreedBytes, deleted.Size)
	}
	if d.Summary.MovedBytes != moved.Size {
		t.Errorf("moved_bytes = %d, want the trashed item's %d", d.Summary.MovedBytes, moved.Size)
	}
	if d.Summary.FreedBytes == 0 || d.Summary.MovedBytes == 0 {
		t.Errorf("Space Freed and Space Moved must both be non-zero: %+v", d.Summary)
	}
	if sum := d.Summary.FreedBytes + d.Summary.MovedBytes; sum != deleted.Size+moved.Size {
		t.Errorf("freed+moved = %d, but the items total %d", sum, deleted.Size+moved.Size)
	}
	if d.Summary.Items != 2 {
		t.Errorf("summary.items = %d, want 2", d.Summary.Items)
	}
	if len(d.Summary.Failed) != 0 {
		t.Errorf("summary.failed = %+v, want none", d.Summary.Failed)
	}
}

func TestCleanLogHasOneRecordPerItem(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	cheap := f.addCache(t, "com.example.Slack", 12*1024*1024)
	orphan := f.addOrphan(t, "com.example.Gone", 3*1024*1024)

	d, code := f.cleanJSON(t, "--yes", "--trash-dir", f.trashDir())
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.HasPrefix(d.Summary.LogPath, f.home+string(filepath.Separator)) {
		t.Fatalf("Cleanup Log %q is not inside the fixture HOME %q", d.Summary.LogPath, f.home)
	}
	records := readLog(t, d.Summary.LogPath)
	if len(records) != 2 {
		t.Fatalf("Cleanup Log has %d records, want 2", len(records))
	}

	byPath := map[string]cleanItemDoc{}
	for _, rec := range records {
		byPath[rec.Path] = rec
		if rec.Size <= 0 {
			t.Errorf("record %q has size %d", rec.Path, rec.Size)
		}
		if rec.Owner == "" {
			t.Errorf("record %q has no owner", rec.Path)
		}
		if rec.Risk != "safe" && rec.Risk != "caution" {
			t.Errorf("record %q has risk %q", rec.Path, rec.Risk)
		}
		if rec.Mode != "delete" && rec.Mode != "trash" {
			t.Errorf("record %q has mode %q", rec.Path, rec.Mode)
		}
		if rec.Outcome != "deleted" && rec.Outcome != "trashed" {
			t.Errorf("record %q has outcome %q", rec.Path, rec.Outcome)
		}
		if _, err := time.Parse(time.RFC3339, rec.Timestamp); err != nil {
			t.Errorf("record %q timestamp %q is not RFC3339: %v", rec.Path, rec.Timestamp, err)
		}
	}
	if byPath[cheap].Outcome != "deleted" {
		t.Errorf("record for the deleted item = %+v", byPath[cheap])
	}
	if byPath[orphan].Outcome != "trashed" || byPath[orphan].Mode != "trash" {
		t.Errorf("record for the moved item = %+v", byPath[orphan])
	}
}

func TestCleanLogIsAppendedAcrossRuns(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	f.addCache(t, "com.example.Slack", 12*1024*1024)
	first, code := f.cleanJSON(t, "--yes", "--trash-dir", f.trashDir())
	if code != 0 {
		t.Fatalf("first run exit = %d", code)
	}

	f.addCache(t, "com.example.Arc", 8*1024*1024)
	second, code := f.cleanJSON(t, "--yes", "--trash-dir", f.trashDir())
	if code != 0 {
		t.Fatalf("second run exit = %d", code)
	}
	if first.Summary.LogPath != second.Summary.LogPath {
		t.Fatalf("runs used different logs: %q and %q", first.Summary.LogPath, second.Summary.LogPath)
	}
	if records := readLog(t, second.Summary.LogPath); len(records) != 2 {
		t.Fatalf("Cleanup Log has %d records after two runs, want 2 appended", len(records))
	}
}

func TestCleanFailureIsReportedAndExitsOne(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	good := f.addCache(t, "com.example.Slack", 12*1024*1024)
	locked := f.addLockedCache(t, "Locked")

	d, code := f.cleanJSON(t, "--yes", "--trash-dir", f.trashDir())
	if code != 1 {
		t.Fatalf("exit = %d, want 1 for a partial failure (summary: %+v)", code, d.Summary)
	}
	if len(d.Summary.Failed) != 1 || d.Summary.Failed[0].Path != locked {
		t.Fatalf("summary.failed = %+v, want one entry for %q", d.Summary.Failed, locked)
	}
	if d.Summary.Failed[0].Reason != "no-permission" {
		t.Errorf("failure reason = %q, want no-permission", d.Summary.Failed[0].Reason)
	}
	failed := cleanItemFor(t, d, locked)
	if failed.Outcome != "failed" || failed.Error == "" {
		t.Errorf("failed item = %+v, want outcome=failed with a reason", failed)
	}
	if _, err := os.Lstat(locked); err != nil {
		t.Errorf("a failed item must be left alone, Lstat returned %v", err)
	}
	if _, err := os.Lstat(good); !os.IsNotExist(err) {
		t.Errorf("the healthy item should still be deleted, Lstat returned %v", err)
	}
	if d.Summary.FreedBytes != cleanItemFor(t, d, good).Size {
		t.Errorf("freed_bytes = %d, want only the item that was actually touched", d.Summary.FreedBytes)
	}
	if d.Summary.Items != 1 {
		t.Errorf("summary.items = %d, want 1 successful item", d.Summary.Items)
	}
	if records := readLog(t, d.Summary.LogPath); len(records) != 2 {
		t.Errorf("a failed item still needs a log record, got %d records", len(records))
	}
}

func TestCleanEmptySelectionExitsThree(t *testing.T) {
	t.Run("nothing to clean", func(t *testing.T) {
		f := newFixture(t)
		d, code := f.cleanJSON(t, "--yes", "--trash-dir", f.trashDir())
		if code != 3 {
			t.Fatalf("exit = %d, want 3", code)
		}
		if len(d.Items) != 0 {
			t.Errorf("items = %+v, want none", d.Items)
		}
		if d.Summary.LogPath != "" {
			t.Errorf("an empty run must not create a Cleanup Log, got %q", d.Summary.LogPath)
		}
	})

	t.Run("filter selects nothing", func(t *testing.T) {
		f := newFixture(t)
		f.addCache(t, "Arc", 2*1024*1024)
		_, code := f.cleanJSON(t, "--yes", "--only-kind", "log", "--trash-dir", f.trashDir())
		if code != 3 {
			t.Fatalf("exit = %d, want 3 when the filter selects nothing", code)
		}
	})
}

func TestCleanDryRunCanBeNarrowedByKind(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	cheap := f.addCache(t, "com.example.Slack", 12*1024*1024)
	f.addOrphan(t, "com.example.Gone", 3*1024*1024)

	d, code := f.cleanJSON(t, "--dry-run", "--only-kind", "cache", "--trash-dir", f.trashDir())
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(d.Items) != 1 || d.Items[0].Path != cheap {
		t.Fatalf("cache-only plan = %+v, want just %q", d.Items, cheap)
	}
}

func TestCleanUsageErrorsExitTwo(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{
		{"clean"},                                 // neither --dry-run nor --yes
		{"clean", "--dry-run", "--yes"},           // both at once
		{"clean", "--yes", "--only-kind", "junk"}, // unknown Kind
		{"clean", "--yes", "--nonsense"},          // unknown flag
		{"clean", "--yes", "extra"},               // stray argument
		{"clean", "--permanent"},                  // irreversible mode needs --yes
	} {
		_, stderr, code := f.run(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if stderr == "" {
			t.Errorf("%v: expected a usage message on stderr", args)
		}
	}
}

func TestCleanTextSummarySeparatesSpaceFreedFromSpaceMoved(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	f.addCache(t, "com.example.Slack", 12*1024*1024)
	f.addOrphan(t, "com.example.Gone", 3*1024*1024)

	stdout, stderr, code := f.run(t, "clean", "--yes", "--trash-dir", f.trashDir())
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}
	for _, want := range []string{"Space Freed", "Space Moved", "Cleanup Log", "empty the Trash"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("Summary is missing %q:\n%s", want, stdout)
		}
	}
}

func TestCleanPermanentDeletesCautionAndHighRebuildItemsOutright(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	cheap := f.addCache(t, "com.example.Slack", 12*1024*1024)
	caution := f.addOrphan(t, "com.example.Gone", 3*1024*1024)
	high := f.addCache(t, "JetBrains", 5*1024*1024)

	d, code := f.cleanJSON(t, "--yes", "--permanent", "--json", "--trash-dir", f.trashDir())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (summary: %+v)", code, d.Summary)
	}
	for _, path := range []string{cheap, caution, high} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("permanent item %q is still on disk: %v", path, err)
		}
		it := cleanItemFor(t, d, path)
		if it.Mode != "permanent" {
			t.Errorf("%q mode = %q, want permanent", path, it.Mode)
		}
		if it.Outcome != "deleted" {
			t.Errorf("%q outcome = %q, want deleted", path, it.Outcome)
		}
	}
	wantFreed := cleanItemFor(t, d, cheap).Size + cleanItemFor(t, d, caution).Size + cleanItemFor(t, d, high).Size
	if d.Summary.FreedBytes != wantFreed {
		t.Errorf("freed_bytes = %d, want all %d bytes", d.Summary.FreedBytes, wantFreed)
	}
	if d.Summary.MovedBytes != 0 {
		t.Errorf("moved_bytes = %d, want 0 under Permanent Delete", d.Summary.MovedBytes)
	}
	if _, err := os.Stat(f.trashDir()); err == nil {
		t.Error("Permanent Delete created the Trash directory")
	}

	for _, rec := range readLog(t, d.Summary.LogPath) {
		if rec.Mode != "permanent" || rec.Outcome != "deleted" {
			t.Errorf("log record = %+v, want permanent/deleted", rec)
		}
	}
}
