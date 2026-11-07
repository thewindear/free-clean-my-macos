// Package clean removes Junk Items with the hybrid deletion policy of ADR-0003:
// items that are cheap to regenerate are deleted outright and count toward
// Space Freed, everything else is moved into the Trash and counts toward Space
// Moved. Every item leaves exactly one Cleanup Log record, whatever happened to
// it, so the Summary can be audited later.
package clean

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

// SchemaVersion identifies the JSON contract of a clean run.
const SchemaVersion = 1

// Mode is how a Junk Item is removed.
type Mode string

const (
	// ModeDelete is an outright deletion: the bytes are freed now.
	ModeDelete Mode = "delete"
	// ModeTrash is a move into the Trash: recoverable, and frees nothing until
	// the Trash is emptied in Finder.
	ModeTrash Mode = "trash"
	// ModePermanent is an explicitly requested Permanent Delete: the Trash is
	// bypassed even for caution or high-rebuild items.
	ModePermanent Mode = "permanent"
)

// Outcome is what actually happened to one Junk Item.
type Outcome string

const (
	OutcomePlanned      Outcome = "planned"
	OutcomeDeleted      Outcome = "deleted"
	OutcomeTrashed      Outcome = "trashed"
	OutcomeFailed       Outcome = "failed"
	OutcomeNotAttempted Outcome = "not-attempted"
)

// errVanished marks an item that was already gone when the Cleanup reached it.
// It is reported as a failure rather than silently counted as freed space.
var errVanished = errors.New("path vanished before it could be removed")

// Options carries every edge of a Cleanup: where the Trash is, where the
// Cleanup Log lives, whether this is a dry run, where the progress events go,
// and the clock.
type Options struct {
	TrashDir string
	StateDir string
	DryRun   bool
	// Permanent forces ModePermanent for every selected item, bypassing the
	// Trash whatever the item's Risk Level or Rebuild Cost.
	Permanent bool
	Now       func() time.Time

	// Emit receives one started and one finished event per Junk Item, from the
	// goroutine that runs the Cleanup.
	Emit func(Event)
}

// EventKind is what one Cleanup event reports.
type EventKind string

const (
	// EventItemStarted means the item is about to be removed: the row can show
	// it as in progress.
	EventItemStarted EventKind = "item-started"
	// EventItemFinished means it is done or failed, and Summary says how much
	// has been accounted for so far.
	EventItemFinished EventKind = "item-finished"
)

// Event is one step of a Cleanup, streamed as it happens so a list screen can
// move each Junk Item through pending -> in-progress -> done or failed. The
// Summary is cumulative and never shrinks.
type Event struct {
	Kind    EventKind
	Item    Item
	Summary Summary
}

// Candidate is one selected Junk Item, flattened out of the scan Report with
// the Owner id that the Report only carries at the group level.
type Candidate struct {
	Path    string        `json:"path"`
	Size    int64         `json:"size"`
	Owner   string        `json:"owner"`
	Risk    rules.Risk    `json:"risk"`
	Rebuild rules.Rebuild `json:"rebuild"`
}

// Item is one planned, executed or failed removal: a Candidate plus what
// happened to it. It is also the one record the Cleanup Log stores per item.
type Item struct {
	Candidate
	Mode      Mode    `json:"mode"`
	Outcome   Outcome `json:"outcome"`
	Error     string  `json:"error,omitempty"`
	Timestamp string  `json:"timestamp"`
}

// Failure is one Junk Item that could not be removed, with the reason it could
// not be removed.
type Failure struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Error  string `json:"error,omitempty"`
}

// Summary is the arithmetic the user is asked to trust. Space Freed and Space
// Moved never overlap: freed + moved is exactly the bytes of the items the run
// removed successfully. NotAttempted counts the selected items a cancelled run
// left untouched; they contribute no bytes. On a dry run the same numbers are
// projections of what the run would do, which is what makes the plan worth
// reading.
type Summary struct {
	FreedBytes   int64     `json:"freed_bytes"`
	MovedBytes   int64     `json:"moved_bytes"`
	Items        int       `json:"items"`
	NotAttempted int       `json:"not_attempted"`
	Failed       []Failure `json:"failed"`
	LogPath      string    `json:"log_path"`
}

// Result is the schema v1 document of a clean run.
type Result struct {
	Schema    int     `json:"schema"`
	CleanedAt string  `json:"cleaned_at"`
	DryRun    bool    `json:"dry_run"`
	Cancelled bool    `json:"cancelled"`
	Items     []Item  `json:"items"`
	Summary   Summary `json:"summary"`
}

// ModeFor is the hybrid deletion policy: a Junk Item that is safe and cheap to
// rebuild is deleted outright, everything else stays recoverable in the Trash.
// It mirrors the first two conditions of engine.Recommended on purpose.
func ModeFor(risk rules.Risk, rebuild rules.Rebuild) Mode {
	if risk == rules.RiskSafe && (rebuild == rules.RebuildNone || rebuild == rules.RebuildLow) {
		return ModeDelete
	}
	return ModeTrash
}

// Select flattens the scan Report into the ordered selection a Cleanup works
// through: largest first, then by path, so a plan reads top-down and is
// deterministic.
func Select(report engine.Report, kinds []rules.Kind) []Candidate {
	var wanted map[rules.Kind]bool
	if len(kinds) > 0 {
		wanted = make(map[rules.Kind]bool, len(kinds))
		for _, k := range kinds {
			wanted[k] = true
		}
	}

	var out []Candidate
	for _, o := range report.Owners {
		for _, it := range o.Items {
			if wanted != nil && !wanted[it.Kind] {
				continue
			}
			out = append(out, Candidate{
				Path:    it.Path,
				Size:    it.Size,
				Owner:   o.ID,
				Risk:    it.Risk,
				Rebuild: it.Rebuild,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Run executes the Cleanup of exactly the given Junk Items, in the given order
// (largest first is what Select returns, and what a progress list reads best).
// A dry run builds the plan and touches nothing, not even the Cleanup Log. A
// returned error means the run could not start or had to stop; per-item
// failures are reported in the Summary instead.
func Run(ctx context.Context, selected []Candidate, opts Options) (Result, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	started := now()

	result := Result{
		Schema:    SchemaVersion,
		CleanedAt: started.Format(time.RFC3339),
		DryRun:    opts.DryRun,
		Items:     []Item{},
		Summary:   Summary{Failed: []Failure{}},
	}

	if len(selected) == 0 {
		return result, nil
	}

	var log *os.File
	if !opts.DryRun {
		if needsTrash(selected, opts.Permanent) {
			if opts.TrashDir == "" {
				return result, errors.New("clean: no Trash directory configured")
			}
			if err := os.MkdirAll(opts.TrashDir, 0o755); err != nil {
				return result, fmt.Errorf("clean: creating the Trash directory: %w", err)
			}
		}
		f, path, err := openLog(opts.StateDir, started)
		if err != nil {
			// Without a Cleanup Log there is no honest record of the run, so
			// nothing is removed.
			return result, fmt.Errorf("clean: opening the Cleanup Log: %w", err)
		}
		defer f.Close()
		log = f
		result.Summary.LogPath = path
	}

	for i, c := range selected {
		if ctx.Err() != nil {
			result.Cancelled = true
			for _, rest := range selected[i:] {
				item := Item{
					Candidate: rest,
					Mode:      modeFor(rest, opts.Permanent),
					Outcome:   OutcomeNotAttempted,
					Timestamp: now().Format(time.RFC3339),
				}
				if log != nil {
					if err := appendLog(log, item); err != nil {
						return result, fmt.Errorf("clean: writing the Cleanup Log: %w", err)
					}
				}
				result.Items = append(result.Items, item)
				result.Summary.NotAttempted++
				emit(opts, Event{Kind: EventItemFinished, Item: item, Summary: snapshot(result.Summary)})
			}
			break
		}
		item := Item{
			Candidate: c,
			Mode:      modeFor(c, opts.Permanent),
			Timestamp: now().Format(time.RFC3339),
		}
		emit(opts, Event{Kind: EventItemStarted, Item: item, Summary: snapshot(result.Summary)})

		if opts.DryRun {
			item.Outcome = OutcomePlanned
			result.Summary.Items++
			if item.Mode == ModeTrash {
				result.Summary.MovedBytes += item.Size
			} else {
				result.Summary.FreedBytes += item.Size
			}
			result.Items = append(result.Items, item)
			emit(opts, Event{Kind: EventItemFinished, Item: item, Summary: snapshot(result.Summary)})
			continue
		}

		if err := apply(opts.TrashDir, item.Path, item.Mode); err != nil {
			reason, detail := classify(err)
			item.Outcome = OutcomeFailed
			item.Error = detail
			result.Summary.Failed = append(result.Summary.Failed, Failure{
				Path:   item.Path,
				Reason: reason,
				Error:  detail,
			})
		} else if item.Mode == ModeTrash {
			item.Outcome = OutcomeTrashed
			result.Summary.MovedBytes += item.Size
			result.Summary.Items++
		} else {
			item.Outcome = OutcomeDeleted
			result.Summary.FreedBytes += item.Size
			result.Summary.Items++
		}

		if err := appendLog(log, item); err != nil {
			return result, fmt.Errorf("clean: writing the Cleanup Log: %w", err)
		}
		result.Items = append(result.Items, item)
		emit(opts, Event{Kind: EventItemFinished, Item: item, Summary: snapshot(result.Summary)})
	}

	return result, nil
}

// emit hands one event to the consumer when there is one.
func emit(opts Options, ev Event) {
	if opts.Emit != nil {
		opts.Emit(ev)
	}
}

// snapshot copies a Summary for an event, so a later failure cannot change an
// event the consumer already holds.
func snapshot(s Summary) Summary {
	s.Failed = append([]Failure(nil), s.Failed...)
	return s
}

// modeFor is the deletion mode for one item: --permanent overrides the hybrid
// policy for the whole selection.
func modeFor(c Candidate, permanent bool) Mode {
	if permanent {
		return ModePermanent
	}
	return ModeFor(c.Risk, c.Rebuild)
}

// needsTrash reports whether any selected item has to be moved into the Trash.
func needsTrash(selected []Candidate, permanent bool) bool {
	if permanent {
		return false
	}
	for _, c := range selected {
		if ModeFor(c.Risk, c.Rebuild) == ModeTrash {
			return true
		}
	}
	return false
}

// apply removes one path according to mode. A path that was already gone is a
// failure, not a success: counting bytes that were not touched would inflate
// Space Freed.
func apply(trashDir, path string, mode Mode) error {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errVanished
		}
		return err
	}
	if mode == ModeTrash {
		return os.Rename(path, freePath(trashDir, filepath.Base(path)))
	}
	return os.RemoveAll(path)
}

// freePath picks a name inside dir that is not taken yet, the way Finder does:
// "report.pdf" becomes "report 2.pdf" instead of overwriting.
func freePath(dir, name string) string {
	target := filepath.Join(dir, name)
	if !taken(target) {
		return target
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 2; ; n++ {
		target = filepath.Join(dir, fmt.Sprintf("%s %d%s", stem, n, ext))
		if !taken(target) {
			return target
		}
	}
}

func taken(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// classify turns a filesystem error into a Skip-style reason plus the detail
// that goes into the Cleanup Log.
func classify(err error) (reason, detail string) {
	if errors.Is(err, errVanished) {
		return engine.ReasonVanished, err.Error()
	}
	return engine.ReasonFor(err), err.Error()
}

// openLog appends to the Cleanup Log for the month of t, creating the state
// directory on first use. The tool never rotates or rewrites the log.
func openLog(stateDir string, t time.Time) (*os.File, string, error) {
	if stateDir == "" {
		return nil, "", errors.New("no state directory configured")
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, "", err
	}
	path := filepath.Join(stateDir, "cleanup-"+t.Format("200601")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}

// appendLog writes the one JSONL record that every Junk Item is entitled to.
func appendLog(w io.Writer, item Item) error {
	b, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
