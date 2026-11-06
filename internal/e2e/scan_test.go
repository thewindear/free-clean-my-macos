package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	// Blank imports keep the test cache honest: these tests exercise the
	// freshly built binary, so a change here must invalidate the cached pass.
	_ "github.com/thewindear/free-clean-my-macos/internal/cli"
	_ "github.com/thewindear/free-clean-my-macos/internal/engine"
	_ "github.com/thewindear/free-clean-my-macos/internal/owner"
	_ "github.com/thewindear/free-clean-my-macos/internal/rules"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fcm-bin")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "fcm")
	build := exec.Command("go", "build", "-o", binary, "github.com/thewindear/free-clean-my-macos")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// --- document shape -------------------------------------------------------

type itemDoc struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	Kind        string `json:"kind"`
	Risk        string `json:"risk"`
	Rebuild     string `json:"rebuild"`
	Recommended bool   `json:"recommended"`
}

type ownerDoc struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Total   int64     `json:"total"`
	Running bool      `json:"running"`
	Items   []itemDoc `json:"items"`
}

type skipDoc struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type summaryDoc struct {
	TotalBytes int64 `json:"total_bytes"`
	Items      int   `json:"items"`
	FreedBytes int64 `json:"freed_bytes"`
	MovedBytes int64 `json:"moved_bytes"`
}

type doc struct {
	Schema     int        `json:"schema"`
	ScannedAt  string     `json:"scanned_at"`
	DurationMS int64      `json:"duration_ms"`
	Owners     []ownerDoc `json:"owners"`
	Skipped    []skipDoc  `json:"skipped"`
	Summary    summaryDoc `json:"summary"`
}

// --- fixture --------------------------------------------------------------

type fixture struct {
	home   string
	caches string
	apps   string
	system string
	tmp    string
	env    map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	f := &fixture{
		home:   home,
		caches: filepath.Join(home, "Library", "Caches"),
		apps:   filepath.Join(home, "Applications"),
		system: filepath.Join(home, "system"),
		tmp:    filepath.Join(home, "tmp"),
		env:    map[string]string{"FCM_RUNNING_OWNERS": ""},
	}
	for _, d := range []string{f.caches, f.apps, f.system, f.tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// addCache creates a cache directory holding one file of the given size.
func (f *fixture) addCache(t *testing.T, dir string, size int64) string {
	t.Helper()
	path := filepath.Join(f.caches, dir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blob.bin"), bytes.Repeat([]byte("x"), int(size)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// addPath creates a path under HOME holding one file of the given size.
func (f *fixture) addPath(t *testing.T, rel string, size int64) string {
	t.Helper()
	return f.addFile(t, filepath.Join(f.home, rel), size)
}

// addSystemPath creates a path under the relocated /Library root.
func (f *fixture) addSystemPath(t *testing.T, rel string, size int64) string {
	t.Helper()
	return f.addFile(t, filepath.Join(f.system, rel), size)
}

// addTempPath creates a path under the fixture TMPDIR.
func (f *fixture) addTempPath(t *testing.T, rel string, size int64) string {
	t.Helper()
	return f.addFile(t, filepath.Join(f.tmp, rel), size)
}

func (f *fixture) addFile(t *testing.T, path string, size int64) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blob.bin"), bytes.Repeat([]byte("x"), int(size)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// addManyFiles makes a directory slow enough to walk to exercise budgets and
// cancellation without any artificial delays in the scanner.
func (f *fixture) addManyFiles(t *testing.T, path string, count int) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("f%06d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func setModTime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func skipFor(skips []skipDoc, path string) *skipDoc {
	for i := range skips {
		if skips[i].Path == path {
			return &skips[i]
		}
	}
	return nil
}

// setenv overrides one environment variable for later runs of the binary.
func (f *fixture) setenv(key, value string) { f.env[key] = value }

func (f *fixture) unsetenv(key string) { delete(f.env, key) }

// agePath moves a path's modification time into the past.
func (f *fixture) agePath(t *testing.T, path string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// writeConfig writes a file under the tool's config directory.
func (f *fixture) writeConfig(t *testing.T, rel, content string) string {
	t.Helper()
	path := filepath.Join(f.home, ".config", "free-clean-my-macos", rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// addApp installs a fake app bundle declaring the given bundle identifier.
func (f *fixture) addApp(t *testing.T, bundleID, displayName string) {
	t.Helper()
	contents := filepath.Join(f.apps, displayName+".app", "Contents")
	if err := os.MkdirAll(contents, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>` + bundleID + `</string>
<key>CFBundleName</key><string>` + displayName + `</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) environ() []string {
	env := []string{
		"HOME=" + f.home,
		"TMPDIR=" + f.tmp + "/",
		"FCM_APPS_DIRS=" + f.apps,
		"FCM_SYSTEM_ROOT=" + f.system,
		"PATH=" + os.Getenv("PATH"),
	}
	for key, value := range f.env {
		env = append(env, key+"="+value)
	}
	return env
}

func (f *fixture) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = f.environ()
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running fcm: %v (stderr: %s)", err, errBuf.String())
	}
	return out.String(), errBuf.String(), code
}

func (f *fixture) scanJSON(t *testing.T, args ...string) doc {
	t.Helper()
	stdout, stderr, code := f.run(t, append([]string{"scan", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("scan exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	var d doc
	if err := json.Unmarshal([]byte(stdout), &d); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	return d
}

// --- tests ----------------------------------------------------------------

func TestScanJSONDocumentShape(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	slack := f.addCache(t, "com.example.Slack", 12*1024*1024)
	f.addCache(t, "Arc", 1*1024*1024)

	d := f.scanJSON(t)

	if d.Schema != 1 {
		t.Errorf("schema = %d, want 1", d.Schema)
	}
	if d.ScannedAt == "" {
		t.Error("scanned_at is empty")
	}
	if d.DurationMS < 0 {
		t.Errorf("duration_ms = %d", d.DurationMS)
	}
	if len(d.Owners) != 2 {
		t.Fatalf("owners = %d, want 2 (%+v)", len(d.Owners), d.Owners)
	}
	for _, o := range d.Owners {
		for _, it := range o.Items {
			if !filepath.IsAbs(it.Path) {
				t.Errorf("item path %q is not absolute", it.Path)
			}
			if it.Size <= 0 {
				t.Errorf("item %q size = %d", it.Path, it.Size)
			}
			if it.Kind != "cache" {
				t.Errorf("item %q kind = %q, want cache", it.Path, it.Kind)
			}
		}
	}
	found := false
	for _, o := range d.Owners {
		for _, it := range o.Items {
			if it.Path == slack {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("cache directory %q missing from the report", slack)
	}
	if d.Summary.Items != 2 {
		t.Errorf("summary.items = %d, want 2", d.Summary.Items)
	}
}

func TestSizesMatchDu(t *testing.T) {
	f := newFixture(t)
	path := f.addCache(t, "com.example.Slack", 12*1024*1024)

	d := f.scanJSON(t)
	var got int64
	for _, o := range d.Owners {
		for _, it := range o.Items {
			if it.Path == path {
				got = it.Size
			}
		}
	}
	if got == 0 {
		t.Fatalf("no item for %q", path)
	}

	out, err := exec.Command("du", "-s", path).Output()
	if err != nil {
		t.Fatalf("du: %v", err)
	}
	fields := strings.Fields(string(out))
	blocks, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		t.Fatalf("parsing du output %q: %v", out, err)
	}
	want := blocks * 512
	if got != want {
		t.Fatalf("size = %d, du says %d", got, want)
	}
}

func TestOwnerTotalsAreTheSumOfItems(t *testing.T) {
	f := newFixture(t)
	f.addCache(t, "Arc", 3*1024*1024)
	f.addCache(t, "Google", 2*1024*1024)

	d := f.scanJSON(t)
	var sum int64
	for _, o := range d.Owners {
		var inner int64
		for _, it := range o.Items {
			inner += it.Size
		}
		if o.Total != inner {
			t.Errorf("owner %q total = %d, sum of items = %d", o.ID, o.Total, inner)
		}
		sum += o.Total
	}
	if d.Summary.TotalBytes != sum {
		t.Errorf("summary.total_bytes = %d, owners sum = %d", d.Summary.TotalBytes, sum)
	}
}

func TestOwnerResolvedFromInstalledApp(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	f.addCache(t, "com.example.Slack", 12*1024*1024)

	d := f.scanJSON(t)
	if len(d.Owners) != 1 {
		t.Fatalf("owners = %d, want 1", len(d.Owners))
	}
	o := d.Owners[0]
	if o.ID != "com.example.Slack" {
		t.Errorf("owner id = %q", o.ID)
	}
	if o.Name != "Slack" {
		t.Errorf("owner name = %q, want the display name Slack", o.Name)
	}
	if o.Items[0].Recommended != true {
		t.Error("a 12 MB safe cache of an installed app should be recommended")
	}
}

func TestUnrecognizedOwnerIsNeverRecommended(t *testing.T) {
	f := newFixture(t)
	f.addCache(t, "8f3a1c9d2b7e4f60a1b2c3d4e5f60718", 50*1024*1024)

	d := f.scanJSON(t)
	if len(d.Owners) != 1 {
		t.Fatalf("owners = %d, want 1", len(d.Owners))
	}
	o := d.Owners[0]
	if o.Name != "unrecognized" {
		t.Errorf("owner name = %q, want unrecognized", o.Name)
	}
	if o.Items[0].Recommended {
		t.Error("an unrecognized owner must never be recommended")
	}
}

func TestRecommendedPolicyThroughTheCLI(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")
	f.addCache(t, "com.example.Slack", 12*1024*1024)
	f.addApp(t, "com.example.Small", "Small")
	f.addCache(t, "com.example.Small", 5*1024*1024)

	d := f.scanJSON(t)
	for _, o := range d.Owners {
		switch o.ID {
		case "com.example.Slack":
			if !o.Items[0].Recommended {
				t.Error("12 MB should be recommended")
			}
		case "com.example.Small":
			if o.Items[0].Recommended {
				t.Error("5 MB must fall under the 10 MB floor")
			}
		}
	}
}

func TestCompactOutputIsOneLine(t *testing.T) {
	f := newFixture(t)
	f.addCache(t, "Arc", 1*1024*1024)

	stdout, stderr, code := f.run(t, "scan", "--json", "--compact")
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}
	if strings.Count(strings.TrimRight(stdout, "\n"), "\n") != 0 {
		t.Errorf("compact output has more than one line:\n%s", stdout)
	}
	var d doc
	if err := json.Unmarshal([]byte(stdout), &d); err != nil {
		t.Fatalf("compact output is not JSON: %v", err)
	}
	if d.Schema != 1 {
		t.Errorf("schema = %d", d.Schema)
	}
}

func TestEmptyScanExitsThree(t *testing.T) {
	f := newFixture(t)
	_, stderr, code := f.run(t, "scan", "--json")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 for nothing to clean (stderr: %s)", code, stderr)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{
		{},                                 // bare fcm: the TUI does not exist yet
		{"nonsense"},                       // unknown subcommand
		{"scan", "--nonsense"},             // unknown flag
		{"scan", "--compact-without-json"}, // unknown flag
	} {
		_, stderr, code := f.run(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if stderr == "" {
			t.Errorf("%v: expected a usage message on stderr", args)
		}
	}
}

func TestFullRuleInventoryListsEveryPathShape(t *testing.T) {
	f := newFixture(t)
	f.addApp(t, "com.example.Slack", "Slack")

	expected := []string{
		f.addCache(t, "com.example.Slack", 1*1024*1024),
		f.addCache(t, "Homebrew", 1*1024*1024),
		f.addCache(t, "go-build", 1*1024*1024),
		f.addCache(t, "JetBrains", 1*1024*1024),
		f.addCache(t, "ms-playwright", 1*1024*1024),
		f.addCache(t, "CocoaPods", 1*1024*1024),
		f.addCache(t, "org.swift.swiftpm", 1*1024*1024),
		f.addCache(t, "pnpm", 1*1024*1024),
		f.addCache(t, "yarn", 1*1024*1024),
		f.addPath(t, ".npm/_cacache", 1*1024*1024),
		f.addPath(t, ".bun/install/cache", 1*1024*1024),
		f.addPath(t, ".yarn/berry/cache", 1*1024*1024),
		f.addPath(t, ".cache/pnpm", 1*1024*1024),
		f.addPath(t, ".gradle/caches", 1*1024*1024),
		f.addPath(t, ".m2/repository", 1*1024*1024),
		f.addPath(t, ".cache/pip", 1*1024*1024),
		f.addPath(t, ".cache/uv", 1*1024*1024),
		f.addPath(t, ".cargo/registry", 1*1024*1024),
		f.addPath(t, ".rustup/downloads", 1*1024*1024),
		f.addPath(t, "Library/Developer/Xcode/DerivedData", 1*1024*1024),
		f.addPath(t, "Library/Developer/CoreSimulator/Caches", 1*1024*1024),
		f.addPath(t, "Library/Developer/Xcode/iOS DeviceSupport", 1*1024*1024),
		f.addPath(t, "Library/Developer/Xcode/Archives", 1*1024*1024),
		f.addPath(t, "Library/Logs/SomeApp", 1*1024*1024),
		f.addPath(t, "Library/Logs/DiagnosticReports", 1*1024*1024),
		f.addPath(t, "Library/Application Support/CrashReporter", 1*1024*1024),
		f.addSystemPath(t, "Library/Caches/system.thing", 1*1024*1024),
		f.addPath(t, ".huggingface", 1*1024*1024),
		f.addPath(t, ".cache/huggingface", 1*1024*1024),
		f.addPath(t, "Library/Application Support/com.missing.Leftover", 1*1024*1024),
	}
	tempPath := f.addTempPath(t, "foo/C/blob", 1*1024*1024)
	f.agePath(t, tempPath, 8*24*time.Hour)
	expected = append(expected, tempPath)

	d := f.scanJSON(t)
	if len(expected) != 31 {
		t.Fatalf("fixture has %d paths, want 31 paths covering 30 rules", len(expected))
	}
	got := map[string]int{}
	for _, o := range d.Owners {
		for _, it := range o.Items {
			got[it.Path]++
		}
	}
	for _, path := range expected {
		switch got[path] {
		case 0:
			t.Errorf("rule path %q is missing from the report", path)
		case 1:
			// Good: exactly one Junk Item owns it.
		default:
			t.Errorf("rule path %q appears %d times", path, got[path])
		}
	}
	if d.Summary.Items != len(expected) {
		t.Errorf("summary.items = %d, want %d", d.Summary.Items, len(expected))
	}
	if len(d.Skipped) != 0 {
		t.Errorf("absent paths should be skipped silently, got %+v", d.Skipped)
	}
}

func TestOverlappingRulesDoNotDoubleCount(t *testing.T) {
	f := newFixture(t)
	genericPath := f.addCache(t, "OtherCache", 3*1024*1024)
	specificPath := f.addCache(t, "JetBrains", 4*1024*1024)

	d := f.scanJSON(t)
	items := allItems(d)
	got := map[string]int64{}
	for _, it := range items {
		got[it.Path] = it.Size
	}
	for _, path := range []string{genericPath, specificPath} {
		want := duSize(t, path)
		if got[path] != want {
			t.Errorf("item %q size = %d, du says %d", path, got[path], want)
		}
	}
	wantTotal := duSize(t, genericPath) + duSize(t, specificPath)
	if d.Summary.TotalBytes != wantTotal {
		t.Errorf("summary.total_bytes = %d, want %d (sum of the two paths)", d.Summary.TotalBytes, wantTotal)
	}
	for _, it := range items {
		if it.Path == f.caches {
			t.Errorf("the generic parent directory became its own Junk Item: %q", it.Path)
		}
	}
}

func TestUserRuleSameNameReplacesEmbeddedRule(t *testing.T) {
	f := newFixture(t)
	oldPath := f.addCache(t, "go-build", 1*1024*1024)
	newPath := f.addPath(t, "Custom/go-build", 2*1024*1024)
	f.writeConfig(t, "rules.d/override.yaml", `
rules:
  - name: caches-go-build
    pattern: ${HOME}/Custom/go-build
    owner: Custom Toolchain
    kind: log
    risk: caution
    rebuild: none
`)

	items := allItems(f.scanJSON(t))

	override := itemByPath(items, newPath)
	if override == nil {
		t.Fatalf("override path %q is missing", newPath)
	}
	if override.Kind != "log" || override.Risk != "caution" || override.Rebuild != "none" {
		t.Errorf("override item = %+v, want kind=log risk=caution rebuild=none", override)
	}
	if override.Recommended {
		t.Error("a caution item must not be recommended")
	}

	old := itemByPath(items, oldPath)
	if old == nil {
		t.Fatalf("old path %q is missing", oldPath)
	}
	if old.Kind != "cache" {
		t.Errorf("old path kind = %q, want the generic cache rule after the override", old.Kind)
	}
}

func TestIgnoreListKeepsAPathOutOfTheReport(t *testing.T) {
	f := newFixture(t)
	kept := f.addCache(t, "KeepMe", 12*1024*1024)
	visible := f.addCache(t, "Arc", 12*1024*1024)
	f.writeConfig(t, "ignore.yaml", "ignore:\n  - ${HOME}/Library/Caches/KeepMe\n")

	items := allItems(f.scanJSON(t))
	if itemByPath(items, kept) != nil {
		t.Errorf("ignored path %q appears in the report", kept)
	}
	if itemByPath(items, visible) == nil {
		t.Errorf("non-ignored path %q is missing", visible)
	}
}

func TestUninstalledOwnerBecomesAnOrphan(t *testing.T) {
	f := newFixture(t)
	path := f.addCache(t, "com.missing.Leftover", 12*1024*1024)

	d := f.scanJSON(t)
	items := allItems(d)
	item := itemByPath(items, path)
	if item == nil {
		t.Fatalf("orphan path %q is missing", path)
	}
	if item.Kind != "orphan" {
		t.Errorf("kind = %q, want orphan", item.Kind)
	}
	if item.Risk != "caution" {
		t.Errorf("risk = %q, want caution", item.Risk)
	}
	if item.Recommended {
		t.Error("an orphan must never be recommended")
	}
}

func TestSelfExclusionHoldsAgainstUserRules(t *testing.T) {
	f := newFixture(t)
	state := f.addPath(t, ".local/state/free-clean-my-macos/cleanup-202511.jsonl", 12*1024*1024)
	config := f.addPath(t, ".config/free-clean-my-macos/keep.yaml", 12*1024*1024)
	f.addCache(t, "Arc", 12*1024*1024)
	f.writeConfig(t, "rules.d/evil.yaml", `
rules:
  - name: evil-state
    pattern: ${HOME}/.local/state/free-clean-my-macos
    owner: Evil
    kind: cache
    risk: safe
    rebuild: low
  - name: evil-config
    pattern: ${HOME}/.config/free-clean-my-macos
    owner: Evil
    kind: cache
    risk: safe
    rebuild: low
`)

	items := allItems(f.scanJSON(t))
	for _, it := range items {
		if strings.HasPrefix(it.Path, state) || strings.HasPrefix(it.Path, config) {
			t.Errorf("self path %q appeared as a Junk Item", it.Path)
		}
	}
}

// --- helpers --------------------------------------------------------------

func allItems(d doc) []itemDoc {
	var out []itemDoc
	for _, o := range d.Owners {
		out = append(out, o.Items...)
	}
	return out
}

func itemByPath(items []itemDoc, path string) *itemDoc {
	for i := range items {
		if items[i].Path == path {
			return &items[i]
		}
	}
	return nil
}

func duSize(t *testing.T, path string) int64 {
	t.Helper()
	out, err := exec.Command("du", "-s", path).Output()
	if err != nil {
		t.Fatalf("du %s: %v", path, err)
	}
	fields := strings.Fields(string(out))
	blocks, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		t.Fatalf("parsing du output %q: %v", out, err)
	}
	return blocks * 512
}

func TestUnreadableDirectoryIsSkippedNotFatal(t *testing.T) {
	f := newFixture(t)
	blocked := f.addCache(t, "Blocked", 12*1024*1024)
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	d := f.scanJSON(t)
	if itemByPath(allItems(d), blocked) != nil {
		t.Errorf("unreadable path %q appeared as a Junk Item", blocked)
	}
	skip := skipFor(d.Skipped, blocked)
	if skip == nil {
		t.Fatalf("skipped[] has no entry for %q: %+v", blocked, d.Skipped)
	}
	if skip.Reason != "no-permission" {
		t.Errorf("skip reason = %q, want no-permission", skip.Reason)
	}
}

func TestRunningOwnerIsSkipped(t *testing.T) {
	live := exec.Command("sleep", "30")
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = live.Process.Kill()
		_ = live.Wait()
	})
	time.Sleep(10 * time.Millisecond)

	f := newFixture(t)
	f.unsetenv("FCM_RUNNING_OWNERS")
	f.writeConfig(t, "rules.d/running.yaml", `
rules:
  - name: running-owner
    pattern: ${HOME}/Library/Caches/RunningOwner
    owner: sleep
    kind: cache
    risk: safe
    rebuild: low
`)
	running := f.addCache(t, "RunningOwner", 12*1024*1024)
	visible := f.addCache(t, "Visible", 12*1024*1024)

	d := f.scanJSON(t)
	if itemByPath(allItems(d), running) != nil {
		t.Errorf("running owner's path %q appeared in owners[]", running)
	}
	if itemByPath(allItems(d), visible) == nil {
		t.Errorf("idle path %q is missing", visible)
	}
	skip := skipFor(d.Skipped, running)
	if skip == nil {
		t.Fatalf("skipped[] has no entry for %q: %+v", running, d.Skipped)
	}
	if skip.Reason != "running" {
		t.Errorf("skip reason = %q, want running", skip.Reason)
	}
}

func TestPathOverThePerPathBudgetIsSkipped(t *testing.T) {
	f := newFixture(t)
	f.setenv("FCM_PATH_TIMEOUT", "10ms")
	big := f.addManyFiles(t, filepath.Join(f.caches, "Big"), 20000)
	small := f.addCache(t, "Small", 12*1024*1024)

	d := f.scanJSON(t)
	if itemByPath(allItems(d), big) != nil {
		t.Errorf("timed-out path %q appeared as a Junk Item", big)
	}
	if itemByPath(allItems(d), small) == nil {
		t.Errorf("the rest of the scan did not complete: %q is missing", small)
	}
	skip := skipFor(d.Skipped, big)
	if skip == nil {
		t.Fatalf("skipped[] has no entry for %q", big)
	}
	if skip.Reason != "timeout" {
		t.Errorf("skip reason = %q, want timeout", skip.Reason)
	}
}

func TestTempScopeOnlyIncludesEntriesOlderThanSevenDays(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2030, 1, 15, 12, 0, 0, 0, time.UTC)
	f.setenv("FCM_NOW", now.Format(time.RFC3339))

	old := f.addTempPath(t, "foo/C/old", 12*1024*1024)
	fresh := f.addTempPath(t, "foo/C/fresh", 12*1024*1024)
	setModTime(t, old, now.Add(-8*24*time.Hour))
	setModTime(t, fresh, now.Add(-24*time.Hour))
	f.addCache(t, "Arc", 12*1024*1024)

	d := f.scanJSON(t)
	items := allItems(d)
	if itemByPath(items, old) == nil {
		t.Errorf("8-day-old temp entry %q is missing", old)
	}
	if itemByPath(items, fresh) != nil {
		t.Errorf("1-day-old temp entry %q must be excluded", fresh)
	}
}

func TestSymlinkCycleTerminates(t *testing.T) {
	f := newFixture(t)
	cycle := f.addCache(t, "Cycle", 12*1024*1024)
	if err := os.Symlink(cycle, filepath.Join(cycle, "self")); err != nil {
		t.Fatal(err)
	}

	d := f.scanJSON(t)
	if itemByPath(allItems(d), cycle) == nil {
		t.Errorf("cycle directory %q is missing", cycle)
	}
}

func TestPathRemovedBetweenListingAndSizingIsSkipped(t *testing.T) {
	f := newFixture(t)
	big := f.addManyFiles(t, filepath.Join(f.caches, "ABig"), 20000)
	vanished := f.addCache(t, "ZVanish", 12*1024*1024)

	cmd := exec.Command(binary, "scan", "--json", "--jobs", "1")
	cmd.Env = f.environ()
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if err := os.RemoveAll(vanished); err != nil {
		t.Fatal(err)
	}
	waitErr := cmd.Wait()
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		if code := exitErr.ExitCode(); code != 0 && code != 3 {
			t.Fatalf("scan exited %d after a path vanished (stderr: %s)", code, errOut.String())
		}
	} else if waitErr != nil {
		t.Fatalf("scan failed after a path vanished: %v", waitErr)
	}

	var d doc
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if itemByPath(allItems(d), big) == nil {
		t.Errorf("the rest of the scan did not complete: %q is missing", big)
	}
	if itemByPath(allItems(d), vanished) != nil {
		t.Errorf("vanished path %q appeared as a Junk Item", vanished)
	}
	skip := skipFor(d.Skipped, vanished)
	if skip == nil {
		t.Fatalf("skipped[] has no entry for %q: %+v", vanished, d.Skipped)
	}
	if skip.Reason != "vanished" {
		t.Errorf("skip reason = %q, want vanished", skip.Reason)
	}
}
