package rules_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

func TestExpand(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		home    string
		tmpdir  string
		want    string
	}{
		{"home variable", "${HOME}/Library/Caches/*", "/Users/fixture", "/tmp/", "/Users/fixture/Library/Caches/*"},
		{"tmpdir variable", "${TMPDIR}**/C/*", "/Users/fixture", "/var/folders/xx/T/", "/var/folders/xx/T/**/C/*"},
		{"both variables", "${HOME}/tmp/${TMPDIR}", "/Users/fixture", "/var/folders/T/", "/Users/fixture/tmp//var/folders/T/"},
		{"literal absolute path", "/Library/Caches/*", "/Users/fixture", "/tmp/", "/Library/Caches/*"},
		{"repeated variable", "${HOME}/a/${HOME}/b", "/h", "/t/", "/h/a//h/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rules.Expand(tt.pattern, tt.home, tt.tmpdir); got != tt.want {
				t.Fatalf("Expand(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestDefaultInventory(t *testing.T) {
	rs := rules.Default()
	if len(rs) != 30 {
		t.Fatalf("Default() has %d rules, want the 30-rule inventory", len(rs))
	}

	seen := map[string]bool{}
	for _, r := range rs {
		if r.Name == "" {
			t.Errorf("rule %+v has no name", r)
		}
		if seen[r.Name] {
			t.Errorf("duplicate rule name %q", r.Name)
		}
		seen[r.Name] = true
		if len(r.PathPatterns()) == 0 {
			t.Errorf("rule %q has no path pattern", r.Name)
		}
	}

	// The HuggingFace row names two paths but is one Owner and one override key.
	hf, ok := byName(rs, "huggingface-caches")
	if !ok {
		t.Fatal("huggingface-caches missing from the inventory")
	}
	if len(hf.PathPatterns()) != 2 {
		t.Fatalf("huggingface-caches has %d paths, want 2", len(hf.PathPatterns()))
	}
}

func TestDefaultPatternsExpandToFullAbsolutePaths(t *testing.T) {
	for _, r := range rules.ExpandAll(rules.Default(), "/Users/fixture", "/var/folders/xx/T/") {
		for _, p := range r.PathPatterns() {
			if !filepath.IsAbs(p) {
				t.Errorf("rule %q expanded to non-absolute path %q", r.Name, p)
			}
		}
	}
}

func TestMatchPrecedence(t *testing.T) {
	generic := rules.Rule{Name: "generic", Pattern: "/a/*", Kind: rules.KindCache}
	specific := rules.Rule{Name: "specific", Pattern: "/a/b", Kind: rules.KindDevResidue}
	nested := rules.Rule{Name: "nested", Pattern: "/a/b/c", Kind: rules.KindLog}
	duplicateFirst := rules.Rule{Name: "duplicate-first", Pattern: "/dup/*", Kind: rules.KindCache}
	duplicateSecond := rules.Rule{Name: "duplicate-second", Pattern: "/dup/*", Kind: rules.KindLog}
	multi := rules.Rule{Name: "multi", Patterns: []string{"/one/*", "/two/*"}, Kind: rules.KindDevResidue}

	tests := []struct {
		name   string
		target string
		rules  []rules.Rule
		want   string
		ok     bool
	}{
		{"generic claims a child", "/a/x", []rules.Rule{generic}, "generic", true},
		{"generic claims a child's descendant", "/a/x/y", []rules.Rule{generic}, "generic", true},
		{"specific beats generic", "/a/b", []rules.Rule{generic, specific}, "specific", true},
		{"specific beats generic on a descendant", "/a/b/x", []rules.Rule{generic, specific}, "specific", true},
		{"nested exact beats its parent", "/a/b/c/x", []rules.Rule{generic, specific, nested}, "nested", true},
		{"exact duplicate keeps the first rule", "/dup/x", []rules.Rule{duplicateFirst, duplicateSecond}, "duplicate-first", true},
		{"multi-pattern rule claims its first path", "/one/x", []rules.Rule{multi}, "multi", true},
		{"multi-pattern rule claims its second path", "/two/x", []rules.Rule{multi}, "multi", true},
		{"prefix is not a container", "/aX/b", []rules.Rule{generic}, "", false},
		{"unrelated path", "/elsewhere", []rules.Rule{generic, specific}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rules.Match(tt.target, tt.rules)
			if ok != tt.ok {
				t.Fatalf("Match(%q) ok = %v, want %v", tt.target, ok, tt.ok)
			}
			if got.Name != tt.want {
				t.Fatalf("Match(%q) = %q, want %q", tt.target, got.Name, tt.want)
			}
		})
	}
}

func TestMatchDoubleStarGlob(t *testing.T) {
	temp := rules.Rule{Name: "temp", Pattern: "/var/folders/T/**/C/*", Kind: rules.KindTemp}
	tests := []struct {
		target string
		ok     bool
	}{
		{"/var/folders/T/C/blob", true},
		{"/var/folders/T/a/C/blob", true},
		{"/var/folders/T/a/b/C/blob", true},
		{"/var/folders/T/a/b/C/blob/deeper", true}, // a child of the claimed item
		{"/var/folders/T/a/b/D/blob", false},
		{"/var/folders/T/a/b/C", false}, // the C directory itself is not the claimed child
	}
	for _, tt := range tests {
		if _, ok := rules.Match(tt.target, []rules.Rule{temp}); ok != tt.ok {
			t.Errorf("Match(%q) ok = %v, want %v", tt.target, ok, tt.ok)
		}
	}
}

func TestMergeSameNameOverrides(t *testing.T) {
	override := rules.Rule{
		Name:    "caches-go-build",
		Pattern: "${HOME}/Custom/go-build",
		Owner:   "Custom Toolchain",
		Kind:    rules.KindLog,
		Risk:    rules.RiskCaution,
		Rebuild: rules.RebuildNone,
	}
	merged := rules.Merge(rules.Default(), []rules.Rule{override})
	if len(merged) != len(rules.Default()) {
		t.Fatalf("same-name override changed the rule count to %d", len(merged))
	}
	got, ok := byName(merged, "caches-go-build")
	if !ok {
		t.Fatal("caches-go-build disappeared")
	}
	if got.Pattern != override.Pattern || got.Kind != override.Kind || got.Owner != override.Owner {
		t.Fatalf("override did not replace the embedded rule: %+v", got)
	}

	added := rules.Rule{Name: "my-rule", Pattern: "/tmp/mine", Kind: rules.KindCache, Risk: rules.RiskSafe, Rebuild: rules.RebuildLow}
	merged = rules.Merge(rules.Default(), []rules.Rule{added})
	if len(merged) != len(rules.Default())+1 {
		t.Fatalf("a new user rule was not appended: %d rules", len(merged))
	}
}

func TestLoadDirReadsYAMLFilesInOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "01-first.yaml"), `
rules:
  - name: first
    pattern: /tmp/first
    owner: First
    kind: cache
    risk: safe
    rebuild: low
`)
	write(t, filepath.Join(dir, "02-second.yml"), `
rules:
  - name: second
    pattern: /tmp/second
    owner: Second
    kind: log
    risk: caution
    rebuild: none
`)
	rs, err := rules.LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Name != "first" || rs[1].Name != "second" {
		t.Fatalf("LoadDir() = %+v", rs)
	}

	if rs, err := rules.LoadDir(filepath.Join(dir, "missing")); err != nil || rs != nil {
		t.Fatalf("missing rules directory = %+v, %v; want nil, nil", rs, err)
	}
}

func TestLoadIgnore(t *testing.T) {
	dir := t.TempDir()
	mapping := filepath.Join(dir, "mapping.yaml")
	write(t, mapping, "ignore:\n  - ${HOME}/Library/Caches/KeepMe\n")
	patterns, err := rules.LoadIgnore(mapping)
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 1 || patterns[0] != "${HOME}/Library/Caches/KeepMe" {
		t.Fatalf("LoadIgnore() = %v", patterns)
	}

	sequence := filepath.Join(dir, "sequence.yaml")
	write(t, sequence, "- /tmp/one\n- /tmp/two\n")
	patterns, err = rules.LoadIgnore(sequence)
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 2 {
		t.Fatalf("sequence ignore file = %v", patterns)
	}

	if patterns, err := rules.LoadIgnore(filepath.Join(dir, "missing.yaml")); err != nil || patterns != nil {
		t.Fatalf("missing ignore file = %v, %v; want nil, nil", patterns, err)
	}
}

func TestIgnoredMatchesPathAndDescendants(t *testing.T) {
	patterns := []string{"/Users/fixture/Library/Caches/KeepMe", "/tmp/skip*"}
	for _, target := range []string{
		"/Users/fixture/Library/Caches/KeepMe",
		"/Users/fixture/Library/Caches/KeepMe/blob",
		"/tmp/skipped/blob",
	} {
		if !rules.Ignored(target, patterns) {
			t.Errorf("Ignored(%q) = false, want true", target)
		}
	}
	if rules.Ignored("/Users/fixture/Library/Caches/Other", patterns) {
		t.Error("an unrelated path was ignored")
	}
}

func TestParseRejectsInvalidRules(t *testing.T) {
	tests := map[string]string{
		"bare directory name": `
rules:
  - name: bad
    pattern: Caches
    kind: cache
    risk: safe
    rebuild: low
`,
		"missing name": `
rules:
  - pattern: /tmp/x
    kind: cache
    risk: safe
    rebuild: low
`,
		"unknown kind": `
rules:
  - name: bad
    pattern: /tmp/x
    kind: mystery
    risk: safe
    rebuild: low
`,
		"unknown risk": `
rules:
  - name: bad
    pattern: /tmp/x
    kind: cache
    risk: maybe
    rebuild: low
`,
		"unknown rebuild": `
rules:
  - name: bad
    pattern: /tmp/x
    kind: cache
    risk: safe
    rebuild: centuries
`,
		"both pattern forms": `
rules:
  - name: bad
    pattern: /tmp/x
    patterns:
      - /tmp/y
    kind: cache
    risk: safe
    rebuild: low
`,
		"no pattern": `
rules:
  - name: bad
    kind: cache
    risk: safe
    rebuild: low
`,
	}
	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := rules.Parse([]byte(doc)); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func byName(rs []rules.Rule, name string) (rules.Rule, bool) {
	for _, r := range rs {
		if r.Name == name {
			return r, true
		}
	}
	return rules.Rule{}, false
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
