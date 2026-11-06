// Package cli is the fcm command line: flag parsing, output, exit codes.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/thewindear/free-clean-my-macos/internal/clean"
	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/format"
	"github.com/thewindear/free-clean-my-macos/internal/owner"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
	"github.com/thewindear/free-clean-my-macos/internal/ui"
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
  fcm                      open the list screen
  fcm scan [--json] [--compact] [--rules-dir DIR] [--jobs N]
                           scan and report, exit 3 when there is nothing to clean
  fcm clean --dry-run [--json] [--only-kind cache,log] [--jobs N]
                           print the removal plan and touch nothing
  fcm clean --yes [--json] [--trash-dir DIR] [--only-kind cache,log] [--jobs N]
                           remove every Junk Item: cheap ones deleted outright,
                           the rest moved into the Trash
  fcm help                 show this message

User rules live in ~/.config/free-clean-my-macos/rules.d/*.yaml; a rule with the
same name as an embedded one replaces it. ~/.config/free-clean-my-macos/ignore.yaml
excludes path patterns from every scan.
`

// Run executes one command line and returns the process exit code. The
// context is cancelled by SIGINT/SIGTERM, so an interrupted scan still emits
// the partial Report it has.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) == 0 {
		return runList(stdout, stderr)
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return ExitOK
	case "scan":
		return runScan(ctx, args[1:], stdout, stderr)
	case "clean":
		return runClean(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "fcm: unknown command %q\n\n%s", args[0], usage)
		return ExitUsage
	}
}

// runList opens the list screen. Without a terminal there is no way to read a
// keypress, so fcm says so and points at the scriptable commands instead of
// hanging or printing escape codes into a pipe.
func runList(stdout, stderr io.Writer) int {
	if !ui.Supported() {
		fmt.Fprintln(stderr, "fcm: the list screen needs a terminal; use `fcm scan --json` or `fcm clean`")
		return ExitUsage
	}
	opts, err := options("", 0)
	if err != nil {
		fmt.Fprintf(stderr, "fcm: %v\n", err)
		return ExitPartialFailed
	}
	if err := ui.Run(stdout, opts, cleanOptions("", false)); err != nil {
		fmt.Fprintf(stderr, "fcm: %v\n", err)
		return ExitPartialFailed
	}
	return ExitOK
}

// cleanOptions is where a Cleanup puts what it removes and logs what it did. The
// CLI passes its flags in; the list screen uses the defaults, so both paths
// write to the same Trash and the same Cleanup Log.
func cleanOptions(trashDir string, dryRun bool) clean.Options {
	home := os.Getenv("HOME")
	trash := trashDir
	if trash == "" {
		trash = filepath.Join(home, ".Trash")
	}
	return clean.Options{
		TrashDir: trash,
		StateDir: stateDir(home),
		DryRun:   dryRun,
	}
}

func runScan(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit the schema v1 JSON document")
	compact := fs.Bool("compact", false, "emit single-line JSON (implies --json)")
	rulesDir := fs.String("rules-dir", "", "load user Scan Rules from this directory")
	jobs := fs.Int("jobs", 0, "bounded scan parallelism (default min(8, NumCPU/2))")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *jobs < 0 {
		fmt.Fprintln(stderr, "fcm scan: --jobs must be positive")
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "fcm scan: unexpected argument %q\n", fs.Arg(0))
		return ExitUsage
	}
	if *compact {
		*asJSON = true
	}

	opts, err := options(*rulesDir, *jobs)
	if err != nil {
		fmt.Fprintf(stderr, "fcm scan: %v\n", err)
		return ExitPartialFailed
	}
	report, err := engine.Scan(ctx, opts)
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

	// A run that found nothing but did skip unreadable/running/timed-out
	// paths is still a successful, honest scan: it is not the same as a clean
	// machine, so it does not get the nothing-to-do exit code.
	if report.Summary.Items == 0 && len(report.Skipped) == 0 {
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
func runClean(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "print the plan and touch nothing")
	yes := fs.Bool("yes", false, "execute the plan")
	asJSON := fs.Bool("json", false, "emit the schema v1 JSON document")
	compact := fs.Bool("compact", false, "emit single-line JSON (implies --json)")
	trashDir := fs.String("trash-dir", "", "where items that are not deleted outright are moved (default $HOME/.Trash)")
	onlyKind := fs.String("only-kind", "", "comma-separated Kind filter, e.g. cache,log")
	rulesDir := fs.String("rules-dir", "", "load user Scan Rules from this directory")
	jobs := fs.Int("jobs", 0, "bounded scan parallelism (default min(8, NumCPU/2))")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *jobs < 0 {
		fmt.Fprintln(stderr, "fcm clean: --jobs must be positive")
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

	opts, err := options(*rulesDir, *jobs)
	if err != nil {
		fmt.Fprintf(stderr, "fcm clean: %v\n", err)
		return ExitPartialFailed
	}
	report, err := engine.Scan(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "fcm clean: %v\n", err)
		return ExitPartialFailed
	}

	result, err := clean.Run(ctx, clean.Select(report, kinds), cleanOptions(*trashDir, *dryRun))
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
	fmt.Fprintf(w, "%s %d items · %s\n\n", verb, len(result.Items), format.Bytes(total))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, it := range result.Items {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", outcomeLabel(it), abbreviateHome(it.Path), format.Bytes(it.Size))
	}
	tw.Flush()

	freed, moved := "Space Freed", "Space Moved"
	if result.DryRun {
		freed, moved = freed+" (planned)", moved+" (planned)"
	}
	fmt.Fprintf(w, "\n%s  %s\n%s  %s\n", freed, format.Bytes(result.Summary.FreedBytes), moved, format.Bytes(result.Summary.MovedBytes))

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
	total := format.Bytes(report.Summary.TotalBytes)
	fmt.Fprintf(w, "Scanned %d items in %dms · %s reclaimable · %d skipped\n\n",
		report.Summary.Items, report.DurationMS, total, len(report.Skipped))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, o := range report.Owners {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", ownerLabel(o), format.Bytes(o.Total), format.Count(len(o.Items), "item"))
		folded, foldedBytes := 0, int64(0)
		for _, it := range o.Items {
			if it.Size < engine.FoldBelow {
				folded++
				foldedBytes += it.Size
				continue
			}
			mark := " "
			if it.Recommended {
				mark = "✓"
			}
			fmt.Fprintf(tw, "    %s %s\t%s\t%s\n", mark, abbreviateHome(it.Path), format.Bytes(it.Size), it.Kind)
		}
		if folded > 0 {
			fmt.Fprintf(tw, "      %s\t%s\tfolded\n", format.Count(folded, "smaller item"), format.Bytes(foldedBytes))
		}
	}
	tw.Flush()

	for _, s := range report.Skipped {
		fmt.Fprintf(w, "  skipped %s (%s)\n", abbreviateHome(s.Path), s.Reason)
	}
}

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

// abbreviateHome is the Display Path, with HOME taken from the environment for
// the text listings.
func abbreviateHome(path string) string {
	return format.Home(os.Getenv("HOME"), path)
}

func options(rulesDir string, jobs int) (engine.Options, error) {
	home := os.Getenv("HOME")
	configDir := filepath.Join(home, ".config", "free-clean-my-macos")
	if rulesDir == "" {
		rulesDir = filepath.Join(configDir, "rules.d")
	}

	userRules, err := rules.LoadDir(rulesDir)
	if err != nil {
		return engine.Options{}, fmt.Errorf("loading user rules: %w", err)
	}
	ignore, err := rules.LoadIgnore(filepath.Join(configDir, "ignore.yaml"))
	if err != nil {
		return engine.Options{}, fmt.Errorf("loading ignore list: %w", err)
	}

	timeout, err := pathTimeout()
	if err != nil {
		return engine.Options{}, err
	}
	now, err := fixedNow()
	if err != nil {
		return engine.Options{}, err
	}

	selfBinary, _ := os.Executable()
	return engine.Options{
		Home:        home,
		TmpDir:      os.Getenv("TMPDIR"),
		Rules:       rules.Merge(rules.Default(), userRules),
		AppsDirs:    appsDirs(home),
		Ignore:      ignore,
		ConfigDir:   configDir,
		StateDir:    stateDir(home),
		SelfBinary:  selfBinary,
		SystemRoot:  os.Getenv("FCM_SYSTEM_ROOT"),
		PathTimeout: timeout,
		Jobs:        jobs,
		Now:         now,
		Running:     runningProbe(),
	}, nil
}

// runningProbe reads the real process table unless FCM_RUNNING_OWNERS is set.
// The override exists so process-boundary tests can isolate themselves from
// apps that happen to be running on the developer's Mac.
func runningProbe() func(owner.Owner) bool {
	raw, set := os.LookupEnv("FCM_RUNNING_OWNERS")
	if !set {
		return owner.NewRunningProbe()
	}
	var processes []string
	for _, name := range strings.Split(raw, ",") {
		if name = strings.TrimSpace(name); name != "" {
			processes = append(processes, name)
		}
	}
	return func(o owner.Owner) bool { return owner.IsRunning(o, processes) }
}

// pathTimeout reads the FCM_PATH_TIMEOUT test seam. Production scans use the
// engine's 30 second default.
func pathTimeout() (time.Duration, error) {
	raw := os.Getenv("FCM_PATH_TIMEOUT")
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("FCM_PATH_TIMEOUT: %w", err)
	}
	return d, nil
}

// fixedNow reads the FCM_NOW test seam so temp-expiry tests can inject a clock.
func fixedNow() (func() time.Time, error) {
	raw := os.Getenv("FCM_NOW")
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("FCM_NOW: %w", err)
	}
	return func() time.Time { return t }, nil
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
