package rules_test

import (
	"testing"

	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

func TestExpandSubstitutesHomeAndTmpdir(t *testing.T) {
	got := rules.Expand("${HOME}/Library/Caches/*", "/Users/fixture", "/var/folders/xx/T")
	want := "/Users/fixture/Library/Caches/*"
	if got != want {
		t.Fatalf("Expand() = %q, want %q", got, want)
	}

	got = rules.Expand("${TMPDIR}C/*", "/Users/fixture", "/var/folders/xx/T")
	want = "/var/folders/xx/TC/*"
	if got != want {
		t.Fatalf("Expand() = %q, want %q", got, want)
	}
}

func TestDefaultRulesCoverTheCachesScope(t *testing.T) {
	rs := rules.Default()
	if len(rs) == 0 {
		t.Fatal("Default() returned no rules")
	}
	rule, ok := rules.Match("/Users/fixture/Library/Caches/Arc/blob.bin", rules.ExpandAll(rs, "/Users/fixture", "/tmp"))
	if !ok {
		t.Fatal("expected the caches scope to claim a cache path")
	}
	if rule.Kind != rules.KindCache {
		t.Fatalf("kind = %q, want %q", rule.Kind, rules.KindCache)
	}
	if rule.Risk != rules.RiskSafe {
		t.Fatalf("risk = %q, want %q", rule.Risk, rules.RiskSafe)
	}
}

func TestMatchRejectsPathsOutsideEveryRule(t *testing.T) {
	if _, ok := rules.Match("/Users/fixture/Documents/notes.md", rules.ExpandAll(rules.Default(), "/Users/fixture", "/tmp")); ok {
		t.Fatal("a user document must never match a rule")
	}
}

func TestMatchPrefersTheLongestPattern(t *testing.T) {
	specific := rules.Rule{
		Pattern: "${HOME}/Library/Caches/JetBrains",
		Kind:    rules.KindDevResidue,
		Risk:    rules.RiskSafe,
		Rebuild: rules.RebuildHigh,
		Note:    "reindexing takes hours",
	}
	rs := append([]rules.Rule{specific}, rules.Default()...)

	rule, ok := rules.Match("/Users/fixture/Library/Caches/JetBrains/idea/index", rules.ExpandAll(rs, "/Users/fixture", "/tmp"))
	if !ok {
		t.Fatal("expected a match")
	}
	if rule.Rebuild != rules.RebuildHigh {
		t.Fatalf("rebuild = %q, want the specific rule's %q", rule.Rebuild, rules.RebuildHigh)
	}
}

func TestMatchDoesNotTreatAPrefixAsAContainer(t *testing.T) {
	if _, ok := rules.Match("/Users/fixture/Library/CachesX/file", rules.ExpandAll(rules.Default(), "/Users/fixture", "/tmp")); ok {
		t.Fatal("CachesX must not match the Caches rule")
	}
}

func TestChildrenOfAWildcardPattern(t *testing.T) {
	base, ok := rules.WildcardBase("${HOME}/Library/Caches/*")
	if !ok {
		t.Fatal("expected a wildcard pattern")
	}
	if base != "${HOME}/Library/Caches" {
		t.Fatalf("base = %q", base)
	}
	if _, ok := rules.WildcardBase("${HOME}/Library/Caches/JetBrains"); ok {
		t.Fatal("an exact rule is not a wildcard")
	}
}
