package clean_test

import (
	"testing"

	"github.com/thewindear/free-clean-my-macos/internal/clean"
	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

func TestModeForIsTheHybridPolicy(t *testing.T) {
	tests := []struct {
		name    string
		risk    rules.Risk
		rebuild rules.Rebuild
		want    clean.Mode
	}{
		{"safe+none is deleted outright", rules.RiskSafe, rules.RebuildNone, clean.ModeDelete},
		{"safe+low is deleted outright", rules.RiskSafe, rules.RebuildLow, clean.ModeDelete},
		{"safe+high stays recoverable", rules.RiskSafe, rules.RebuildHigh, clean.ModeTrash},
		{"caution+none stays recoverable", rules.RiskCaution, rules.RebuildNone, clean.ModeTrash},
		{"caution+low stays recoverable", rules.RiskCaution, rules.RebuildLow, clean.ModeTrash},
		{"caution+high stays recoverable", rules.RiskCaution, rules.RebuildHigh, clean.ModeTrash},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clean.ModeFor(tt.risk, tt.rebuild); got != tt.want {
				t.Fatalf("ModeFor(%s, %s) = %q, want %q", tt.risk, tt.rebuild, got, tt.want)
			}
		})
	}
}

// TestSelectNarrowsByKind pins the selection semantics: no filter means every
// Junk Item, and a filter excludes the rest, so an empty selection is a real
// answer rather than an error.
func TestSelectNarrowsByKind(t *testing.T) {
	report := engine.Report{Owners: []engine.Owner{
		{ID: "com.example.Slack", Items: []engine.Item{
			{Path: "/h/Library/Caches/Slack", Size: 12 * 1024 * 1024, Kind: rules.KindCache, Risk: rules.RiskSafe, Rebuild: rules.RebuildLow},
		}},
		{ID: "system", Items: []engine.Item{
			{Path: "/h/Library/Logs/app.log", Size: 1024, Kind: rules.KindLog, Risk: rules.RiskSafe, Rebuild: rules.RebuildNone},
		}},
	}}

	all := clean.Select(report, nil)
	if len(all) != 2 {
		t.Fatalf("unfiltered selection = %d items, want 2", len(all))
	}
	if all[0].Path != "/h/Library/Caches/Slack" {
		t.Errorf("selection is not largest-first: %+v", all)
	}
	if all[0].Owner != "com.example.Slack" {
		t.Errorf("owner = %q, want the group's id", all[0].Owner)
	}

	logs := clean.Select(report, []rules.Kind{rules.KindLog})
	if len(logs) != 1 || logs[0].Path != "/h/Library/Logs/app.log" {
		t.Fatalf("log selection = %+v", logs)
	}

	if got := clean.Select(report, []rules.Kind{rules.KindTemp}); len(got) != 0 {
		t.Fatalf("an unmatched kind must select nothing, got %+v", got)
	}
}

// TestSelectIsDeterministic keeps the plan and the Cleanup Log stable: equal
// sizes fall back to the path, so two runs over the same scan agree.
func TestSelectIsDeterministic(t *testing.T) {
	report := engine.Report{Owners: []engine.Owner{
		{ID: "a", Items: []engine.Item{
			{Path: "/z", Size: 10, Kind: rules.KindCache},
			{Path: "/a", Size: 10, Kind: rules.KindCache},
		}},
	}}
	for i := 0; i < 20; i++ {
		got := clean.Select(report, nil)
		if got[0].Path != "/a" || got[1].Path != "/z" {
			t.Fatalf("selection order is not stable: %+v", got)
		}
	}
}
