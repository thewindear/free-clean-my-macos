package ui_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thewindear/free-clean-my-macos/internal/clean"
	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
	"github.com/thewindear/free-clean-my-macos/internal/ui"
)

const gb = 1024 * mb

// cleanReport is one Owner of each interesting shape: cheap and safe, an orphan
// that needs a human decision, and one item over the confirmation threshold.
func cleanReport() engine.Report {
	return engine.Report{
		Schema: 1,
		Owners: []engine.Owner{
			{ID: "com.example.Slack", Name: "Slack", Total: 12 * mb, Items: []engine.Item{{
				Path: "/home/Library/Caches/com.example.Slack", Size: 12 * mb,
				Kind: rules.KindCache, Risk: rules.RiskSafe, Rebuild: rules.RebuildLow, Recommended: true,
				Rule: "caches-generic", Note: "application caches; the app rebuilds them on next use",
			}}},
			{ID: "com.example.Gone", Name: "com.example.Gone", Total: 3 * mb, Items: []engine.Item{{
				Path: "/home/Library/Caches/com.example.Gone", Size: 3 * mb,
				Kind: rules.KindOrphan, Risk: rules.RiskCaution, Rebuild: rules.RebuildNone,
				Rule: "caches-generic",
			}}},
			{ID: "com.example.Big", Name: "Big", Total: 6 * gb, Items: []engine.Item{{
				Path: "/home/Library/Caches/com.example.Big", Size: 6 * gb,
				Kind: rules.KindCache, Risk: rules.RiskSafe, Rebuild: rules.RebuildLow,
				Rule: "caches-generic",
			}}},
		},
		Summary: engine.Summary{Items: 3, TotalBytes: 12*mb + 3*mb + 6*gb},
	}
}

func cleanReady(t *testing.T) *ui.Model {
	t.Helper()
	m := ui.New("/home", time.Unix(1_700_000_000, 0))
	m.SetSize(80, 24)
	m.FinishScan(cleanReport(), nil)
	return m
}

func candidate(path string, size int64, risk rules.Risk, rebuild rules.Rebuild) clean.Candidate {
	return clean.Candidate{Path: path, Size: size, Owner: "com.example.App", Risk: risk, Rebuild: rebuild}
}

// --- the confirmation gate ------------------------------------------------

func TestConfirmDoesNotGateACheapSafeSelection(t *testing.T) {
	got := ui.Confirm([]clean.Candidate{
		candidate("/a", 12*mb, rules.RiskSafe, rules.RebuildLow),
		candidate("/b", 200*mb, rules.RiskSafe, rules.RebuildNone),
	}, false)
	if got.Required {
		t.Fatalf("cheap safe items must not need a confirmation: %+v", got)
	}
}

func TestConfirmGatesOnOneLargeItem(t *testing.T) {
	big := candidate("/Library/Caches/Big", 6*gb, rules.RiskSafe, rules.RebuildLow)
	got := ui.Confirm([]clean.Candidate{candidate("/a", mb, rules.RiskSafe, rules.RebuildLow), big}, false)
	if !got.Required {
		t.Fatal("a single Junk Item of 6 GB must trigger the gate")
	}
	if len(got.Items) != 2 || got.Bytes != mb+6*gb {
		t.Errorf("the gate must show the whole selection: %+v", got)
	}
	if len(got.Reasons) == 0 || !strings.Contains(strings.Join(got.Reasons, " "), "5") {
		t.Errorf("reasons should name the 5 GB threshold: %+v", got.Reasons)
	}
	for _, it := range got.Items {
		if it.Path == big.Path {
			return
		}
	}
	t.Errorf("the gate must name the large item: %+v", got.Items)
}

func TestConfirmThresholdBoundary(t *testing.T) {
	tests := []struct {
		size int64
		want bool
	}{
		{5 * gb, true},
		{5*gb - 1, false},
	}
	for _, tt := range tests {
		got := ui.Confirm([]clean.Candidate{candidate("/a", tt.size, rules.RiskSafe, rules.RebuildLow)}, false)
		if got.Required != tt.want {
			t.Errorf("size %d exactly at the threshold: required = %v, want %v", tt.size, got.Required, tt.want)
		}
	}
}

func TestConfirmGatesOnACautionItem(t *testing.T) {
	got := ui.Confirm([]clean.Candidate{
		candidate("/a", mb, rules.RiskSafe, rules.RebuildLow),
		candidate("/b", mb, rules.RiskCaution, rules.RebuildNone),
	}, false)
	if !got.Required {
		t.Fatal("a caution item must trigger the gate whatever its size")
	}
	if !strings.Contains(strings.ToLower(strings.Join(got.Reasons, " ")), "caution") {
		t.Errorf("reasons should say why: %+v", got.Reasons)
	}
}

func TestConfirmGatesOnPermanentDelete(t *testing.T) {
	got := ui.Confirm([]clean.Candidate{candidate("/a", mb, rules.RiskSafe, rules.RebuildLow)}, true)
	if !got.Required {
		t.Fatal("Permanent Delete must always trigger the gate")
	}
	if !strings.Contains(strings.ToLower(strings.Join(got.Reasons, " ")), "permanent") {
		t.Errorf("reasons should name Permanent Delete: %+v", got.Reasons)
	}
}

// --- cleaning from the list ----------------------------------------------

func TestCleanStartsImmediatelyWithoutAGate(t *testing.T) {
	m := cleanReady(t)
	if got := m.HandleKey(ui.KeyClean); got != ui.ActionClean {
		t.Fatalf("c on a cheap safe selection = %v, want clean", got)
	}
	if !m.Cleaning() {
		t.Fatal("the model is not cleaning")
	}
	paths := m.CleanPaths()
	if len(paths) != 1 || paths[0] != "/home/Library/Caches/com.example.Slack" {
		t.Fatalf("clean paths = %v, want the preselected recommended item", paths)
	}
	if _, ok := m.PendingConfirmation(); ok {
		t.Error("no confirmation should be open")
	}
}

func TestCleanGatesACautionSelection(t *testing.T) {
	m := cleanReady(t)
	cursorOn(t, m, ownerRow("com.example.Big"))
	expandOwnerAt(t, m)
	m.HandleKey(ui.KeySpace) // select the 6 GB item
	if got := m.HandleKey(ui.KeyClean); got != ui.ActionNone {
		t.Fatalf("a gated selection must not clean immediately, got %v", got)
	}
	confirmation, ok := m.PendingConfirmation()
	if !ok {
		t.Fatal("the confirmation overlay did not open")
	}
	if !confirmation.Required || confirmation.Bytes == 0 || len(confirmation.Items) == 0 {
		t.Fatalf("confirmation = %+v, want the selection and its total", confirmation)
	}
	if m.Cleaning() {
		t.Error("nothing may be cleaned before the user confirms")
	}
}

// expandOwnerAt expands the Owner under the cursor.
func expandOwnerAt(t *testing.T, m *ui.Model) {
	t.Helper()
	row := m.Rows()[m.Cursor()]
	if row.Kind != ui.RowOwner {
		t.Fatalf("cursor is on %+v, want an Owner", row)
	}
	m.HandleKey(ui.KeyEnter)
}

func TestDecliningTheConfirmationChangesNothing(t *testing.T) {
	m := cleanReady(t)
	cursorOn(t, m, ownerRow("com.example.Gone"))
	expandOwnerAt(t, m)
	m.HandleKey(ui.KeySpace)
	m.HandleKey(ui.KeyClean)

	if got := m.HandleKey(ui.KeyDecline); got != ui.ActionNone {
		t.Fatalf("declining = %v, want no action", got)
	}
	if m.Cleaning() {
		t.Error("declining must not start a Cleanup")
	}
	if paths := m.CleanPaths(); len(paths) != 0 {
		t.Errorf("declining left %v ready to clean", paths)
	}
	if _, ok := m.PendingConfirmation(); ok {
		t.Error("the confirmation overlay should be closed")
	}
	if got := m.Stats(); !strings.Contains(got.Notice, "nothing") {
		t.Errorf("notice = %q, want it to say nothing was removed", got.Notice)
	}
}

func TestConfirmingRunsTheCleanup(t *testing.T) {
	m := cleanReady(t)
	cursorOn(t, m, ownerRow("com.example.Gone"))
	expandOwnerAt(t, m)
	m.HandleKey(ui.KeySpace)
	m.HandleKey(ui.KeyClean)

	if got := m.HandleKey(ui.KeyConfirm); got != ui.ActionClean {
		t.Fatalf("confirming = %v, want clean", got)
	}
	if !m.Cleaning() {
		t.Fatal("confirming should start the Cleanup")
	}
	if paths := m.CleanPaths(); len(paths) != 2 {
		t.Fatalf("clean paths = %v, want the two selected items", paths)
	}
}

func TestCleanupProgressAdvancesPerItem(t *testing.T) {
	m := cleanReady(t)
	m.HandleKey(ui.KeyClean) // the 12 MB Slack cache
	path := "/home/Library/Caches/com.example.Slack"

	m.ApplyCleanEvent(clean.Event{
		Kind: clean.EventItemStarted,
		Item: clean.Item{Candidate: candidate(path, 12*mb, rules.RiskSafe, rules.RebuildLow)},
	})
	if state, ok := m.CleanState(path); !ok || state != ui.StateInProgress {
		t.Fatalf("state after started = %v (%v), want in-progress", state, ok)
	}
	if got := m.Stats(); !got.Cleaning || got.CleanItems != 0 || got.CleanCount != 1 || got.CleanTotalBytes != 12*mb {
		t.Errorf("stats while running = %+v", got)
	}

	m.ApplyCleanEvent(clean.Event{
		Kind:    clean.EventItemFinished,
		Item:    clean.Item{Candidate: candidate(path, 12*mb, rules.RiskSafe, rules.RebuildLow), Outcome: clean.OutcomeDeleted},
		Summary: clean.Summary{Items: 1, FreedBytes: 12 * mb},
	})
	if state, ok := m.CleanState(path); !ok || state != ui.StateDone {
		t.Fatalf("state after finished = %v (%v), want done", state, ok)
	}
	got := m.Stats()
	if got.CleanItems != 1 || got.CleanBytes != 12*mb {
		t.Errorf("live counter = %d items / %d bytes, want 1 / 12 MB", got.CleanItems, got.CleanBytes)
	}
}

func TestCleanupFailureKeepsTheItemAndItsReason(t *testing.T) {
	m := cleanReady(t)
	m.HandleKey(ui.KeyClean)
	path := "/home/Library/Caches/com.example.Slack"

	m.ApplyCleanEvent(clean.Event{
		Kind: clean.EventItemFinished,
		Item: clean.Item{
			Candidate: candidate(path, 12*mb, rules.RiskSafe, rules.RebuildLow),
			Outcome:   clean.OutcomeFailed,
			Error:     "permission denied",
		},
		Summary: clean.Summary{Failed: []clean.Failure{{Path: path, Reason: "no-permission"}}},
	})
	if state, ok := m.CleanState(path); !ok || state != ui.StateFailed {
		t.Fatalf("state = %v (%v), want failed", state, ok)
	}
	if got := m.CleanFailure(path); !strings.Contains(got, "permission denied") {
		t.Errorf("failure reason = %q", got)
	}
}

func TestFinishCleanSelectsOnlyWhatFailed(t *testing.T) {
	m := cleanReady(t)
	m.HandleKey(ui.KeyClean)
	slack := "/home/Library/Caches/com.example.Slack"

	m.FinishClean(clean.Result{
		Items: []clean.Item{{
			Candidate: candidate(slack, 12*mb, rules.RiskSafe, rules.RebuildLow),
			Mode:      clean.ModeDelete,
			Outcome:   clean.OutcomeFailed,
			Error:     "no-permission",
		}},
		Summary: clean.Summary{
			Items:   0,
			Failed:  []clean.Failure{{Path: slack, Reason: "no-permission"}},
			LogPath: "/home/.local/state/free-clean-my-macos/cleanup-202607.jsonl",
		},
	}, nil)

	if m.Cleaning() {
		t.Error("the run should be over")
	}
	if items, _ := m.Selection(); items != 1 {
		t.Errorf("selection = %d items, want the failed item left selected for a retry", items)
	}
	if got := m.HandleKey(ui.KeyClean); got != ui.ActionClean {
		t.Fatalf("retrying the failed item = %v, want clean", got)
	}
	if paths := m.CleanPaths(); len(paths) != 1 || paths[0] != slack {
		t.Fatalf("retry paths = %v, want just the failed item", paths)
	}
}

func TestFinishCleanDeselectsWhatWasRemoved(t *testing.T) {
	m := cleanReady(t)
	m.HandleKey(ui.KeyClean)
	slack := "/home/Library/Caches/com.example.Slack"

	m.FinishClean(clean.Result{
		Items: []clean.Item{{
			Candidate: candidate(slack, 12*mb, rules.RiskSafe, rules.RebuildLow),
			Mode:      clean.ModeDelete,
			Outcome:   clean.OutcomeDeleted,
		}},
		Summary: clean.Summary{Items: 1, FreedBytes: 12 * mb},
	}, nil)

	if items, _ := m.Selection(); items != 0 {
		t.Errorf("selection = %d items, want nothing left selected", items)
	}
	if got := m.HandleKey(ui.KeyClean); got != ui.ActionNone {
		t.Errorf("c with nothing selected = %v, want no action", got)
	}
}

func TestRetryKeepsTheEarlierResults(t *testing.T) {
	m := cleanReady(t)
	m.HandleKey(ui.KeyToggleAll) // select everything, including the 6 GB item
	m.HandleKey(ui.KeyClean)
	if _, ok := m.PendingConfirmation(); !ok {
		t.Fatal("selecting everything should hit the gate")
	}
	m.HandleKey(ui.KeyConfirm)
	slack := "/home/Library/Caches/com.example.Slack"
	gone := "/home/Library/Caches/com.example.Gone"
	big := "/home/Library/Caches/com.example.Big"

	m.FinishClean(clean.Result{
		Items: []clean.Item{
			{Candidate: candidate(slack, 12*mb, rules.RiskSafe, rules.RebuildLow), Mode: clean.ModeDelete, Outcome: clean.OutcomeDeleted},
			{Candidate: candidate(gone, 3*mb, rules.RiskCaution, rules.RebuildNone), Mode: clean.ModeTrash, Outcome: clean.OutcomeTrashed},
			{Candidate: candidate(big, 6*gb, rules.RiskSafe, rules.RebuildLow), Mode: clean.ModeDelete, Outcome: clean.OutcomeFailed, Error: "no-permission"},
		},
		Summary: clean.Summary{
			Items:      2,
			FreedBytes: 12 * mb,
			MovedBytes: 3 * mb,
			Failed:     []clean.Failure{{Path: big, Reason: "no-permission"}},
		},
	}, nil)

	// The failed item is still over the 5 GB threshold, so a retry asks again:
	// the gate guards the item, not one particular attempt.
	if got := m.HandleKey(ui.KeyClean); got != ui.ActionNone {
		t.Fatalf("c on the failed 6 GB item = %v, want the gate first", got)
	}
	if _, ok := m.PendingConfirmation(); !ok {
		t.Fatal("a retry of a large item must still pass the gate")
	}
	if got := m.HandleKey(ui.KeyConfirm); got != ui.ActionClean {
		t.Fatalf("confirming the retry = %v, want clean", got)
	}
	if got, _ := m.CleanState(slack); got != ui.StateDone {
		t.Errorf("the item cleaned by the first run now reads %q, want done", got)
	}
	if got, _ := m.CleanState(big); got != ui.StatePending {
		t.Errorf("the item being retried reads %q, want pending", got)
	}
	if paths := m.CleanPaths(); len(paths) != 1 || paths[0] != big {
		t.Errorf("retry paths = %v, want only the failed item", paths)
	}
}

func TestCleanSummaryIsShownAfterTheRun(t *testing.T) {
	m := cleanReady(t)
	m.HandleKey(ui.KeyClean)
	m.FinishClean(clean.Result{
		Items: []clean.Item{{
			Candidate: candidate("/home/Library/Caches/com.example.Slack", 12*mb, rules.RiskSafe, rules.RebuildLow),
			Mode:      clean.ModeDelete,
			Outcome:   clean.OutcomeDeleted,
		}},
		Summary: clean.Summary{
			Items:      1,
			FreedBytes: 12 * mb,
			MovedBytes: 3 * mb,
			Failed:     []clean.Failure{{Path: "/x", Reason: "no-permission"}},
			LogPath:    "/home/.local/state/free-clean-my-macos/cleanup-202607.jsonl",
		},
	}, nil)

	summary, ok := m.CleanSummary()
	if !ok {
		t.Fatal("the Summary overlay did not open")
	}
	if summary.Summary.FreedBytes != 12*mb || summary.Summary.MovedBytes != 3*mb {
		t.Errorf("Summary = %+v, want freed and moved kept apart", summary.Summary)
	}
	if summary.Summary.LogPath == "" {
		t.Error("the Summary must carry the Cleanup Log path")
	}
	m.HandleKey(ui.KeyEsc)
	if _, ok := m.CleanSummary(); ok {
		t.Error("esc should close the Summary")
	}
}

func TestCleanupThatCouldNotStartIsReported(t *testing.T) {
	m := cleanReady(t)
	m.HandleKey(ui.KeyClean)
	m.FinishClean(clean.Result{}, context.DeadlineExceeded)

	if m.Cleaning() {
		t.Error("the run should be over")
	}
	if got := m.Stats(); !strings.Contains(got.Notice, "deadline") {
		t.Errorf("notice = %q, want the failure reason", got.Notice)
	}
	if items, _ := m.Selection(); items != 1 {
		t.Errorf("a run that never started must keep the selection, got %d items", items)
	}
}

func TestCleanIsIgnoredWhileAlreadyCleaning(t *testing.T) {
	m := cleanReady(t)
	m.HandleKey(ui.KeyClean)
	if got := m.HandleKey(ui.KeyClean); got != ui.ActionNone {
		t.Fatalf("a second c while cleaning = %v, want no action", got)
	}
}

func TestCleanWithNothingSelectedSaysSo(t *testing.T) {
	m := cleanReady(t)
	m.HandleKey(ui.KeyToggleAll) // select everything...
	m.HandleKey(ui.KeyToggleAll) // ...then clear it again
	if got := m.HandleKey(ui.KeyClean); got != ui.ActionNone {
		t.Fatalf("c with nothing selected = %v, want no action", got)
	}
	if got := m.Stats(); !strings.Contains(got.Notice, "nothing") {
		t.Errorf("notice = %q, want it to say nothing is selected", got.Notice)
	}
}
