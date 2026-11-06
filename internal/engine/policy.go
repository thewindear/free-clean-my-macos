package engine

import "github.com/thewindear/free-clean-my-macos/internal/rules"

// tenMB is the size floor below which an item is folded away rather than
// recommended: deleting it wins nothing worth the decision. It is the same
// number as FoldBelow because both answer the same question.
const tenMB = FoldBelow

// Recommended reports whether a Junk Item is pre-checked for the user. It is
// the engine's single source of truth for the checkboxes; the UI never
// recomputes it. An unrecognized Owner disqualifies an item: if the tool cannot
// say who owns the data, it does not recommend deleting it.
func Recommended(risk rules.Risk, rebuild rules.Rebuild, ownerRunning, ownerRecognized bool, size int64) bool {
	if risk != rules.RiskSafe {
		return false
	}
	if rebuild != rules.RebuildNone && rebuild != rules.RebuildLow {
		return false
	}
	if ownerRunning || !ownerRecognized {
		return false
	}
	return size >= tenMB
}
