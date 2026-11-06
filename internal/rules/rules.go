// Package rules implements Scan Rules: the only thing that can turn a path
// into a Junk Item. A rule names a path pattern, who owns it, what kind of
// data it is, and how expensive it is to get back.
package rules

import "strings"

// Risk is the Risk Level of a rule: whether deleting it costs the user
// anything beyond disk space.
type Risk string

const (
	RiskSafe    Risk = "safe"
	RiskCaution Risk = "caution"
)

// Rebuild is the Rebuild Cost of a rule: what it takes to get the data back.
type Rebuild string

const (
	RebuildNone Rebuild = "none"
	RebuildLow  Rebuild = "low"
	RebuildHigh Rebuild = "high"
)

// Kind is where a Junk Item came from.
type Kind string

const (
	KindCache      Kind = "cache"
	KindLog        Kind = "log"
	KindDevResidue Kind = "dev-residue"
	KindTemp       Kind = "temp"
	KindOrphan     Kind = "orphan"
)

// validKinds is the closed set of Kind values a rule or a selection may use.
var validKinds = map[Kind]bool{
	KindCache:      true,
	KindLog:        true,
	KindDevResidue: true,
	KindTemp:       true,
	KindOrphan:     true,
}

// ValidKind reports whether k is a Kind the domain defines, so the CLI can
// reject a typo instead of silently selecting nothing.
func ValidKind(k Kind) bool { return validKinds[k] }

// Rule is one declarative Scan Rule. Pattern is written as a full path with
// ${HOME} / ${TMPDIR} variables; a trailing /* claims every child directory.
type Rule struct {
	Pattern string  `yaml:"pattern"`
	Owner   string  `yaml:"owner"`
	Kind    Kind    `yaml:"kind"`
	Risk    Risk    `yaml:"risk"`
	Rebuild Rebuild `yaml:"rebuild"`
	Note    string  `yaml:"note"`
}

// Default is the built-in rule set. This tracer bullet ships the application
// caches scope only; the full inventory arrives with its own ticket.
//
// The rebuild cost matters for the Cleanup, not for the scan: a rule that
// declares it high is always moved into the Trash rather than deleted.
func Default() []Rule {
	return []Rule{
		{
			Pattern: "${HOME}/Library/Caches/*",
			Kind:    KindCache,
			Risk:    RiskSafe,
			Rebuild: RebuildLow,
			Note:    "application caches; the app rebuilds them on next use",
		},
	}
}

// Expand resolves the ${HOME} and ${TMPDIR} variables in a pattern.
func Expand(pattern, home, tmpdir string) string {
	expanded := strings.ReplaceAll(pattern, "${HOME}", home)
	return strings.ReplaceAll(expanded, "${TMPDIR}", tmpdir)
}

// ExpandAll resolves every rule's pattern against the given environment.
func ExpandAll(rs []Rule, home, tmpdir string) []Rule {
	out := make([]Rule, len(rs))
	for i, r := range rs {
		r.Pattern = Expand(r.Pattern, home, tmpdir)
		out[i] = r
	}
	return out
}

// WildcardBase reports the directory a trailing /* pattern claims children of.
func WildcardBase(pattern string) (string, bool) {
	if !strings.HasSuffix(pattern, "/*") {
		return "", false
	}
	return strings.TrimSuffix(pattern, "/*"), true
}

// Match returns the rule claiming path. When several match, the longest
// pattern wins (Rule Precedence), so a specific rule always overrides the
// generic one and a path can never belong to two Junk Items.
func Match(path string, rs []Rule) (Rule, bool) {
	var best Rule
	bestLen := -1
	for _, r := range rs {
		covered, ok := covers(r.Pattern, path)
		if !ok || covered <= bestLen {
			continue
		}
		best, bestLen = r, covered
	}
	if bestLen < 0 {
		return Rule{}, false
	}
	return best, true
}

// covers reports the pattern length that claims path, or false.
func covers(pattern, path string) (int, bool) {
	base, wildcard := WildcardBase(pattern)
	if !wildcard {
		base = pattern
	}
	if path == base && !wildcard {
		return len(base), true
	}
	if strings.HasPrefix(path, base+"/") {
		return len(base), true
	}
	return 0, false
}
