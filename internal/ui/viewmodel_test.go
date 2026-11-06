package ui_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
	"github.com/thewindear/free-clean-my-macos/internal/ui"
)

const mb = 1024 * 1024

// fixtureReport is three Owners: one recommended cache, one high-rebuild cache
// with three small items that have to fold, and one owner under the 10 MB floor.
func fixtureReport() engine.Report {
	return engine.Report{
		Schema: 1,
		Owners: []engine.Owner{
			{ID: "com.example.Slack", Name: "Slack", Total: 120 * mb, Items: []engine.Item{{
				Path: "/home/Library/Caches/com.example.Slack", Size: 120 * mb,
				Kind: rules.KindCache, Risk: rules.RiskSafe, Rebuild: rules.RebuildLow, Recommended: true,
				Rule: "caches-generic", Note: "application caches; the app rebuilds them on next use",
			}}},
			{ID: "com.jetbrains.toolbox", Name: "JetBrains IDEs", Total: 210 * mb, Items: []engine.Item{
				{
					Path: "/home/Library/Caches/JetBrains", Size: 200 * mb,
					Kind: rules.KindDevResidue, Risk: rules.RiskSafe, Rebuild: rules.RebuildHigh,
					Rule: "caches-jetbrains", Note: "IDE indexes; reindexing a large project takes hours",
				},
				{Path: "/home/Library/Caches/JetBrains/one", Size: 5 * mb, Kind: rules.KindDevResidue, Risk: rules.RiskSafe, Rebuild: rules.RebuildHigh},
				{Path: "/home/Library/Caches/JetBrains/two", Size: 3 * mb, Kind: rules.KindDevResidue, Risk: rules.RiskSafe, Rebuild: rules.RebuildHigh},
				{Path: "/home/Library/Caches/JetBrains/three", Size: 2 * mb, Kind: rules.KindDevResidue, Risk: rules.RiskSafe, Rebuild: rules.RebuildHigh},
			}},
			{ID: "com.arc", Name: "Arc", Total: 8 * mb, Items: []engine.Item{{
				Path: "/home/Library/Caches/Arc", Size: 8 * mb,
				Kind: rules.KindCache, Risk: rules.RiskSafe, Rebuild: rules.RebuildLow,
				Rule: "caches-generic", Note: "application caches; the app rebuilds them on next use",
			}}},
		},
		Summary: engine.Summary{Items: 6, TotalBytes: 338 * mb, Failed: []string{}},
	}
}

// readyModel is the model after a finished scan.
func readyModel(t *testing.T) *ui.Model {
	t.Helper()
	m := ui.New("/home", time.Unix(1_700_000_000, 0))
	m.SetSize(80, 24)
	m.FinishScan(fixtureReport(), nil)
	return m
}

// scanned starts the model on a loaded report and moves the cursor to the row
// whose label or path matches.
func cursorOn(t *testing.T, m *ui.Model, match func(ui.Row) bool) *ui.Model {
	t.Helper()
	for i, row := range m.Rows() {
		if match(row) {
			for m.Cursor() < i {
				m.HandleKey(ui.KeyDown)
			}
			for m.Cursor() > i {
				m.HandleKey(ui.KeyUp)
			}
			return m
		}
	}
	t.Fatalf("no row matches: %+v", m.Rows())
	return nil
}

func itemRow(path string) func(ui.Row) bool {
	return func(r ui.Row) bool { return r.Kind == ui.RowItem && r.Path == path }
}

func ownerRow(id string) func(ui.Row) bool {
	return func(r ui.Row) bool { return r.Kind == ui.RowOwner && r.OwnerID == id }
}

// --- the list -------------------------------------------------------------

func TestListShowsOwnersFromTheReport(t *testing.T) {
	m := readyModel(t)
	rows := m.Rows()
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want the 3 Owners collapsed: %+v", len(rows), rows)
	}
	for i, want := range []string{"com.example.Slack", "com.jetbrains.toolbox", "com.arc"} {
		if rows[i].Kind != ui.RowOwner || rows[i].OwnerID != want {
			t.Errorf("row %d = %+v, want Owner %q", i, rows[i], want)
		}
	}
	if rows[0].Check != ui.CheckAll {
		t.Errorf("the recommended Owner row should read as fully selected, got %v", rows[0].Check)
	}
	if rows[1].Check != ui.CheckNone {
		t.Errorf("a high-rebuild Owner must not be preselected, got %v", rows[1].Check)
	}
}

func TestExpandingAnOwnerRevealsItsItems(t *testing.T) {
	m := cursorOn(t, readyModel(t), ownerRow("com.example.Slack"))
	m.HandleKey(ui.KeyEnter)

	rows := m.Rows()
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want the Owner plus its item: %+v", len(rows), rows)
	}
	if rows[1].Kind != ui.RowItem || rows[1].Depth != 1 {
		t.Fatalf("row 1 = %+v, want the Junk Item indented under its Owner", rows[1])
	}
	if rows[1].Path != "/home/Library/Caches/com.example.Slack" {
		t.Errorf("item row path = %q", rows[1].Path)
	}

	m.HandleKey(ui.KeyEnter)
	if got := len(m.Rows()); got != 3 {
		t.Fatalf("collapsing left %d rows, want 3", got)
	}
}

func TestSmallItemsFoldIntoOneRow(t *testing.T) {
	m := cursorOn(t, readyModel(t), ownerRow("com.jetbrains.toolbox"))
	m.HandleKey(ui.KeyEnter)

	rows := m.Rows()
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 3 Owners plus the expanded Owner's two rows: %+v", len(rows), rows)
	}
	if rows[2].Kind != ui.RowItem || rows[2].Path != "/home/Library/Caches/JetBrains" {
		t.Fatalf("row 2 = %+v, want the 200 MB item", rows[2])
	}
	fold := rows[3]
	if fold.Kind != ui.RowFold || fold.Count != 3 || fold.Size != 10*mb {
		t.Fatalf("fold row = %+v, want 3 items totalling 10 MB", fold)
	}

	// enter unfolds the small items: down to the 200 MB item, down to the fold row
	m.HandleKey(ui.KeyDown)
	m.HandleKey(ui.KeyDown)
	if got := m.Rows()[m.Cursor()]; got.Kind != ui.RowFold {
		t.Fatalf("cursor did not reach the fold row: %+v", got)
	}
	m.HandleKey(ui.KeyEnter)
	rows = m.Rows()
	if len(rows) != 7 {
		t.Fatalf("unfolded rows = %d, want 7: %+v", len(rows), rows)
	}
	for _, r := range rows[3:6] {
		if r.Kind != ui.RowItem {
			t.Errorf("row %+v should be an individual Junk Item after unfolding", r)
		}
	}
}

func TestTogglingAnItemUpdatesTheSelectionTotal(t *testing.T) {
	m := cursorOn(t, readyModel(t), ownerRow("com.jetbrains.toolbox"))
	m.HandleKey(ui.KeyEnter)

	before := m.Stats()
	m.HandleKey(ui.KeyDown) // the 200 MB JetBrains item
	m.HandleKey(ui.KeySpace)
	after := m.Stats()

	if after.SelectedItems != before.SelectedItems+1 {
		t.Errorf("selected items %d -> %d, want one more", before.SelectedItems, after.SelectedItems)
	}
	if after.SelectedBytes != before.SelectedBytes+200*mb {
		t.Errorf("selected bytes %d -> %d, want +200 MB", before.SelectedBytes, after.SelectedBytes)
	}
	for _, row := range m.Rows() {
		if row.Kind == ui.RowOwner && row.OwnerID == "com.jetbrains.toolbox" && row.Check != ui.CheckSome {
			t.Errorf("the partially selected Owner should read as some, got %v", row.Check)
		}
	}

	m.HandleKey(ui.KeySpace)
	if got := m.Stats(); got.SelectedItems != before.SelectedItems || got.SelectedBytes != before.SelectedBytes {
		t.Errorf("toggling the same item twice must restore the total, got %+v want %+v", got, before)
	}
}

func TestTogglingAnOwnerSelectsAllItsItems(t *testing.T) {
	m := cursorOn(t, readyModel(t), ownerRow("com.example.Slack"))

	m.HandleKey(ui.KeyToggleOwner) // deselect
	if rows := m.Rows(); rows[0].Check != ui.CheckNone {
		t.Fatalf("Owner row check = %v, want none after toggling off", rows[0].Check)
	}
	if got := m.Stats(); got.SelectedItems != 0 || got.SelectedBytes != 0 {
		t.Fatalf("selection after deselecting the only recommended Owner = %+v", got)
	}

	m.HandleKey(ui.KeyToggleOwner) // select again
	if rows := m.Rows(); rows[0].Check != ui.CheckAll {
		t.Fatalf("Owner row check = %v, want all", rows[0].Check)
	}
	if got := m.Stats(); got.SelectedItems != 1 || got.SelectedBytes != 120*mb {
		t.Fatalf("selection = %+v, want Slack's 120 MB", got)
	}
}

func TestToggleAllCoversEveryVisibleItem(t *testing.T) {
	m := readyModel(t)
	m.HandleKey(ui.KeyToggleAll)
	all := m.Stats()
	if all.SelectedItems != 6 || all.SelectedBytes != 338*mb {
		t.Fatalf("select-all = %+v, want every one of the 6 items", all)
	}

	m.HandleKey(ui.KeyToggleAll)
	if got := m.Stats(); got.SelectedItems != 0 || got.SelectedBytes != 0 {
		t.Fatalf("second select-all should clear the selection, got %+v", got)
	}
}

// --- filtering ------------------------------------------------------------

func TestFilterNarrowsToMatchingOwners(t *testing.T) {
	m := readyModel(t)
	m.HandleKey(ui.KeyFilter)
	if !m.EditingFilter() {
		t.Fatal("/ should start filter editing")
	}
	for _, r := range "jetbrains" {
		m.HandleKey(ui.Key(r))
	}
	rows := m.Rows()
	if len(rows) == 0 {
		t.Fatal("filtering for jetbrains left nothing on screen")
	}
	for _, row := range rows {
		if row.OwnerID != "com.jetbrains.toolbox" {
			t.Fatalf("filtered rows still include %q: %+v", row.OwnerID, rows)
		}
	}

	// A filtered Owner is expanded, so the matching item is on screen.
	if rows[1].Kind != ui.RowItem {
		t.Fatalf("a filtered Owner should show its items: %+v", rows)
	}
}

func TestFilterMatchesAnItemPath(t *testing.T) {
	m := readyModel(t)
	m.HandleKey(ui.KeyFilter)
	for _, r := range "JetBrains/three" {
		m.HandleKey(ui.Key(r))
	}
	rows := m.Rows()
	if len(rows) == 0 || rows[0].OwnerID != "com.jetbrains.toolbox" {
		t.Fatalf("filtered rows = %+v, want the Owner of the matching path", rows)
	}
	for _, row := range rows {
		if row.OwnerID != "com.jetbrains.toolbox" {
			t.Fatalf("a path filter must still exclude other Owners: %+v", rows)
		}
	}
}

func TestClearingTheFilterRestoresTheList(t *testing.T) {
	m := readyModel(t)
	m.HandleKey(ui.KeyFilter)
	for _, r := range "arc" {
		m.HandleKey(ui.Key(r))
	}
	if rows := m.Rows(); len(rows) == 0 || rows[0].OwnerID != "com.arc" {
		t.Fatalf("filtered rows = %+v, want Arc", rows)
	}

	m.HandleKey(ui.KeyEsc)
	if m.Query() != "" || m.EditingFilter() {
		t.Fatalf("esc should clear and leave filter editing, query=%q editing=%v", m.Query(), m.EditingFilter())
	}
	if got := len(m.Rows()); got != 3 {
		t.Fatalf("clearing the filter left %d rows, want all 3 Owners", got)
	}
}

func TestEnterKeepsTheFilterAndLeavesEditing(t *testing.T) {
	m := readyModel(t)
	m.HandleKey(ui.KeyFilter)
	for _, r := range "slack" {
		m.HandleKey(ui.Key(r))
	}
	m.HandleKey(ui.KeyEnter)
	if m.EditingFilter() {
		t.Fatal("enter should leave filter editing")
	}
	if m.Query() != "slack" {
		t.Fatalf("query = %q, want the filter to stay applied", m.Query())
	}
	if rows := m.Rows(); len(rows) == 0 || rows[0].OwnerID != "com.example.Slack" {
		t.Fatalf("rows = %+v, want the filter still applied", rows)
	}
}

// --- detail ---------------------------------------------------------------

func TestDetailExplainsTheItem(t *testing.T) {
	m := cursorOn(t, readyModel(t), ownerRow("com.example.Slack"))
	m.HandleKey(ui.KeyEnter)
	m.HandleKey(ui.KeyDown) // the Slack item
	m.HandleKey(ui.KeyDetail)

	detail, ok := m.Detail()
	if !ok {
		t.Fatal("d should open the detail panel on a Junk Item")
	}
	text := detailText(detail)
	for _, want := range []string{
		"/home/Library/Caches/com.example.Slack", // the absolute path
		"caches-generic",                         // the Scan Rule that matched
		"application caches",                     // the rule's explanation
		"safe",                                   // Risk Level
		"fully functional",                       // what safe means
		"low",                                    // Rebuild Cost
		"seconds",                                // what low means
	} {
		if !strings.Contains(text, want) {
			t.Errorf("detail is missing %q:\n%s", want, text)
		}
	}

	m.HandleKey(ui.KeyEsc)
	if _, ok := m.Detail(); ok {
		t.Fatal("esc should close the detail panel")
	}
}

func TestDetailOnAnOwnerNamesTheOwner(t *testing.T) {
	m := cursorOn(t, readyModel(t), ownerRow("com.arc"))
	m.HandleKey(ui.KeyDetail)
	detail, ok := m.Detail()
	if !ok {
		t.Fatal("d should open the detail panel on an Owner row")
	}
	if !strings.Contains(detailText(detail), "com.arc") {
		t.Errorf("Owner detail is missing the bundle id:\n%s", detailText(detail))
	}
}

// --- progress and the scan lifecycle --------------------------------------

func TestProgressFeedsTheStatsBar(t *testing.T) {
	m := ui.New("/home", time.Unix(1_700_000_000, 0))
	m.SetSize(80, 24)

	m.ApplyProgress(engine.Progress{Items: 2, Bytes: 40 * mb, Skipped: 1, Current: "/home/Library/Caches/JetBrains"})
	m.Tick(time.Unix(1_700_000_003, 0))

	got := m.Stats()
	if got.Phase != ui.PhaseScanning {
		t.Errorf("phase = %v, want scanning", got.Phase)
	}
	if got.ItemsFound != 2 || got.BytesFound != 40*mb || got.Skipped != 1 {
		t.Errorf("stats = %+v, want the engine's progress verbatim", got)
	}
	if got.Current != "/home/Library/Caches/JetBrains" {
		t.Errorf("current = %q, want the path being sized", got.Current)
	}
	if got.Elapsed != 3*time.Second {
		t.Errorf("elapsed = %v, want 3s", got.Elapsed)
	}

	// A later tick keeps counting up.
	m.Tick(time.Unix(1_700_000_005, 0))
	if got := m.Stats().Elapsed; got != 5*time.Second {
		t.Errorf("elapsed = %v, want 5s", got)
	}
}

func TestFinishScanUsesTheEngineDuration(t *testing.T) {
	report := fixtureReport()
	report.DurationMS = 11234 // the engine already measured 11.2 s

	m := ui.New("/home", time.Unix(1_700_000_000, 0))
	m.SetSize(80, 24)
	m.FinishScan(report, nil)

	got := m.Stats()
	if got.Phase != ui.PhaseReady {
		t.Errorf("phase = %v, want ready", got.Phase)
	}
	if got.Elapsed != 11234*time.Millisecond {
		t.Errorf("elapsed = %v, want the engine's duration", got.Elapsed)
	}
	if got.ItemsFound != 6 || got.BytesFound != 338*mb {
		t.Errorf("stats = %+v, want the report's totals", got)
	}
}

func TestEscCancelsAnInFlightScanWithoutQuitting(t *testing.T) {
	m := ui.New("/home", time.Unix(1_700_000_000, 0))
	m.SetSize(80, 24)
	if got := m.HandleKey(ui.KeyEsc); got != ui.ActionCancelScan {
		t.Fatalf("esc while scanning = %v, want cancel-scan", got)
	}
	if got := m.HandleKey(ui.KeyQuit); got != ui.ActionQuit {
		t.Fatalf("q = %v, want quit", got)
	}
}

func TestCancelledScanKeepsThePartialList(t *testing.T) {
	m := ui.New("/home", time.Unix(1_700_000_000, 0))
	m.SetSize(80, 24)
	partial := fixtureReport()
	partial.Owners = partial.Owners[:1]
	partial.Summary.Items = 1
	partial.Summary.TotalBytes = 120 * mb

	m.FinishScan(partial, context.Canceled)

	got := m.Stats()
	if got.Phase != ui.PhaseReady {
		t.Errorf("phase = %v, want ready so the partial list is browsable", got.Phase)
	}
	if got.Notice == "" {
		t.Error("a cancelled scan should say so")
	}
	if len(m.Rows()) != 1 {
		t.Errorf("rows = %d, want the item found before the cancel", len(m.Rows()))
	}
}

func TestFailedScanIsReported(t *testing.T) {
	m := ui.New("/home", time.Unix(1_700_000_000, 0))
	m.SetSize(80, 24)
	m.FinishScan(engine.Report{}, errors.New("permission denied"))

	got := m.Stats()
	if got.Phase != ui.PhaseReady || !strings.Contains(got.Notice, "permission denied") {
		t.Fatalf("stats = %+v, want a ready screen that names the failure", got)
	}
}

// --- cursor ---------------------------------------------------------------

func TestCursorMovesAndClamps(t *testing.T) {
	m := readyModel(t)
	if m.Cursor() != 0 {
		t.Fatalf("cursor starts at %d, want 0", m.Cursor())
	}
	m.HandleKey(ui.KeyUp)
	if m.Cursor() != 0 {
		t.Errorf("up at the top moved to %d", m.Cursor())
	}
	m.HandleKey(ui.KeyEnd)
	if m.Cursor() != 2 {
		t.Errorf("end = %d, want the last row (2)", m.Cursor())
	}
	m.HandleKey(ui.KeyDown)
	if m.Cursor() != 2 {
		t.Errorf("down at the bottom moved to %d", m.Cursor())
	}
	m.HandleKey(ui.KeyHome)
	if m.Cursor() != 0 {
		t.Errorf("home = %d, want 0", m.Cursor())
	}
}

// detailText is the detail panel's content as one string, which is what a
// reader sees.
func detailText(d ui.Detail) string {
	var b strings.Builder
	b.WriteString(d.Title + "\n")
	for _, f := range d.Fields {
		b.WriteString(f.Label + ": " + f.Value + "\n")
	}
	return b.String()
}
