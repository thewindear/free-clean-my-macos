// Package ui is the list screen: a pure view model over the engine's Report,
// and the Bubble Tea shell that feeds it.
//
// The model owns every number the screen shows. Sizes, totals, Risk Levels and
// Recommended flags all come from the engine; the model only sums and groups
// them. It never walks the filesystem and never re-derives a rule decision, so
// the list, the CLI and a future GUI can never disagree.
package ui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/format"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

// Phase is what the screen is doing.
type Phase int

const (
	PhaseScanning Phase = iota
	PhaseReady
)

// Action is what a key press asks the shell to do. The model never quits or
// cancels a scan itself: it reports the intent and the shell performs it.
type Action int

const (
	ActionNone Action = iota
	ActionQuit
	ActionCancelScan
)

// Key is one normalized key press. The shell translates terminal input into
// these, so the whole key map is testable without a terminal.
type Key string

const (
	KeySpace     Key = "space"
	KeyEnter     Key = "enter"
	KeyUp        Key = "up"
	KeyDown      Key = "down"
	KeyHome      Key = "home"
	KeyEnd       Key = "end"
	KeyEsc       Key = "esc"
	KeyQuit      Key = "q"
	KeyBackspace Key = "backspace"

	KeyToggleOwner Key = "a"
	KeyToggleAll   Key = "A"
	KeyFilter      Key = "/"
	KeyDetail      Key = "d"
)

// Check is the checkbox state of a row: for an item it is selected or not, for
// an Owner or a fold row it aggregates its Junk Items.
type Check int

const (
	CheckNone Check = iota
	CheckSome
	CheckAll
)

// RowKind is what a row in the list stands for.
type RowKind int

const (
	RowOwner RowKind = iota
	RowItem
	RowFold
)

// Row is one visible line of the list. Rows are derived from the Report on
// every call, so the list can never drift out of sync with the selection.
type Row struct {
	Kind      RowKind
	OwnerID   string
	OwnerName string
	Path      string // RowItem only: the absolute path
	Depth     int    // 0 for an Owner, 1 for what sits under it
	Label     string // Owner display name, or "N other items" for RowFold
	Size      int64
	Count     int // RowFold: how many Junk Items are folded in
	Check     Check
}

// Stats is the stats bar: what the scan found and what is selected right now.
type Stats struct {
	Phase         Phase
	ItemsFound    int
	BytesFound    int64
	Skipped       int
	Elapsed       time.Duration
	SelectedItems int
	SelectedBytes int64
	Current       string // the absolute path being sized, while scanning
	Notice        string // "scan cancelled", "scan failed: ...", or empty
}

// Detail is the modal panel a `d` press opens: labelled facts about one row.
type Detail struct {
	Title  string
	Fields []Field
}

// Field is one labelled line of the Detail panel.
type Field struct {
	Label string
	Value string
}

// Model is the list screen's state. It is a plain value with methods; no
// terminal, no goroutine, no clock of its own.
type Model struct {
	home      string
	startedAt time.Time
	width     int
	height    int

	phase    Phase
	progress engine.Progress
	elapsed  time.Duration
	report   engine.Report
	notice   string

	selected map[string]bool // absolute path -> selected
	expanded map[string]bool // Owner id -> items visible
	unfolded map[string]bool // Owner id -> small items listed individually

	query   string
	editing bool
	detail  bool
	cursor  int
	scroll  int
}

// New starts a Model in the scanning phase. home is only used to shorten paths
// to Display Paths.
func New(home string, startedAt time.Time) *Model {
	return &Model{
		home:      home,
		startedAt: startedAt,
		width:     80,
		height:    24,
		phase:     PhaseScanning,
		selected:  map[string]bool{},
		expanded:  map[string]bool{},
		unfolded:  map[string]bool{},
	}
}

// SetSize records the terminal size the shell reports.
func (m *Model) SetSize(width, height int) {
	if width > 0 {
		m.width = width
	}
	if height > 0 {
		m.height = height
	}
	m.followCursor()
}

// ApplyProgress records a Scan Progress event. The numbers are the engine's;
// the model copies them verbatim.
func (m *Model) ApplyProgress(p engine.Progress) {
	m.progress = p
}

// Tick advances the live elapsed time while a scan is running. After the scan
// finishes the engine's own DurationMS is authoritative and ticks are ignored.
func (m *Model) Tick(now time.Time) {
	if m.phase != PhaseScanning {
		return
	}
	if elapsed := now.Sub(m.startedAt); elapsed > 0 {
		m.elapsed = elapsed
	}
}

// FinishScan adopts the Report. A cancelled scan still adopts the partial
// Report, so the items sized before the cancel stay browsable. Every Junk Item
// the engine marked Recommended starts selected.
func (m *Model) FinishScan(report engine.Report, err error) {
	m.report = report
	m.phase = PhaseReady
	m.elapsed = time.Duration(report.DurationMS) * time.Millisecond
	m.query, m.editing, m.detail = "", false, false
	m.cursor, m.scroll = 0, 0
	m.progress = engine.Progress{
		Items:   report.Summary.Items,
		Bytes:   report.Summary.TotalBytes,
		Skipped: len(report.Skipped),
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.notice = "scan cancelled — showing what was found before you cancelled"
		} else {
			m.notice = "scan failed: " + err.Error()
		}
	}

	m.selected = map[string]bool{}
	for _, o := range report.Owners {
		for _, it := range o.Items {
			if it.Recommended {
				m.selected[it.Path] = true
			}
		}
	}
}

// Query is the active filter text.
func (m *Model) Query() string { return m.query }

// EditingFilter reports whether keystrokes go into the filter.
func (m *Model) EditingFilter() bool { return m.editing }

// Cursor is the focused row's index into Rows.
func (m *Model) Cursor() int { return m.cursor }

// HandleKey applies one key press and reports what the shell should do about
// it.
func (m *Model) HandleKey(k Key) Action {
	if k == KeyQuit && !m.editing {
		return ActionQuit
	}

	if m.editing {
		switch k {
		case KeyEsc:
			m.query, m.editing, m.cursor, m.scroll = "", false, 0, 0
		case KeyEnter:
			m.editing = false
		case KeyBackspace:
			if r := []rune(m.query); len(r) > 0 {
				m.query = string(r[:len(r)-1])
			}
			m.cursor, m.scroll = 0, 0
		case KeySpace:
			m.query += " "
			m.cursor, m.scroll = 0, 0
		default:
			// Everything else that is a single character is text; named keys
			// (up, home, ...) are not.
			if runes := []rune(k); len(runes) == 1 {
				m.query += string(runes)
				m.cursor, m.scroll = 0, 0
			}
		}
		return ActionNone
	}

	if m.detail {
		switch k {
		case KeyEsc, KeyEnter, KeyDetail:
			m.detail = false
		}
		return ActionNone
	}

	if m.phase == PhaseScanning {
		if k == KeyEsc {
			return ActionCancelScan
		}
		return ActionNone
	}

	switch k {
	case KeyEsc:
		// esc backs out of the filter before it does anything else.
		if m.query != "" {
			m.query, m.cursor, m.scroll = "", 0, 0
		}
	case KeyUp:
		m.moveCursor(-1)
	case KeyDown:
		m.moveCursor(1)
	case KeyHome:
		m.cursor = 0
		m.followCursor()
	case KeyEnd:
		m.cursor = len(m.Rows()) - 1
		m.followCursor()
	case KeySpace:
		m.toggleFocused()
	case KeyEnter:
		m.toggleExpandFocused()
	case KeyToggleOwner:
		if row, ok := m.focused(); ok {
			m.toggleOwner(row.OwnerID)
		}
	case KeyToggleAll:
		m.toggleAll()
	case KeyFilter:
		m.editing = true
	case KeyDetail:
		if _, ok := m.focused(); ok {
			m.detail = true
		}
	}
	return ActionNone
}

// Rows is the visible list: every Owner, expanded into its Junk Items when it
// is open, with small items folded into one row unless unfolded. A non-empty
// filter narrows to matching Owners and opens them, so the matching path is on
// screen.
func (m *Model) Rows() []Row {
	var rows []Row
	for _, o := range m.report.Owners {
		if !m.matches(o) {
			continue
		}
		rows = append(rows, Row{
			Kind:      RowOwner,
			OwnerID:   o.ID,
			OwnerName: o.Name,
			Label:     o.Name,
			Size:      o.Total,
			Count:     len(o.Items),
			Check:     m.checkOwner(o),
		})
		if !m.isExpanded(o.ID) {
			continue
		}

		var folded []engine.Item
		var big []engine.Item
		for _, it := range o.Items {
			if it.Size < engine.FoldBelow && !m.unfolded[o.ID] {
				folded = append(folded, it)
				continue
			}
			big = append(big, it)
		}
		for _, it := range big {
			rows = append(rows, Row{
				Kind:      RowItem,
				OwnerID:   o.ID,
				OwnerName: o.Name,
				Path:      it.Path,
				Depth:     1,
				Label:     it.Path,
				Size:      it.Size,
				Check:     checkOf(m.selected[it.Path]),
			})
		}
		if len(folded) > 0 {
			var size int64
			selected := 0
			for _, it := range folded {
				size += it.Size
				if m.selected[it.Path] {
					selected++
				}
			}
			check := CheckNone
			if selected == len(folded) {
				check = CheckAll
			} else if selected > 0 {
				check = CheckSome
			}
			rows = append(rows, Row{
				Kind:      RowFold,
				OwnerID:   o.ID,
				OwnerName: o.Name,
				Depth:     1,
				Label:     format.Count(len(folded), "other item"),
				Size:      size,
				Count:     len(folded),
				Check:     check,
			})
		}
	}
	return rows
}

// Stats is the stats bar's content.
func (m *Model) Stats() Stats {
	s := Stats{
		Phase:   m.phase,
		Elapsed: m.elapsed,
		Notice:  m.notice,
	}
	if m.phase == PhaseScanning {
		// Straight from the engine's events: the stats bar never derives a
		// number of its own while the scan is running.
		s.ItemsFound = m.progress.Items
		s.BytesFound = m.progress.Bytes
		s.Skipped = m.progress.Skipped
		s.Current = m.progress.Current
	} else {
		s.ItemsFound = m.report.Summary.Items
		s.BytesFound = m.report.Summary.TotalBytes
		s.Skipped = len(m.report.Skipped)
	}
	s.SelectedItems, s.SelectedBytes = m.Selection()
	return s
}

// Selection is what the user has checked: how many Junk Items and how many
// bytes, always summed from the engine's sizes.
func (m *Model) Selection() (items int, bytes int64) {
	for _, o := range m.report.Owners {
		for _, it := range o.Items {
			if m.selected[it.Path] {
				items++
				bytes += it.Size
			}
		}
	}
	return items, bytes
}

// Detail is the modal panel's content for the focused row, and whether the
// panel is open at all.
func (m *Model) Detail() (Detail, bool) {
	if !m.detail {
		return Detail{}, false
	}
	row, ok := m.focused()
	if !ok {
		return Detail{}, false
	}
	switch row.Kind {
	case RowOwner:
		for _, o := range m.report.Owners {
			if o.ID != row.OwnerID {
				continue
			}
			fields := []Field{
				{Label: "Owner id", Value: o.ID},
				{Label: "Owner name", Value: o.Name},
				{Label: "Total", Value: format.Bytes(o.Total)},
				{Label: "Junk Items", Value: format.Count(len(o.Items), "item")},
				{Label: "Running", Value: yesNo(o.Running)},
			}
			return Detail{Title: "Owner", Fields: fields}, true
		}
	case RowFold:
		return Detail{
			Title: "Folded Junk Items",
			Fields: []Field{
				{Label: "Owner", Value: row.OwnerName + " (" + row.OwnerID + ")"},
				{Label: "Items", Value: format.Count(row.Count, "item")},
				{Label: "Total", Value: format.Bytes(row.Size)},
				{Label: "What now", Value: "press enter to list them individually"},
			},
		}, true
	case RowItem:
		item, ok := m.item(row.Path)
		if !ok {
			return Detail{}, false
		}
		fields := []Field{
			{Label: "Path", Value: item.Path},
			{Label: "Size", Value: format.Bytes(item.Size)},
			{Label: "Owner", Value: row.OwnerName + " (" + row.OwnerID + ")"},
			{Label: "Kind", Value: string(item.Kind)},
			{Label: "Risk Level", Value: string(item.Risk) + " — " + riskExplanation(item.Risk)},
			{Label: "Rebuild Cost", Value: string(item.Rebuild) + " — " + rebuildExplanation(item.Rebuild)},
			{Label: "Scan Rule", Value: ruleLabel(item)},
		}
		return Detail{Title: "Junk Item", Fields: fields}, true
	}
	return Detail{}, false
}

// --- selection ------------------------------------------------------------

func (m *Model) focused() (Row, bool) {
	rows := m.Rows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return Row{}, false
	}
	return rows[m.cursor], true
}

// toggleFocused is space: an item flips itself, an Owner or a fold row flips
// every Junk Item it stands for.
func (m *Model) toggleFocused() {
	row, ok := m.focused()
	if !ok {
		return
	}
	switch row.Kind {
	case RowItem:
		m.selected[row.Path] = !m.selected[row.Path]
	case RowOwner:
		o, ok := m.owner(row.OwnerID)
		if !ok {
			return
		}
		set := m.checkOwner(o) != CheckAll
		for _, it := range o.Items {
			m.selected[it.Path] = set
		}
	case RowFold:
		o, ok := m.owner(row.OwnerID)
		if !ok {
			return
		}
		set := row.Check != CheckAll
		for _, it := range o.Items {
			if it.Size < engine.FoldBelow {
				m.selected[it.Path] = set
			}
		}
	}
}

// toggleExpandFocused is enter: an Owner opens or closes, a fold row lists its
// small items individually.
func (m *Model) toggleExpandFocused() {
	row, ok := m.focused()
	if !ok {
		return
	}
	switch row.Kind {
	case RowOwner:
		m.expanded[row.OwnerID] = !m.isExpanded(row.OwnerID)
	case RowFold:
		m.unfolded[row.OwnerID] = !m.unfolded[row.OwnerID]
	}
	m.followCursor()
}

// toggleOwner is `a`: every Junk Item of one Owner.
func (m *Model) toggleOwner(ownerID string) {
	o, ok := m.owner(ownerID)
	if !ok {
		return
	}
	set := m.checkOwner(o) != CheckAll
	for _, it := range o.Items {
		m.selected[it.Path] = set
	}
}

// toggleAll is `A`: every Junk Item currently listed, so a filter narrows it.
func (m *Model) toggleAll() {
	items := m.visibleItems()
	if len(items) == 0 {
		return
	}
	set := false
	for _, it := range items {
		if !m.selected[it.Path] {
			set = true
			break
		}
	}
	for _, it := range items {
		m.selected[it.Path] = set
	}
}

// visibleItems is every Junk Item in the current filter, regardless of whether
// its Owner is expanded.
func (m *Model) visibleItems() []engine.Item {
	var out []engine.Item
	for _, o := range m.report.Owners {
		if m.matches(o) {
			out = append(out, o.Items...)
		}
	}
	return out
}

func (m *Model) checkOwner(o engine.Owner) Check {
	if len(o.Items) == 0 {
		return CheckNone
	}
	selected := 0
	for _, it := range o.Items {
		if m.selected[it.Path] {
			selected++
		}
	}
	switch selected {
	case 0:
		return CheckNone
	case len(o.Items):
		return CheckAll
	default:
		return CheckSome
	}
}

func (m *Model) owner(id string) (engine.Owner, bool) {
	for _, o := range m.report.Owners {
		if o.ID == id {
			return o, true
		}
	}
	return engine.Owner{}, false
}

func (m *Model) item(path string) (engine.Item, bool) {
	for _, o := range m.report.Owners {
		for _, it := range o.Items {
			if it.Path == path {
				return it, true
			}
		}
	}
	return engine.Item{}, false
}

// --- filter and expansion -------------------------------------------------

func (m *Model) isExpanded(ownerID string) bool {
	return m.expanded[ownerID] || m.query != ""
}

func (m *Model) matches(o engine.Owner) bool {
	q := strings.ToLower(strings.TrimSpace(m.query))
	if q == "" {
		return true
	}
	if strings.Contains(strings.ToLower(o.ID), q) || strings.Contains(strings.ToLower(o.Name), q) {
		return true
	}
	for _, it := range o.Items {
		if strings.Contains(strings.ToLower(it.Path), q) {
			return true
		}
	}
	return false
}

// --- cursor and scrolling -------------------------------------------------

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	m.clampCursor()
	m.followCursor()
}

func (m *Model) clampCursor() {
	if n := len(m.Rows()); m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// followCursor scrolls the list only when the cursor would otherwise leave the
// window, so moving one row never jumps the whole list.
func (m *Model) followCursor() {
	m.clampCursor()
	height := m.listHeight()
	if height <= 0 {
		m.scroll = 0
		return
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+height {
		m.scroll = m.cursor - height + 1
	}
	if max := len(m.Rows()) - height; m.scroll > max {
		m.scroll = max
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// listHeight is how many list rows fit between the status line and the key
// hints: the stats bar and the status line above, a blank and the hints below.
func (m *Model) listHeight() int {
	return m.height - 4
}

// --- vocabulary -----------------------------------------------------------

// riskExplanation is the Risk Level's meaning from the glossary, so the detail
// panel explains a judgement instead of just naming it.
func riskExplanation(risk rules.Risk) string {
	switch risk {
	case rules.RiskSafe:
		return "deleting it leaves the system and its software fully functional"
	case rules.RiskCaution:
		return "deleting it costs a feature or needs a human decision"
	default:
		return "unclassified"
	}
}

// rebuildExplanation is the Rebuild Cost's meaning from the glossary.
func rebuildExplanation(rebuild rules.Rebuild) string {
	switch rebuild {
	case rules.RebuildNone:
		return "nothing to rebuild"
	case rules.RebuildLow:
		return "the app rebuilds it in seconds"
	case rules.RebuildHigh:
		return "rebuild takes hours or a large download"
	default:
		return "unclassified"
	}
}

func ruleLabel(item engine.Item) string {
	switch {
	case item.Rule == "" && item.Note == "":
		return "unknown"
	case item.Note == "":
		return item.Rule
	case item.Rule == "":
		return item.Note
	default:
		return item.Rule + " — " + item.Note
	}
}

func checkOf(selected bool) Check {
	if selected {
		return CheckAll
	}
	return CheckNone
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
