package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/thewindear/free-clean-my-macos/internal/format"
)

// Current mode markers: what the focused row's checkbox says, and whether its
// Owner is open.
const (
	checkOff   = "[ ]"
	checkOn    = "[✓]"
	checkPart  = "[~]"
	openMark   = "▾"
	closedMark = "▸"
)

// sizeColumn is the width reserved at the right edge of a row for its size.
const sizeColumn = 10

// View renders the whole screen: the stats bar, one status line, the list and
// the key hints. It is a pure function of the model and the terminal size, so
// it renders correctly over SSH and in a test.
func (m *Model) View() string {
	if m.overlay != overlayNone {
		return m.panelView()
	}
	return m.listView()
}

// listView is the list screen proper.
func (m *Model) listView() string {
	height := m.listHeight()
	if height < 0 {
		height = 0
	}
	// stats bar, status line, the list, a blank, the key hints
	lines := make([]string, 0, height+4)
	lines = append(lines, m.statsBar(), m.statusLine())

	lines = append(lines, m.listLines(height)...)
	lines = append(lines, "", m.hintLine())
	if m.height >= 2 && len(lines) > m.height {
		// A terminal shorter than the chrome: keep the top line and the hints.
		lines = append(lines[:m.height-1], lines[len(lines)-1])
	}

	for i, line := range lines {
		lines[i] = pad(line, m.width)
	}
	return strings.Join(lines, "\n") + "\n"
}

// listLines is the scrollable middle of the screen, always exactly height lines
// so the footer never moves.
func (m *Model) listLines(height int) []string {
	rows := m.Rows()
	out := make([]string, 0, height)
	if len(rows) == 0 {
		out = append(out, "  "+m.emptyState())
	}

	start := m.scroll
	if start > len(rows) {
		start = len(rows)
	}
	for _, row := range rows[start:] {
		if len(out) >= height {
			break
		}
		out = append(out, m.rowLine(row))
	}
	for len(out) < height {
		out = append(out, "")
	}
	return out
}

// rowLine renders one list row with its size right-aligned.
func (m *Model) rowLine(row Row) string {
	prefix := "  "
	if row.Kind == RowOwner {
		if m.isExpanded(row.OwnerID) {
			prefix = openMark
		} else {
			prefix = closedMark
		}
	}
	label := row.Label
	if row.Kind == RowOwner && row.OwnerName != row.OwnerID {
		label = fmt.Sprintf("%s (%s)", row.OwnerName, row.OwnerID)
	}
	if row.Kind == RowItem {
		label = format.Home(m.home, row.Path)
	}
	mark := m.checkMark(row.Check)
	if row.State != "" {
		mark = stateMark(row.State)
	}
	left := fmt.Sprintf("%s %s %s", prefix, mark, label)
	return rightAlign(left, format.Bytes(row.Size), m.width)
}

// stateMark is the Cleanup Progress glyph for one Junk Item: queued, being
// removed, removed, or failed.
func stateMark(state ItemState) string {
	switch state {
	case StateInProgress:
		return "[>]"
	case StateDone:
		return "[✓]"
	case StateFailed:
		return "[!]"
	default:
		return "[·]"
	}
}

// checkMark is the row's checkbox: an Owner can also read as partly selected.
func (m *Model) checkMark(c Check) string {
	switch c {
	case CheckAll:
		return checkOn
	case CheckSome:
		return checkPart
	default:
		return checkOff
	}
}

// statsBar is the top line: what was found, how long it took, what is selected.
func (m *Model) statsBar() string {
	s := m.Stats()
	parts := []string{"fcm"}
	if s.Phase == PhaseScanning {
		parts = append(parts, "scanning…")
	}
	parts = append(parts, format.Count(s.ItemsFound, "item"), format.Bytes(s.BytesFound))
	parts = append(parts, formatElapsed(s.Elapsed))
	if s.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", s.Skipped))
	}
	parts = append(parts, fmt.Sprintf("selected %d (%s)", s.SelectedItems, format.Bytes(s.SelectedBytes)))
	return joinLine(parts)
}

// statusLine is what the middle of the top says: the path being sized, the
// filter in progress, or the notice from a cancelled scan.
func (m *Model) statusLine() string {
	switch {
	case m.cleaning:
		return fmt.Sprintf("cleaning  %d/%d · %s of %s",
			m.cleanup.Items, m.cleanup.Count,
			format.Bytes(m.cleanup.Bytes), format.Bytes(m.cleanup.TotalBytes))
	case m.editing:
		return "filter: " + m.query + "_"
	case m.query != "":
		return "filter: " + m.query + "  (esc clears)"
	case m.phase == PhaseScanning && m.Stats().Current != "":
		return "current  " + format.Home(m.home, m.Stats().Current)
	case m.notice != "":
		return m.notice
	default:
		return ""
	}
}

// hintLine is the key map for whatever mode the screen is in.
func (m *Model) hintLine() string {
	switch {
	case m.editing:
		return "enter apply · esc clear"
	case m.cleaning:
		return "cleaning… q quits, the Cleanup Log keeps what happened"
	case m.phase == PhaseScanning:
		return "esc cancel scan · q quit"
	default:
		return "space toggle · a owner · A all · / filter · d detail · c clean · q quit"
	}
}

// emptyState is what the list area says when there is nothing to show.
func (m *Model) emptyState() string {
	switch {
	case m.phase == PhaseScanning:
		return "scanning… nothing found yet"
	case m.query != "":
		return "no Owner or path matches " + m.query
	default:
		return "nothing safe to clean"
	}
}

// panelView is the modal panel overlay: the Detail, the confirmation gate or
// the Cleanup Summary. It replaces the list while it is up, keeping the stats
// bar and a one-line key hint.
func (m *Model) panelView() string {
	p, ok := m.panelFor()
	if !ok {
		return m.listView()
	}
	lines := []string{m.statsBar(), p.Title, strings.Repeat("─", maxInt(0, minInt(m.width, 60)))}

	const labelWidth = 15
	valueWidth := maxInt(1, m.width-labelWidth-1)
	for _, f := range p.Fields {
		wrapped := wrapCells(f.Value, valueWidth)
		lines = append(lines, pad(f.Label, labelWidth)+" "+wrapped[0])
		for _, extra := range wrapped[1:] {
			lines = append(lines, strings.Repeat(" ", labelWidth+1)+extra)
		}
	}
	lines = append(lines, "", p.Hint)

	// Never overflow the terminal.
	if max := maxInt(2, m.height); len(lines) > max {
		lines = append(lines[:max-2], "…", p.Hint)
	}
	for i, line := range lines {
		lines[i] = pad(line, m.width)
	}
	return strings.Join(lines, "\n") + "\n"
}

// --- text helpers ---------------------------------------------------------

// rightAlign puts label on the left and value flush right, truncating the label
// rather than the value: a size is never ambiguous.
func rightAlign(label, value string, width int) string {
	room := width - sizeColumn - 1
	if room < 1 {
		room = 1
	}
	label = truncate(label, room)
	gap := width - runewidth.StringWidth(label) - runewidth.StringWidth(value)
	if gap < 1 {
		gap = 1
	}
	return label + strings.Repeat(" ", gap) + value
}

// truncate shortens s to at most width cells, marking the cut with an ellipsis.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return runewidth.Truncate(s, width-1, "") + "…"
}

// pad makes s exactly width cells so no line ever wraps.
func pad(s string, width int) string {
	s = truncate(s, width)
	if gap := width - runewidth.StringWidth(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

// wrapCells breaks s into lines of at most width cells, preferring to break
// after a "/" or a space so a long path stays readable.
func wrapCells(s string, width int) []string {
	if width < 1 {
		return []string{""}
	}
	var out []string
	for runewidth.StringWidth(s) > width {
		head := runewidth.Truncate(s, width, "")
		cut := strings.LastIndexAny(head, "/ ")
		if cut <= 0 {
			cut = len(head)
		} else {
			cut++ // keep the separator on the line it belongs to
		}
		if cut > len(s) {
			cut = len(s)
		}
		out = append(out, strings.TrimRight(s[:cut], " "))
		s = strings.TrimLeft(s[cut:], " ")
	}
	return append(out, s)
}

// formatElapsed keeps one decimal for the first minute, because "11.2s" is the
// number the scan actually took.
func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

func joinLine(parts []string) string {
	return strings.Join(parts, " · ")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
