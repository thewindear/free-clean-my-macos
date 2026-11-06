package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	f := &fixture{
		home:   home,
		caches: filepath.Join(home, "Library", "Caches"),
		apps:   filepath.Join(home, "Applications"),
	}
	for _, d := range []string{f.caches, f.apps} {
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

func (f *fixture) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = []string{
		"HOME=" + f.home,
		"TMPDIR=" + filepath.Join(f.home, "tmp") + "/",
		"FCM_APPS_DIRS=" + f.apps,
		"PATH=" + os.Getenv("PATH"),
	}
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
