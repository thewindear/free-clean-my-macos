package ui

import (
	"strings"

	"github.com/thewindear/free-clean-my-macos/internal/clean"
	"github.com/thewindear/free-clean-my-macos/internal/format"
)

// panel is one modal panel: a title, labelled facts, and a one-line hint saying
// how to get out of it.
type panel struct {
	Title  string
	Fields []Field
	Hint   string
}

// panelFor builds the panel the current overlay shows.
func (m *Model) panelFor() (panel, bool) {
	switch m.overlay {
	case overlayDetail:
		detail, ok := m.Detail()
		if !ok {
			return panel{}, false
		}
		return panel{Title: detail.Title, Fields: detail.Fields, Hint: "esc close"}, true
	case overlayConfirm:
		return m.confirmationPanel(), true
	case overlaySummary:
		return m.summaryPanel(), true
	default:
		return panel{}, false
	}
}

// confirmationPanel names what is about to be removed and why it stopped to
// ask. The total is the Selection's, and every listed item is a real path.
func (m *Model) confirmationPanel() panel {
	p := panel{Title: "Confirm Cleanup", Hint: "y confirm · n cancel"}
	p.Fields = append(p.Fields, Field{
		Label: "Why",
		Value: strings.Join(m.confirmation.Reasons, "; "),
	})
	p.Fields = append(p.Fields,
		Field{Label: "Junk Items", Value: format.Count(len(m.confirmation.Items), "item")},
		Field{Label: "Total", Value: format.Bytes(m.confirmation.Bytes)},
	)

	// A panel is 24 lines at most, so the item list is capped; the user can
	// see the rest by declining and looking at the list itself.
	const maxListed = 10
	for i, it := range m.confirmation.Items {
		if i == maxListed {
			p.Fields = append(p.Fields, Field{
				Label: "",
				Value: "… and " + format.Count(len(m.confirmation.Items)-maxListed, "more item"),
			})
			break
		}
		p.Fields = append(p.Fields, Field{
			Label: "Item",
			Value: format.Bytes(it.Size) + "  " + format.Home(m.home, it.Path),
		})
	}
	return p
}

// summaryPanel is the honest end-of-run report: Space Freed and Space Moved are
// never added together, failures keep their reason, and the Cleanup Log path is
// there to be grepped later.
func (m *Model) summaryPanel() panel {
	result := clean.Result{}
	if m.result != nil {
		result = *m.result
	}
	s := result.Summary
	p := panel{Title: "Cleanup Summary", Hint: "esc close"}
	p.Fields = append(p.Fields,
		Field{Label: "Space Freed", Value: format.Bytes(s.FreedBytes)},
		Field{Label: "Space Moved", Value: format.Bytes(s.MovedBytes)},
		Field{Label: "Items cleaned", Value: format.Count(s.Items, "item")},
	)
	if s.NotAttempted > 0 {
		p.Fields = append(p.Fields, Field{
			Label: "Not attempted",
			Value: format.Count(s.NotAttempted, "item") + " left in the list — press c to retry",
		})
	}
	if len(s.Failed) > 0 {
		p.Fields = append(p.Fields, Field{
			Label: "Failed",
			Value: format.Count(len(s.Failed), "item") + " left in the list — press c to retry",
		})
		for _, f := range s.Failed {
			value := format.Home(m.home, f.Path) + " (" + f.Reason + ")"
			if f.Error != "" && f.Error != f.Reason {
				value += ": " + f.Error
			}
			p.Fields = append(p.Fields, Field{Label: "", Value: value})
		}
	}
	if s.LogPath != "" {
		p.Fields = append(p.Fields, Field{Label: "Cleanup Log", Value: format.Home(m.home, s.LogPath)})
	}
	if s.MovedBytes > 0 {
		p.Fields = append(p.Fields, Field{
			Label: "Note",
			Value: "Space Moved is recoverable until you empty the Trash in Finder.",
		})
	}
	return p
}
