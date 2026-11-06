// Package cli is the fcm command line: flag parsing, output, exit codes.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/thewindear/free-clean-my-macos/internal/clean"
	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

// Exit codes.
const (
	ExitOK            = 0
	ExitPartialFailed = 1
	ExitUsage         = 2
	ExitNothingToDo   = 3
)

const usage = `fcm — whitelist-based macOS junk scanner

Usage:
  fcm                      open the TUI (not built yet)
  fcm scan [--json] [--compact]
                           scan and report, exit 3 when there is nothing to clean
  fcm clean --dry-run [--json] [--only-kind cache,log]
                           print the removal plan and touch nothing
  fcm clean --yes [--json] [--trash-dir DIR] [--only-kind cache,log]
                           remove every Junk Item: cheap ones deleted outright,
                           the rest moved into the Trash
  fcm help                 show this message
`

// Run executes one command line and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "fcm: the TUI is not built yet; run `fcm scan --json`")
		return ExitUsage
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return ExitOK
	case "scan":
		return runScan(args[1:], stdout, stderr)
	case "clean":
		return runClean(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "fcm: unknown command %q\n\n%s", args[0], usage)
		return ExitUsage
	}
}

func runScan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit the schema v1 JSON document")
	compact := fs.Bool("compact", false, "emit single-line JSON (implies --json)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "fcm scan: unexpected argument %q\n", fs.Arg(0))
		return ExitUsage
	}
	if *compact {
		*asJSON = true
	}

	report, err := engine.Scan(context.Background(), options())
	if err != nil {
		fmt.Fprintf(stderr, "fcm scan: %v\n", err)
		return ExitPartialFailed
	}

	if *asJSON {
		if err := writeJSON(stdout, report, *compact); err != nil {
			fmt.Fprintf(stderr, "fcm scan: %v\n", err)
			return ExitUsage
		}
	} else {
		writeText(stdout, report)
	}

	if report.Summary.Items == 0 {
		return ExitNothingToDo
	}
	return ExitOK
}

// writeJSON emits one document, indented unless --compact asked for a single
// line.
func writeJSON[T any](w io.Writer, doc T, compact bool) error {
	enc := json.NewEncoder(w)
	if !compact {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(doc)
}

// runClean scans, then removes the selection according to the hybrid policy.
// --dry-run prints the plan and touches nothing; --yes executes it, and the
// Cleanup Log makes the execution auditable after the fact.
func runClean(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "print the plan and touch nothing")
	yes := fs.Bool("yes", false, "execute the plan")
	asJSON := fs.Bool("json", false, "emit the schema v1 JSON document")
	compact := fs.Bool("compact", false, "emit single-line JSON (implies --json)")
	trashDir := fs.String("trash-dir", "", "where items that are not deleted outright are moved (default $HOME/.Trash)")
	onlyKind := fs.String("only-kind", "", "comma-separated Kind filter, e.g. cache,log")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "fcm clean: unexpected argument %q\n", fs.Arg(0))
		return ExitUsage
	}
	if *compact {
		*asJSON = true
	}
	if *dryRun == *yes {
		fmt.Fprintln(stderr, "fcm clean: pass either --dry-run to preview or --yes to execute")
		return ExitUsage
	}
	kinds, err := parseKinds(*onlyKind)
	if err != nil {
		fmt.Fprintf(stderr, "fcm clean: %v\n", err)
		return ExitUsage
	}

	report, err := engine.Scan(context.Background(), options())
	if err != nil {
		fmt.Fprintf(stderr, "fcm clean: %v\n", err)
		return ExitPartialFailed
	}

	home := os.Getenv("HOME")
	trash := *trashDir
	if trash == "" {
		trash = filepath.Join(home, ".Trash")
	}
	result, err := clean.Run(context.Background(), report, clean.Options{
		TrashDir:  trash,
		StateDir:  stateDir(home),
		DryRun:    *dryRun,
		OnlyKinds: kinds,
	})
	if err != nil {
		fmt.Fprintf(stderr, "fcm clean: %v\n", err)
		return ExitPartialFailed
	}

	if *asJSON {
		if err := writeJSON(stdout, result, *compact); err != nil {
			fmt.Fprintf(stderr, "fcm clean: %v\n", err)
			return ExitUsage
		}
	} else {
		writeCleanText(stdout, result)
	}

	if len(result.Summary.Failed) > 0 {
		return ExitPartialFailed
	}
	if len(result.Items) == 0 {
		return ExitNothingToDo
	}
	return ExitOK
}

// writeCleanText prints the plan or the Summary. Space Freed and Space Moved
// are always separate numbers, so no run ever claims space it only moved.
func writeCleanText(w io.Writer, result clean.Result) {
	verb := "Cleaned"
	if result.DryRun {
		verb = "Would clean"
	}
	total := result.Summary.FreedBytes + result.Summary.MovedBytes
	fmt.Fprintf(w, "%s %d items · %s\n\n", verb, len(result.Items), humanBytes(total))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, it := range result.Items {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", outcomeLabel(it), abbreviateHome(it.Path), humanBytes(it.Size))
	}
	tw.Flush()

	freed, moved := "Space Freed", "Space Moved"
	if result.DryRun {
		freed, moved = freed+" (planned)", moved+" (planned)"
	}
	fmt.Fprintf(w, "\n%s  %s\n%s  %s\n", freed, humanBytes(result.Summary.FreedBytes), moved, humanBytes(result.Summary.MovedBytes))

	if len(result.Summary.Failed) > 0 {
		fmt.Fprintf(w, "\n%d failed:\n", len(result.Summary.Failed))
		for _, f := range result.Summary.Failed {
			fmt.Fprintf(w, "  %s (%s)\n", abbreviateHome(f.Path), f.Reason)
		}
	}
	if result.Summary.LogPath != "" {
		fmt.Fprintf(w, "\nCleanup Log  %s\n", abbreviateHome(result.Summary.LogPath))
	}
	if result.Summary.MovedBytes > 0 {
		fmt.Fprintf(w, "Space Moved is recoverable until you empty the Trash in Finder.\n")
	}
}

// outcomeLabel names one row of the plan: what will happen, or what did.
func outcomeLabel(it clean.Item) string {
	if it.Outcome == clean.OutcomeFailed {
		return "failed"
	}
	if it.Mode == clean.ModeDelete {
		return "delete"
	}
	return "trash"
}

// parseKinds validates the --only-kind selection against the domain's Kinds.
func parseKinds(list string) ([]rules.Kind, error) {
	if strings.TrimSpace(list) == "" {
		return nil, nil
	}
	var kinds []rules.Kind
	for _, raw := range strings.Split(list, ",") {
		kind := rules.Kind(strings.TrimSpace(raw))
		if !rules.ValidKind(kind) {
			return nil, fmt.Errorf("unknown kind %q", strings.TrimSpace(raw))
		}
		kinds = append(kinds, kind)
	}
	return kinds, nil
}

func writeText(w io.Writer, report engine.Report) {
	total := humanBytes(report.Summary.TotalBytes)
	fmt.Fprintf(w, "Scanned %d items in %dms · %s reclaimable · %d skipped\n\n",
		report.Summary.Items, report.DurationMS, total, len(report.Skipped))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, o := range report.Owners {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", ownerLabel(o), humanBytes(o.Total), plural(len(o.Items), "item"))
		folded, foldedBytes := 0, int64(0)
		for _, it := range o.Items {
			if it.Size < foldBelow {
				folded++
				foldedBytes += it.Size
				continue
			}
			mark := " "
			if it.Recommended {
				mark = "✓"
			}
			fmt.Fprintf(tw, "    %s %s\t%s\t%s\n", mark, abbreviateHome(it.Path), humanBytes(it.Size), it.Kind)
		}
		if folded > 0 {
			fmt.Fprintf(tw, "      %s\t%s\tfolded\n", plural(folded, "smaller item"), humanBytes(foldedBytes))
		}
	}
	tw.Flush()

	for _, s := range report.Skipped {
		fmt.Fprintf(w, "  skipped %s (%s)\n", abbreviateHome(s.Path), s.Reason)
	}
}

// foldBelow is the size under which items are folded into one row instead of
// being listed individually.
const foldBelow int64 = 10 * 1024 * 1024

// ownerLabel names an Owner for humans: a resolved display name plus its bundle
// id, or the raw directory name flagged as unrecognized.
func ownerLabel(o engine.Owner) string {
	switch {
	case o.Name == "unrecognized":
		return fmt.Sprintf("%s (unrecognized)", o.ID)
	case o.Name != o.ID:
		return fmt.Sprintf("%s (%s)", o.Name, o.ID)
	default:
		return o.Name
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// abbreviateHome is the Display Path: $HOME shown as ~ to fit the listing.
func abbreviateHome(path string) string {
	home := os.Getenv("HOME")
	if home != "" && strings.HasPrefix(path, home+"/") {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 3; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGT"[exp])
}

func options() engine.Options {
	home := os.Getenv("HOME")
	tmpdir := os.Getenv("TMPDIR")
	return engine.Options{
		Home:     home,
		TmpDir:   tmpdir,
		Rules:    rules.Default(),
		AppsDirs: appsDirs(home),
	}
}

// stateDir is where the Cleanup Log lives. It follows XDG_STATE_HOME when the
// user sets it and defaults to ~/.local/state.
func stateDir(home string) string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "free-clean-my-macos")
	}
	return filepath.Join(home, ".local", "state", "free-clean-my-macos")
}

// appsDirs is where installed app bundles are looked up. FCM_APPS_DIRS exists
// so the process-boundary tests can install fake bundles in a fixture HOME.
func appsDirs(home string) []string {
	if override := os.Getenv("FCM_APPS_DIRS"); override != "" {
		return filepath.SplitList(override)
	}
	return []string{
		"/Applications",
		"/Applications/Utilities",
		filepath.Join(home, "Applications"),
	}
}
