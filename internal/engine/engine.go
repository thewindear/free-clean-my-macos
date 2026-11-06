// Package engine is the whole scanner. The TUI and the CLI are consumers of
// the same Report; sizes, totals and recommendation flags are computed here
// once and never recomputed downstream.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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
	Now      func() time.Time
	Running  func(ownerID string) bool
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

	expanded := rules.ExpandAll(opts.Rules, opts.Home, opts.TmpDir)
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
		rule, ok := rules.Match(candidate, expanded)
		if !ok {
			continue
		}

		res := resolver.Resolve(rule.Owner, filepath.Base(candidate))
		kind, risk := rule.Kind, rule.Risk
		if res.Orphan {
			kind, risk = rules.KindOrphan, rules.RiskCaution
		}
		running := opts.Running(res.ID)

		size, skips, err := duBytes(ctx, candidate)
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

// candidates lists every path the rule set could claim: the children of each
// wildcard pattern, plus each exact pattern that exists.
func candidates(ctx context.Context, rs []rules.Rule) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}

	for _, r := range rs {
		if err := ctx.Err(); err != nil {
			break
		}
		base, wildcard := rules.WildcardBase(r.Pattern)
		if wildcard {
			entries, err := os.ReadDir(base)
			if err != nil {
				continue
			}
			for _, e := range entries {
				add(filepath.Join(base, e.Name()))
			}
			continue
		}
		if _, err := os.Lstat(r.Pattern); err == nil {
			add(r.Pattern)
		}
	}
	sort.Strings(out)
	return out
}

// duBytes sums disk usage the way du does: allocated blocks, never apparent
// size, and never following symlinks.
func duBytes(ctx context.Context, root string) (int64, []Skip, error) {
	var total int64
	var skips []Skip

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			skips = append(skips, Skip{Path: path, Reason: reasonFor(err)})
			if path == root {
				return filepath.SkipAll
			}
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			skips = append(skips, Skip{Path: path, Reason: reasonFor(infoErr)})
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
		skips = append(skips, Skip{Path: root, Reason: reasonFor(walkErr)})
	}
	return total, skips, nil
}

func reasonFor(err error) string {
	switch {
	case os.IsPermission(err):
		return ReasonNoPermission
	case os.IsNotExist(err):
		return ReasonVanished
	default:
		return ReasonError
	}
}
