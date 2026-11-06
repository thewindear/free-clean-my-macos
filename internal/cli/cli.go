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
  fcm scan [--json] [--compact] [--rules-dir DIR]
                           scan and report, exit 3 when there is nothing to clean
  fcm help                 show this message

User rules live in ~/.config/free-clean-my-macos/rules.d/*.yaml; a rule with the
same name as an embedded one replaces it. ~/.config/free-clean-my-macos/ignore.yaml
excludes path patterns from every scan.
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
	rulesDir := fs.String("rules-dir", "", "load user Scan Rules from this directory")
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

	opts, err := options(*rulesDir)
	if err != nil {
		fmt.Fprintf(stderr, "fcm scan: %v\n", err)
		return ExitPartialFailed
	}
	report, err := engine.Scan(context.Background(), opts)
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

func writeJSON(w io.Writer, report engine.Report, compact bool) error {
	enc := json.NewEncoder(w)
	if !compact {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(report)
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

func options(rulesDir string) (engine.Options, error) {
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

	selfBinary, _ := os.Executable()
	return engine.Options{
		Home:       home,
		TmpDir:     os.Getenv("TMPDIR"),
		Rules:      rules.Merge(rules.Default(), userRules),
		AppsDirs:   appsDirs(home),
		Ignore:     ignore,
		ConfigDir:  configDir,
		StateDir:   filepath.Join(home, ".local", "state", "free-clean-my-macos"),
		SelfBinary: selfBinary,
		SystemRoot: os.Getenv("FCM_SYSTEM_ROOT"),
	}, nil
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
