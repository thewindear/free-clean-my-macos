package ui_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
	"github.com/thewindear/free-clean-my-macos/internal/ui"
)

// scanStart is a fixed clock so the rendered elapsed time is stable.
func scanStart() time.Time { return time.Unix(1_700_000_000, 0) }

// assertFits is the 80×24 contract: nothing is wider than the terminal, so the
// terminal never wraps a line and the layout never scrolls.
func assertFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(view, "\n"), "\n")
	if len(lines) > height {
		t.Errorf("rendered %d lines for a %d-line screen", len(lines), height)
	}
	for i, line := range lines {
		if got := runewidth.StringWidth(line); got > width {
			t.Errorf("line %d is %d cells wide, terminal is %d: %q", i, got, width, line)
		}
	}
}

func assertContains(t *testing.T, view string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q\n%s", want, view)
		}
	}
}

func TestViewFitsTheTerminal(t *testing.T) {
	longPath := "/home/Library/Caches/" + strings.Repeat("a-very-long-directory-name/", 8)

	tests := []struct {
		name  string
		build func() *ui.Model
	}{
		{"scanning", func() *ui.Model {
			m := ui.New("/home", scanStart())
			m.SetSize(80, 24)
			m.ApplyProgress(engine.Progress{Items: 12, Bytes: 340 * mb, Current: "/home/Library/Caches/ms-playwright"})
			m.Tick(scanStart().Add(2 * time.Second))
			return m
		}},
		{"ready collapsed", func() *ui.Model { return readyModel(t) }},
		{"ready expanded", func() *ui.Model {
			m := cursorOn(t, readyModel(t), ownerRow("com.jetbrains.toolbox"))
			m.HandleKey(ui.KeyEnter)
			return m
		}},
		{"filtered", func() *ui.Model {
			m := readyModel(t)
			m.HandleKey(ui.KeyFilter)
			for _, r := range "jet" {
				m.HandleKey(ui.Key(r))
			}
			return m
		}},
		{"filter matches nothing", func() *ui.Model {
			m := readyModel(t)
			m.HandleKey(ui.KeyFilter)
			for _, r := range "zzz" {
				m.HandleKey(ui.Key(r))
			}
			return m
		}},
		{"item detail", func() *ui.Model {
			m := cursorOn(t, readyModel(t), ownerRow("com.example.Slack"))
			m.HandleKey(ui.KeyEnter)
			m.HandleKey(ui.KeyDown)
			m.HandleKey(ui.KeyDetail)
			return m
		}},
		{"owner detail", func() *ui.Model {
			m := cursorOn(t, readyModel(t), ownerRow("com.arc"))
			m.HandleKey(ui.KeyDetail)
			return m
		}},
		{"empty scan", func() *ui.Model {
			m := ui.New("/home", scanStart())
			m.SetSize(80, 24)
			m.FinishScan(engine.Report{}, nil)
			return m
		}},
		{"cancelled scan", func() *ui.Model {
			m := ui.New("/home", scanStart())
			m.SetSize(80, 24)
			m.FinishScan(fixtureReport(), context.Canceled)
			return m
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := tt.build().View()
			assertFits(t, view, 80, 24)
		})
	}

	// A path far longer than the screen still renders inside it.
	t.Run("very long path", func(t *testing.T) {
		report := engine.Report{Owners: []engine.Owner{{
			ID: "com.example.Long", Name: "Long", Total: 20 * mb,
			Items: []engine.Item{{
				Path: longPath, Size: 20 * mb, Kind: rules.KindCache,
				Risk: rules.RiskSafe, Rebuild: rules.RebuildLow, Rule: "caches-generic",
				Note: "application caches; the app rebuilds them on next use",
			}},
		}}, Summary: engine.Summary{Items: 1, TotalBytes: 20 * mb}}
		m := ui.New("/home", scanStart())
		m.SetSize(80, 24)
		m.FinishScan(report, nil)
		m.HandleKey(ui.KeyEnter) // expand
		assertFits(t, m.View(), 80, 24)
		m.HandleKey(ui.KeyDown)
		m.HandleKey(ui.KeyDetail)
		assertFits(t, m.View(), 80, 24)
	})

	// Narrow and short terminals must not panic or overflow either.
	for _, size := range [][2]int{{40, 12}, {40, 5}, {30, 3}} {
		t.Run(fmt.Sprintf("narrow %dx%d", size[0], size[1]), func(t *testing.T) {
			m := readyModel(t)
			m.SetSize(size[0], size[1])
			assertFits(t, m.View(), size[0], size[1])
		})
	}
}

func TestViewShowsTheStatsBarAndSelection(t *testing.T) {
	m := readyModel(t)
	view := m.View()
	assertContains(t, view, "fcm", "6 items", "338.0 MB", "selected 1", "120.0 MB")
}

func TestViewShowsKeyHints(t *testing.T) {
	view := readyModel(t).View()
	assertContains(t, view, "space toggle", "/ filter", "d detail", "q quit")

	scanning := ui.New("/home", scanStart())
	scanning.SetSize(80, 24)
	assertContains(t, scanning.View(), "esc cancel")

	filtering := readyModel(t)
	filtering.HandleKey(ui.KeyFilter)
	assertContains(t, filtering.View(), "enter apply", "esc clear")
}

func TestViewFoldsSmallItems(t *testing.T) {
	m := cursorOn(t, readyModel(t), ownerRow("com.jetbrains.toolbox"))
	m.HandleKey(ui.KeyEnter)
	view := m.View()
	assertContains(t, view, "3 other items", "10.0 MB")
	if strings.Contains(view, "JetBrains/one") {
		t.Errorf("folded items must not be listed individually:\n%s", view)
	}

	m.HandleKey(ui.KeyDown)
	m.HandleKey(ui.KeyDown)
	m.HandleKey(ui.KeyEnter)
	assertContains(t, m.View(), "JetBrains/one", "JetBrains/two", "JetBrains/three")
}

func TestViewShowsTheFilterPrompt(t *testing.T) {
	m := readyModel(t)
	m.HandleKey(ui.KeyFilter)
	for _, r := range "jet" {
		m.HandleKey(ui.Key(r))
	}
	assertContains(t, m.View(), "filter: jet")

	m.HandleKey(ui.KeyEsc)
	if strings.Contains(m.View(), "filter:") {
		t.Errorf("clearing the filter left the prompt behind:\n%s", m.View())
	}
}

func TestViewShowsTheScanProgress(t *testing.T) {
	m := ui.New("/home", scanStart())
	m.SetSize(80, 24)
	m.ApplyProgress(engine.Progress{Items: 12, Bytes: 340 * mb, Skipped: 3, Current: "/home/Library/Caches/Arc"})
	m.Tick(scanStart().Add(2 * time.Second))
	view := m.View()
	assertContains(t, view, "scanning", "12 items", "340.0 MB", "3 skipped", "~/Library/Caches/Arc", "2.0s")
}

func TestViewShowsTheCancelledNotice(t *testing.T) {
	m := ui.New("/home", scanStart())
	m.SetSize(80, 24)
	m.FinishScan(fixtureReport(), context.Canceled)
	assertContains(t, m.View(), "cancelled")
}

func TestDetailViewShowsTheWholeAbsolutePath(t *testing.T) {
	path := "/home/Library/Caches/" + strings.Repeat("deeply/nested/", 10) + "blob.bin"
	report := engine.Report{Owners: []engine.Owner{{
		ID: "com.example.Long", Name: "Long", Total: 20 * mb,
		Items: []engine.Item{{
			Path: path, Size: 20 * mb, Kind: rules.KindCache,
			Risk: rules.RiskSafe, Rebuild: rules.RebuildHigh, Rule: "caches-jetbrains",
			Note: "IDE indexes; reindexing a large project takes hours",
		}},
	}}, Summary: engine.Summary{Items: 1, TotalBytes: 20 * mb}}

	m := ui.New("/home", scanStart())
	m.SetSize(80, 24)
	m.FinishScan(report, nil)
	m.HandleKey(ui.KeyEnter)
	m.HandleKey(ui.KeyDown)
	m.HandleKey(ui.KeyDetail)

	view := m.View()
	assertFits(t, view, 80, 24)
	assertContains(t, view, "Junk Item", "caches-jetbrains", "hours", "high", "large download")

	// The path wraps, so compare with all whitespace removed: nothing is lost.
	flat := strings.Join(strings.Fields(view), "")
	if !strings.Contains(flat, strings.Join(strings.Fields(path), "")) {
		t.Errorf("the detail panel does not show the whole absolute path:\n%s", view)
	}
}

func TestViewKeepsTheFocusedRowOnScreen(t *testing.T) {
	// One Owner with 60 large items: far more rows than the 18 a 24-line
	// screen has room for.
	items := make([]engine.Item, 60)
	for i := range items {
		items[i] = engine.Item{
			Path: fmt.Sprintf("/home/Library/Caches/Big/item-%02d", i), Size: 20 * mb,
			Kind: rules.KindCache, Risk: rules.RiskSafe, Rebuild: rules.RebuildLow, Rule: "caches-generic",
		}
	}
	report := engine.Report{Owners: []engine.Owner{{
		ID: "com.example.Big", Name: "Big", Total: 60 * 20 * mb, Items: items,
	}}, Summary: engine.Summary{Items: 60, TotalBytes: 60 * 20 * mb}}

	m := ui.New("/home", scanStart())
	m.SetSize(80, 24)
	m.FinishScan(report, nil)
	m.HandleKey(ui.KeyEnter) // expand the Owner

	first := m.View()
	assertContains(t, first, "item-00")

	m.HandleKey(ui.KeyEnd)
	last := m.View()
	assertContains(t, last, "item-59")
	if strings.Contains(last, "item-00") {
		t.Errorf("the list should have scrolled past the first item:\n%s", last)
	}
	assertFits(t, last, 80, 24)

	m.HandleKey(ui.KeyHome)
	assertContains(t, m.View(), "item-00")
}
