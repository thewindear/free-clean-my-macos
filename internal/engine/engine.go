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
	"sort"
	"strings"
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

	Now     func() time.Time
	Running func(ownerID string) bool
}

// Item is one Junk Item.
type Item struct {
	Path        string        `json:"path"`
	Size        int64         `json:"size"`
	Kind        rules.Kind    `json:"kind"`
	Risk        rules.Risk    `json:"risk"`
	Rebuild     rules.Rebuild `json:"rebuild"`
	Recommended bool          `json:"recommended"`
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

// Scan walks the rule set and returns the Report.
func Scan(ctx context.Context, opts Options) (Report, error) {
	started := time.Now()
	if opts.Now != nil {
		started = opts.Now()
	}
	if opts.Running == nil {
		opts.Running = func(string) bool { return false }
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

	byID := map[string]*Owner{}
	for _, candidate := range candidates(ctx, expanded) {
		if err := ctx.Err(); err != nil {
			return report, err
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
		kind, risk := rule.Kind, rule.Risk
		if res.Orphan {
			kind, risk = rules.KindOrphan, rules.RiskCaution
		}
		running := opts.Running(res.ID)

		size, skips, err := duBytes(ctx, candidate, rule, expanded)
		if err != nil {
			return report, err
		}
		report.Skipped = append(report.Skipped, skips...)

		item := Item{
			Path:        candidate,
			Size:        size,
			Kind:        kind,
			Risk:        risk,
			Rebuild:     rule.Rebuild,
			Recommended: Recommended(risk, rule.Rebuild, running, res.Recognized, size),
		}

		o, ok := byID[res.ID]
		if !ok {
			o = &Owner{ID: res.ID, Name: res.Name, Running: running, Items: []Item{}}
			byID[res.ID] = o
		}
		o.Items = append(o.Items, item)
		o.Total += size
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
	return report, nil
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

	for _, r := range rs {
		for _, pattern := range r.PathPatterns() {
			if err := ctx.Err(); err != nil {
				break
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
func duBytes(ctx context.Context, root string, rule rules.Rule, rs []rules.Rule) (int64, []Skip, error) {
	var total int64
	var skips []Skip

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			skips = append(skips, Skip{Path: path, Reason: ReasonFor(err)})
			if path == root {
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
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			total += st.Blocks * 512
		} else {
			total += info.Size()
		}
		return nil
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return total, skips, ctxErr
	}
	if walkErr != nil && walkErr != filepath.SkipAll {
		skips = append(skips, Skip{Path: root, Reason: ReasonFor(walkErr)})
	}
	return total, skips, nil
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
