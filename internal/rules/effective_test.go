package rules_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

func rule(name, pattern string) rules.Rule {
	return rules.Rule{
		Name:    name,
		Pattern: pattern,
		Kind:    rules.KindCache,
		Risk:    rules.RiskSafe,
		Rebuild: rules.RebuildLow,
	}
}

// TestEffectiveRulesRecordsProvenance is what `fcm rules list` marks: which
// rules are embedded, which the user added, and which one replaced an embedded
// rule.
func TestEffectiveRulesRecordsProvenance(t *testing.T) {
	base := []rules.Rule{rule("a", "${HOME}/a"), rule("b", "${HOME}/b")}
	overrides := []rules.Rule{rule("b", "${HOME}/b-user"), rule("c", "${HOME}/c")}

	got := rules.EffectiveRules(base, overrides)
	if len(got) != 3 {
		t.Fatalf("effective rules = %d, want 3: %+v", len(got), got)
	}

	if got[0].Source != rules.SourceEmbedded || got[0].Rule.Name != "a" {
		t.Errorf("rule 0 = %+v, want the embedded a", got[0])
	}
	if got[0].Superseded != nil {
		t.Errorf("an embedded rule must not report a superseded one")
	}

	if got[1].Source != rules.SourceOverride {
		t.Errorf("rule 1 source = %q, want override", got[1].Source)
	}
	if got[1].Rule.Pattern != "${HOME}/b-user" {
		t.Errorf("rule 1 = %+v, want the user's definition", got[1].Rule)
	}
	if got[1].Superseded == nil || got[1].Superseded.Pattern != "${HOME}/b" {
		t.Errorf("rule 1 superseded = %+v, want the embedded b", got[1].Superseded)
	}

	if got[2].Source != rules.SourceUser || got[2].Rule.Name != "c" {
		t.Errorf("rule 2 = %+v, want the user's new rule c", got[2])
	}
	if got[2].Superseded != nil {
		t.Errorf("a new user rule must not report a superseded one")
	}
}

// TestMergeIsEffectiveRulesWithoutProvenance keeps the two views from drifting:
// the engine scans with Merge's output, the command line explains it with
// EffectiveRules.
func TestMergeIsEffectiveRulesWithoutProvenance(t *testing.T) {
	base := []rules.Rule{rule("a", "${HOME}/a"), rule("b", "${HOME}/b")}
	overrides := []rules.Rule{rule("b", "${HOME}/b-user"), rule("c", "${HOME}/c")}

	merged := rules.Merge(base, overrides)
	effective := rules.EffectiveRules(base, overrides)
	if len(merged) != len(effective) {
		t.Fatalf("Merge returned %d rules, EffectiveRules %d", len(merged), len(effective))
	}
	for i := range merged {
		if merged[i].Name != effective[i].Rule.Name || merged[i].Pattern != effective[i].Rule.Pattern {
			t.Errorf("rule %d: Merge says %+v, EffectiveRules says %+v", i, merged[i], effective[i].Rule)
		}
	}
}

// TestMergeDoesNotAliasTheEmbeddedSet keeps a user override from mutating the
// embedded inventory in place.
func TestMergeDoesNotAliasTheEmbeddedSet(t *testing.T) {
	base := []rules.Rule{{Name: "multi", Patterns: []string{"${HOME}/one", "${HOME}/two"}}}
	merged := rules.Merge(base, nil)
	merged[0].Patterns[0] = "/clobbered"
	if base[0].Patterns[0] != "${HOME}/one" {
		t.Fatal("Merge handed out the embedded slice; an override could rewrite the inventory")
	}
}

func writeRuleFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadFilesReportsEachFile is what `fcm rules reload` prints: which files it
// re-read and how much each contributed.
func TestLoadFilesReportsEachFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rules.d")
	writeRuleFile(t, dir, "b-extra.yml", `
rules:
  - name: extra-one
    pattern: ${HOME}/one
    kind: cache
    risk: safe
    rebuild: low
  - name: extra-two
    pattern: ${HOME}/two
    kind: log
    risk: safe
    rebuild: none
`)
	writeRuleFile(t, dir, "a-first.yaml", `
rules:
  - name: first
    pattern: ${HOME}/first
    kind: cache
    risk: safe
    rebuild: low
`)
	writeRuleFile(t, dir, "notes.txt", "not a rule file")

	files, err := rules.LoadFiles(dir)
	if err != nil {
		t.Fatalf("LoadFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want the 2 yaml files: %+v", len(files), files)
	}
	if filepath.Base(files[0].Path) != "a-first.yaml" || len(files[0].Rules) != 1 {
		t.Errorf("first file = %+v (%d rules)", files[0], len(files[0].Rules))
	}
	if filepath.Base(files[1].Path) != "b-extra.yml" || len(files[1].Rules) != 2 {
		t.Errorf("second file = %+v (%d rules)", files[1], len(files[1].Rules))
	}
}

func TestLoadFilesAcceptsAMissingDirectory(t *testing.T) {
	files, err := rules.LoadFiles(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("a missing rules directory is not an error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("files = %+v, want none", files)
	}
}

func TestLoadFilesNamesTheBrokenFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rules.d")
	writeRuleFile(t, dir, "broken.yaml", "rules:\n  - name: \"\"\n    pattern: ${HOME}/x\n")
	if _, err := rules.LoadFiles(dir); err == nil {
		t.Fatal("an invalid rule file must be an error")
	} else if !contains(err.Error(), "broken.yaml") {
		t.Errorf("error %q should name the file", err)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
