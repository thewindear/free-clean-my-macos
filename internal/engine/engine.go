// Package engine is the whole scanner. The TUI and the CLI are consumers of
// the same Report; sizes, totals and recommendation flags are computed here
// once and never recomputed downstream.
package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/thewindear/free-clean-my-macos/internal/owner"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

// Options carries every edge of the scan, so tests can substitute all of them:
// the home directory, the rule set, the installed-app search path, the clock,
// and the liveness probe.
type Options struct {
	Home     string
	TmpDir   string
	Rules    []rules.Rule
	AppsDirs []string

	// Ignore is the User Ignore List, with unexpanded ${HOME} / ${TMPDIR}
	// variables.
	Ignore []string

	// ConfigDir and StateDir are the tool's own config and state directories.
	// SelfBinary is the running executable. All three are Self-Exclusion
	// boundaries: no rule, embedded or user-supplied, may claim them.
	ConfigDir  string
	StateDir   string
	SelfBinary string

	// SystemRoot is a test seam. It relocates the absolute /Library rules into
	// a fixture so process-boundary tests never read the real /Library.
	SystemRoot string

	// PathTimeout bounds one Junk Item's size walk. Zero means the product
	// default of 30 seconds.
	PathTimeout time.Duration

	// Jobs bounds the size walk. Zero means min(8, NumCPU/2).
	Jobs int

	Now     func() time.Time
	Running func(owner.Owner) bool

	// Emit receives Scan Progress as the scan runs, so a consumer can show a
	// live stats bar instead of a frozen screen. It is called from the scanning
	// goroutine: it must be cheap, must not block, and must not call back into
	// the engine.
	Emit func(Progress)
}

// Progress is the live Scan Progress: how much has been sized so far and which
// path is in flight right now. Items and Bytes are cumulative and never shrink,
// so a stats bar built from them never jumps backwards.
type Progress struct {
	Items   int
	Bytes   int64
	Skipped int
	Current string
}

// FoldBelow is the size under which a listing folds a Junk Item into one
// "other N items" row instead of giving it a row, so the list stays readable at
// 200+ entries. It is the same 10 MB as Recommended's floor: an item that is
// not worth recommending is not worth a row either.
const FoldBelow int64 = 10 * 1024 * 1024

// emit hands one Progress to the consumer when there is one.
func emit(opts Options, p Progress) {
	if opts.Emit != nil {
		opts.Emit(p)
	}
}

// Item is one Junk Item.
type Item struct {
	Path        string        `json:"path"`
	Size        int64         `json:"size"`
	Kind        rules.Kind    `json:"kind"`
	Risk        rules.Risk    `json:"risk"`
	Rebuild     rules.Rebuild `json:"rebuild"`
	Recommended bool          `json:"recommended"`

	// Rule and Note are the Scan Rule that claimed this path and its one-line
	// human explanation, so a detail view can explain why the item is listed
	// without matching rules a second time.
	Rule string `json:"rule"`
	Note string `json:"note,omitempty"`
}

// Owner is an Owner together with its Junk Items.
type Owner struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Total   int64  `json:"total"`
	Running bool   `json:"running"`
	Items   []Item `json:"items"`
}

// Skip is a path the scan refused to size, with the reason it refused.
type Skip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Summary is the arithmetic the user is asked to trust.
type Summary struct {
	TotalBytes int64    `json:"total_bytes"`
	Items      int      `json:"items"`
	FreedBytes int64    `json:"freed_bytes"`
	MovedBytes int64    `json:"moved_bytes"`
	Failed     []string `json:"failed"`
}

// Report is the schema v1 document.
type Report struct {
	Schema     int     `json:"schema"`
	ScannedAt  string  `json:"scanned_at"`
	DurationMS int64   `json:"duration_ms"`
	Owners     []Owner `json:"owners"`
	Skipped    []Skip  `json:"skipped"`
	Summary    Summary `json:"summary"`
}

// SchemaVersion identifies the JSON contract.
const SchemaVersion = 1

// Skip reasons.
const (
	ReasonNoPermission = "no-permission"
	ReasonVanished     = "vanished"
	ReasonTimeout      = "timeout"
	ReasonRunning      = "running"
	ReasonError        = "error"
)

const (
	defaultPathTimeout = 30 * time.Second
	tempMaxAge         = 7 * 24 * time.Hour
)

// Scan walks the rule set and returns the Report.
func Scan(ctx context.Context, opts Options) (Report, error) {
	started := time.Now()
	if opts.Now != nil {
		started = opts.Now()
	}
	if opts.Running == nil {
		opts.Running = func(owner.Owner) bool { return false }
	}
	if opts.PathTimeout <= 0 {
		opts.PathTimeout = defaultPathTimeout
	}

	expanded := applySystemRoot(rules.ExpandAll(opts.Rules, opts.Home, opts.TmpDir), opts.SystemRoot)
	ignore := rules.ExpandPatterns(opts.Ignore, opts.Home, opts.TmpDir)
	protected := selfPaths(opts)
	resolver := owner.NewResolver(opts.AppsDirs)

	report := Report{
		Schema:    SchemaVersion,
		ScannedAt: started.Format(time.RFC3339),
		Owners:    []Owner{},
		Skipped:   []Skip{},
		Summary:   Summary{Failed: []string{}},
	}

	var tasks []sizeTask
	for _, candidate := range candidates(ctx, expanded) {
		if ctx.Err() != nil {
			break
		}
		if excluded(candidate, protected) || rules.Ignored(candidate, ignore) {
			continue
		}
		rule, ok := rules.Match(candidate, expanded)
		if !ok {
			continue
		}

		res := resolver.Resolve(rule.Owner, filepath.Base(candidate))
		if rule.OrphansOnly && !res.Orphan {
			continue
		}
		if opts.Running(res) {
			report.Skipped = append(report.Skipped, Skip{Path: candidate, Reason: ReasonRunning})
			continue
		}
		if rule.Kind == rules.KindTemp && !olderThan(candidate, started, tempMaxAge) {
			continue
		}

		kind, risk := rule.Kind, rule.Risk
		if res.Orphan {
			kind, risk = rules.KindOrphan, rules.RiskCaution
		}
		tasks = append(tasks, sizeTask{path: candidate, rule: rule, own: res, kind: kind, risk: risk})
	}

	var progress Progress
	var scanErr error
	results := sizeAll(ctx, opts, expanded, tasks, func(path string) {
		progress.Current = path
		progress.Skipped = len(report.Skipped)
		emit(opts, progress)
	})
	if err := ctx.Err(); err != nil {
		scanErr = err
	}
	byID := map[string]*Owner{}
	for i, r := range results {
		report.Skipped = append(report.Skipped, r.skips...)
		if !r.done || !r.ok {
			continue
		}
		t := tasks[i]
		item := Item{
			Path:        t.path,
			Size:        r.size,
			Kind:        t.kind,
			Risk:        t.risk,
			Rebuild:     t.rule.Rebuild,
			Recommended: Recommended(t.risk, t.rule.Rebuild, false, t.own.Recognized, r.size),
			Rule:        t.rule.Name,
			Note:        t.rule.Note,
		}

		o, ok := byID[t.own.ID]
		if !ok {
			o = &Owner{ID: t.own.ID, Name: t.own.Name, Items: []Item{}}
			byID[t.own.ID] = o
		}
		o.Items = append(o.Items, item)
		o.Total += r.size

		progress.Current = t.path
		progress.Items++
		progress.Bytes += r.size
		progress.Skipped = len(report.Skipped)
		emit(opts, progress)
	}

	for _, o := range byID {
		sort.Slice(o.Items, func(i, j int) bool { return o.Items[i].Size > o.Items[j].Size })
		report.Owners = append(report.Owners, *o)
		report.Summary.Items += len(o.Items)
		report.Summary.TotalBytes += o.Total
	}
	sort.Slice(report.Owners, func(i, j int) bool {
		if report.Owners[i].Total != report.Owners[j].Total {
			return report.Owners[i].Total > report.Owners[j].Total
		}
		return report.Owners[i].ID < report.Owners[j].ID
	})
	sort.Slice(report.Skipped, func(i, j int) bool { return report.Skipped[i].Path < report.Skipped[j].Path })

	elapsed := time.Since(started)
	if opts.Now != nil {
		elapsed = opts.Now().Sub(started)
	}
	report.DurationMS = elapsed.Milliseconds()

	// One last event so a stats bar lands on the final numbers even if the
	// consumer ignores the Report that follows. A cancelled scan still returns
	// what it sized, plus the cancellation.
	progress.Current = ""
	emit(opts, progress)
	return report, scanErr
}

type sizeTask struct {
	path string
	rule rules.Rule
	own  owner.Owner
	kind rules.Kind
	risk rules.Risk
}

type sizeResult struct {
	done  bool
	ok    bool
	size  int64
	skips []Skip
}

// sizeAll walks candidates with bounded parallelism. Owners were resolved
// before this point, so the resolver is never shared across goroutines.
func sizeAll(ctx context.Context, opts Options, rs []rules.Rule, tasks []sizeTask, onStart func(string)) []sizeResult {
	results := make([]sizeResult, len(tasks))
	if len(tasks) == 0 {
		return results
	}

	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU() / 2
	}
	if jobs < 1 {
		jobs = 1
	}
	if jobs > 8 {
		jobs = 8
	}

	index := make(chan int, len(tasks))
	for i := range tasks {
		index <- i
	}
	close(index)

	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range index {
				if ctx.Err() != nil {
					return
				}
				t := tasks[i]
				if onStart != nil {
					onStart(t.path)
				}
				size, skips, ok, err := duBytes(ctx, t.path, t.rule, rs, opts.PathTimeout)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					skips = append(skips, Skip{Path: t.path, Reason: ReasonError})
					results[i] = sizeResult{done: true, skips: skips}
					continue
				}
				results[i] = sizeResult{done: true, ok: ok, size: size, skips: skips}
			}
		}()
	}
	wg.Wait()
	return results
}

// olderThan reports whether a temp-scope candidate is old enough to consider.
// A path that cannot be inspected is left to the sizer, which records the
// appropriate skip reason.
func olderThan(path string, now time.Time, age time.Duration) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return true
	}
	return info.ModTime().Before(now.Add(-age))
}

// candidates lists every path the rule set could claim: the children matched
// by each wildcard pattern, plus each exact pattern that exists. Nested
// candidates owned by the same rule are pruned to the shallower one so that a
// rule can never create two Junk Items that contain the same bytes.
func candidates(ctx context.Context, rs []rules.Rule) []string {
	seen := map[string]bool{}
	var raw []string
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		raw = append(raw, p)
	}

ruleLoop:
	for _, r := range rs {
		for _, pattern := range r.PathPatterns() {
			if ctx.Err() != nil {
				break ruleLoop
			}
			if !strings.Contains(pattern, "*") {
				if _, err := os.Lstat(pattern); err == nil {
					add(pattern)
				}
				continue
			}

			if !strings.Contains(pattern, "**") {
				matches, err := filepath.Glob(pattern)
				if err != nil {
					continue
				}
				for _, m := range matches {
					add(m)
				}
				continue
			}

			base, _ := rules.WildcardBase(pattern)
			if _, err := os.Lstat(base); err != nil {
				continue
			}
			_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
				if ctx.Err() != nil {
					return filepath.SkipAll
				}
				if err != nil {
					return nil
				}
				if p != base && rules.Matches(pattern, p) {
					add(p)
				}
				return nil
			})
		}
	}

	sort.Slice(raw, func(i, j int) bool {
		if len(raw[i]) != len(raw[j]) {
			return len(raw[i]) < len(raw[j])
		}
		return raw[i] < raw[j]
	})

	type selected struct {
		path string
		rule rules.Rule
	}
	var out []string
	var kept []selected
	for _, candidate := range raw {
		rule, ok := rules.Match(candidate, rs)
		if !ok {
			continue
		}
		nested := false
		for _, s := range kept {
			if within(candidate, s.path) && sameRule(rule, s.rule) {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		kept = append(kept, selected{path: candidate, rule: rule})
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out
}

// duBytes sums disk usage the way du does: allocated blocks, never apparent
// size, and never following symlinks. A subtree claimed by a different rule is
// not counted here: it is its own Junk Item, which is what keeps the headline
// total free of double counting.
//
// ok is false when the item itself could not be sized (unreadable, vanished or
// timed out); skips always carries the reason. A parent context cancellation is
// returned as an error so the caller can stop without inventing a partial item.
func duBytes(ctx context.Context, root string, rule rules.Rule, rs []rules.Rule, timeout time.Duration) (int64, []Skip, bool, error) {
	var total int64
	var skips []Skip
	rootFailed := false

	pathCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		pathCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctxErr := pathCtx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			skips = append(skips, Skip{Path: path, Reason: ReasonFor(err)})
			if path == root {
				rootFailed = true
				return filepath.SkipAll
			}
			return nil
		}
		if path != root {
			if other, ok := rules.Match(path, rs); ok && !sameRule(other, rule) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			skips = append(skips, Skip{Path: path, Reason: ReasonFor(infoErr)})
			if path == root {
				rootFailed = true
				return filepath.SkipAll
			}
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			total += st.Blocks * 512
		} else {
			total += info.Size()
		}
		return nil
	})

	if err := ctx.Err(); err != nil {
		return total, skips, false, err
	}
	if pathCtx.Err() != nil {
		skips = append(skips, Skip{Path: root, Reason: ReasonTimeout})
		return total, skips, false, nil
	}
	if walkErr != nil && walkErr != filepath.SkipAll {
		skips = append(skips, Skip{Path: root, Reason: ReasonFor(walkErr)})
		rootFailed = true
	}
	return total, skips, !rootFailed, nil
}

// sameRule compares the rule identity used for pruning and for partitioning a
// walk. Name is the stable identity; direct callers that build rules by hand
// fall back to the pattern.
func sameRule(a, b rules.Rule) bool {
	if a.Name != "" || b.Name != "" {
		return a.Name == b.Name
	}
	return a.Pattern == b.Pattern && a.Owner == b.Owner && a.Kind == b.Kind
}

// within reports whether target is root or a descendant of it.
func within(target, root string) bool {
	target = filepath.Clean(target)
	root = filepath.Clean(root)
	return target == root || strings.HasPrefix(target, root+string(os.PathSeparator))
}

// excluded reports whether target overlaps a Self-Exclusion boundary. A parent
// of a protected path is excluded too: deleting it would delete the tool's own
// state.
func excluded(target string, protected []string) bool {
	for _, p := range protected {
		if within(target, p) || within(p, target) {
			return true
		}
	}
	return false
}

// selfPaths returns the tool's own binary, config and state paths. Empty
// fields are derived from Home so the invariant holds even for callers that do
// not wire the CLI's options.
func selfPaths(opts Options) []string {
	var out []string
	configDir := opts.ConfigDir
	if configDir == "" && opts.Home != "" {
		configDir = filepath.Join(opts.Home, ".config", "free-clean-my-macos")
	}
	if configDir != "" {
		out = append(out, configDir)
	}
	stateDir := opts.StateDir
	if stateDir == "" && opts.Home != "" {
		stateDir = filepath.Join(opts.Home, ".local", "state", "free-clean-my-macos")
	}
	if stateDir != "" {
		out = append(out, stateDir)
	}
	binary := opts.SelfBinary
	if binary == "" {
		if exe, err := os.Executable(); err == nil {
			binary = exe
		}
	}
	if binary != "" {
		out = append(out, binary)
	}
	return out
}

// applySystemRoot is the test seam for the absolute /Library rules: with
// SystemRoot set, /Library/Caches/* is looked up inside the fixture instead of
// the real machine.
func applySystemRoot(rs []rules.Rule, root string) []rules.Rule {
	if root == "" || root == "/" {
		return rs
	}
	out := make([]rules.Rule, len(rs))
	for i, r := range rs {
		if len(r.Patterns) > 0 {
			patterns := make([]string, len(r.Patterns))
			for j, p := range r.Patterns {
				patterns[j] = relocateLibrary(p, root)
			}
			r.Patterns = patterns
		} else {
			r.Pattern = relocateLibrary(r.Pattern, root)
		}
		out[i] = r
	}
	return out
}

func relocateLibrary(pattern, root string) string {
	if !strings.HasPrefix(pattern, "/Library/") {
		return pattern
	}
	return filepath.Join(root, pattern)
}

func ReasonFor(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return ReasonNoPermission
	case errors.Is(err, fs.ErrNotExist):
		return ReasonVanished
	default:
		return ReasonError
	}
}
