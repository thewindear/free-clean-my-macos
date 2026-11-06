package engine_test

import (
	"testing"

	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

func TestRecommendedBoundary(t *testing.T) {
	const tenMB = 10 * 1024 * 1024
	tests := []struct {
		name       string
		risk       rules.Risk
		rebuild    rules.Rebuild
		running    bool
		recognized bool
		size       int64
		want       bool
	}{
		{"safe+low+idle+big", rules.RiskSafe, rules.RebuildLow, false, true, tenMB, true},
		{"exactly 10MB qualifies", rules.RiskSafe, rules.RebuildLow, false, true, tenMB, true},
		{"one byte under 10MB does not", rules.RiskSafe, rules.RebuildLow, false, true, tenMB - 1, false},
		{"running owner disqualifies", rules.RiskSafe, rules.RebuildLow, true, true, tenMB * 10, false},
		{"unrecognized owner disqualifies", rules.RiskSafe, rules.RebuildLow, false, false, tenMB * 10, false},
		{"caution disqualifies", rules.RiskCaution, rules.RebuildLow, false, true, tenMB * 10, false},
		{"high rebuild cost disqualifies", rules.RiskSafe, rules.RebuildHigh, false, true, tenMB * 10, false},
		{"no rebuild cost still qualifies", rules.RiskSafe, rules.RebuildNone, false, true, tenMB, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.Recommended(tt.risk, tt.rebuild, tt.running, tt.recognized, tt.size)
			if got != tt.want {
				t.Fatalf("Recommended(%s, %s, running=%v, recognized=%v, %d) = %v, want %v", tt.risk, tt.rebuild, tt.running, tt.recognized, tt.size, got, tt.want)
			}
		})
	}
}
