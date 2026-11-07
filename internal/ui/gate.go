package ui

import (
	"github.com/thewindear/free-clean-my-macos/internal/clean"
	"github.com/thewindear/free-clean-my-macos/internal/format"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

// ConfirmThreshold is the size of a single Junk Item that makes a Cleanup ask
// for confirmation whatever its Risk Level: at this point one mis-keypress
// costs a large download or hours of rebuilding.
const ConfirmThreshold int64 = 5 * 1024 * 1024 * 1024

// Confirmation is the gate in front of a Cleanup that is worth stopping for. It
// names the selection and its total, so the user confirms facts rather than a
// vague warning.
type Confirmation struct {
	Required bool
	Reasons  []string
	Items    []clean.Candidate
	Bytes    int64
}

// Confirm decides whether a Cleanup of these Junk Items needs the gate. The
// gate is not a default-yes: Required only ever opens an overlay that the user
// has to answer.
func Confirm(selected []clean.Candidate, permanent bool) Confirmation {
	confirmation := Confirmation{Items: selected}
	var large, caution int
	for _, it := range selected {
		confirmation.Bytes += it.Size
		if it.Size >= ConfirmThreshold {
			large++
		}
		if it.Risk == rules.RiskCaution {
			caution++
		}
	}

	if permanent {
		confirmation.Reasons = append(confirmation.Reasons,
			"Permanent Delete skips the Trash: this cannot be undone")
	}
	if large > 0 {
		confirmation.Reasons = append(confirmation.Reasons,
			format.Count(large, "Junk Item")+" at or above "+format.Bytes(ConfirmThreshold))
	}
	switch {
	case caution == 1:
		confirmation.Reasons = append(confirmation.Reasons, "one caution item needs a human decision")
	case caution > 1:
		confirmation.Reasons = append(confirmation.Reasons,
			format.Count(caution, "caution item")+" need a human decision")
	}

	confirmation.Required = len(confirmation.Reasons) > 0
	return confirmation
}
