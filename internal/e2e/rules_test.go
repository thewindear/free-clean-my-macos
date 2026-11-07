package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFileIn writes one file into dir, creating the directory if needed.
func writeFileIn(dir, name, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

func runRules(t *testing.T, f *fixture, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return f.run(t, append([]string{"rules"}, args...)...)
}

func TestRulesListShowsTheEmbeddedInventory(t *testing.T) {
	f := newFixture(t)
	stdout, stderr, code := runRules(t, f, "list")
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}
	for _, want := range []string{
		"Effective Scan Rules",
		"caches-generic",
		"caches-jetbrains",
		"embedded",
		"${HOME}/Library/Caches/*",
		"~/.config/free-clean-my-macos/rules.d",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("rules list is missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "* ") {
		t.Errorf("no user rules exist, so no row should be marked as an override:\n%s", stdout)
	}
}

func TestRulesListMarksAnOverride(t *testing.T) {
	f := newFixture(t)
	f.writeConfig(t, "rules.d/mine.yaml", `
rules:
  - name: caches-go-build
    pattern: ${HOME}/Library/Caches/go-build
    kind: dev-residue
    risk: caution
    rebuild: high
    note: my own take
`)
	stdout, stderr, code := runRules(t, f, "list")
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "* caches-go-build") {
		t.Errorf("the overridden rule should be marked:\n%s", stdout)
	}
	if !strings.Contains(stdout, "override") {
		t.Errorf("the source column should say override:\n%s", stdout)
	}
	if !strings.Contains(stdout, "replaces the embedded rule") {
		t.Errorf("the output should say what the override replaced:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 override") {
		t.Errorf("the header should count the override:\n%s", stdout)
	}
}

func TestRulesReloadPicksUpANewRule(t *testing.T) {
	f := newFixture(t)

	first, stderr, code := runRules(t, f, "reload")
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}
	if strings.Contains(first, "my-own-cache") {
		t.Fatalf("the rule is not on disk yet:\n%s", first)
	}

	// The user adds a rule while the tool is "running": the next reload has to
	// see it without any restart or cache flush.
	f.writeConfig(t, "rules.d/mine.yaml", `
rules:
  - name: my-own-cache
    pattern: ${HOME}/Library/Caches/my-own-cache
    kind: cache
    risk: safe
    rebuild: low
`)
	second, stderr, code := runRules(t, f, "reload")
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}
	for _, want := range []string{"mine.yaml", "my-own-cache", "1 user", "New rules"} {
		if !strings.Contains(second, want) {
			t.Errorf("reload is missing %q:\n%s", want, second)
		}
	}
}

func TestRulesReloadReportsWhatItReplaced(t *testing.T) {
	f := newFixture(t)
	f.writeConfig(t, "rules.d/mine.yaml", `
rules:
  - name: caches-go-build
    pattern: ${HOME}/Library/Caches/go-build
    kind: dev-residue
    risk: safe
    rebuild: low
`)
	stdout, stderr, code := runRules(t, f, "reload")
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "caches-go-build") || !strings.Contains(stdout, "Overrides") {
		t.Errorf("reload should name the rule it replaced:\n%s", stdout)
	}
}

func TestRulesReloadFailsOnABrokenFile(t *testing.T) {
	f := newFixture(t)
	f.writeConfig(t, "rules.d/broken.yaml", "rules:\n  - name: \"\"\n    pattern: ${HOME}/x\n")

	_, stderr, code := runRules(t, f, "reload")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 for an invalid rule file", code)
	}
	if !strings.Contains(stderr, "broken.yaml") {
		t.Errorf("stderr should name the file, got %q", stderr)
	}
}

func TestRulesHonoursRulesDir(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	if err := writeFileIn(dir, "elsewhere.yaml", `
rules:
  - name: elsewhere
    pattern: ${HOME}/Library/Caches/elsewhere
    kind: cache
    risk: safe
    rebuild: low
`); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runRules(t, f, "list", "--rules-dir", dir)
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "elsewhere") {
		t.Errorf("--rules-dir was ignored:\n%s", stdout)
	}
	if !strings.Contains(stdout, dir) {
		t.Errorf("the output should name the rules directory it read:\n%s", stdout)
	}
}

func TestRulesUsageErrorsExitTwo(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{
		{"rules"},
		{"rules", "bogus"},
		{"rules", "--rules-dir", "/tmp"},
		{"rules", "list", "--nonsense"},
		{"rules", "list", "extra"},
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

func TestVersionIsReported(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{{"version"}, {"--version"}} {
		stdout, stderr, code := f.run(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d (stderr: %s)", args, code, stderr)
		}
		if !strings.HasPrefix(stdout, "fcm ") {
			t.Errorf("%v: version output = %q, want it to start with fcm", args, stdout)
		}
	}
}
