// Package rules implements Scan Rules: the only thing that can turn a path
// into a Junk Item. A rule names a path pattern, who owns it, what kind of
// data it is, and how expensive it is to get back.
package rules

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

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

// Rule is one declarative Scan Rule. Paths are written as full paths with
// ${HOME} / ${TMPDIR} variables; a trailing /* claims every child directory,
// and ** matches any number of path segments.
//
// Name is the override key: a user rule with the same Name replaces the
// embedded rule instead of adding a second one.
type Rule struct {
	Name        string   `yaml:"name"`
	Pattern     string   `yaml:"pattern,omitempty"`
	Patterns    []string `yaml:"patterns,omitempty"`
	Owner       string   `yaml:"owner"`
	Kind        Kind     `yaml:"kind"`
	Risk        Risk     `yaml:"risk"`
	Rebuild     Rebuild  `yaml:"rebuild"`
	Note        string   `yaml:"note"`
	OrphansOnly bool     `yaml:"orphans_only,omitempty"`
}

// PathPatterns is the rule's path list. The single-pattern form is kept for
// the common case; the plural form is for one Owner with two caches (for
// example HuggingFace's two model directories).
func (r Rule) PathPatterns() []string {
	if len(r.Patterns) > 0 {
		return r.Patterns
	}
	if r.Pattern != "" {
		return []string{r.Pattern}
	}
	return nil
}

//go:embed defaults.yaml
var defaultsYAML []byte

var (
	defaultOnce  sync.Once
	defaultRules []Rule
	defaultErr   error
)

// Default is the embedded rule inventory.
func Default() []Rule {
	defaultOnce.Do(func() {
		defaultRules, defaultErr = Parse(defaultsYAML)
	})
	if defaultErr != nil {
		panic(fmt.Sprintf("rules: embedded defaults are invalid: %v", defaultErr))
	}
	return cloneRules(defaultRules)
}

type rulesFile struct {
	Rules []Rule `yaml:"rules"`
}

// Parse reads one YAML rule file and validates every rule in it.
func Parse(data []byte) ([]Rule, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f rulesFile
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	for i := range f.Rules {
		if err := validate(f.Rules[i]); err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
	}
	return f.Rules, nil
}

// LoadDir reads every *.yaml / *.yml file in dir in filename order. A missing
// directory is not an error: it means the user has no overrides.
func LoadDir(dir string) ([]Rule, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".yaml" || ext == ".yml" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	var out []Rule
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		rs, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, rs...)
	}
	return out, nil
}

// Merge layers overrides on top of base. A rule whose Name already exists
// replaces it in place; a new Name is appended. That is the same-name override
// policy for ~/.config/free-clean-my-macos/rules.d.
func Merge(base, overrides []Rule) []Rule {
	out := cloneRules(base)
	index := make(map[string]int, len(out))
	for i, r := range out {
		index[r.Name] = i
	}
	for _, r := range overrides {
		if i, ok := index[r.Name]; ok {
			out[i] = r
			continue
		}
		index[r.Name] = len(out)
		out = append(out, r)
	}
	return out
}

type ignoreFile struct {
	Ignore []string `yaml:"ignore"`
}

// LoadIgnore reads a User Ignore List. A missing file means nothing is
// ignored.
func LoadIgnore(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseIgnore(data)
}

// ParseIgnore accepts either a plain YAML sequence of path patterns or a
// document with a top-level `ignore:` key.
func ParseIgnore(data []byte) ([]string, error) {
	var f ignoreFile
	if err := yaml.Unmarshal(data, &f); err == nil && f.Ignore != nil {
		for _, p := range f.Ignore {
			if err := validatePathPattern(p); err != nil {
				return nil, err
			}
		}
		return f.Ignore, nil
	}

	var list []string
	if err := yaml.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("ignore file must be a list of path patterns: %w", err)
	}
	for _, p := range list {
		if err := validatePathPattern(p); err != nil {
			return nil, err
		}
	}
	return list, nil
}

// Expand resolves the ${HOME} and ${TMPDIR} variables in a pattern.
func Expand(pattern, home, tmpdir string) string {
	return strings.NewReplacer("${HOME}", home, "${TMPDIR}", tmpdir).Replace(pattern)
}

// ExpandAll resolves every rule's patterns against the given environment.
func ExpandAll(rs []Rule, home, tmpdir string) []Rule {
	out := make([]Rule, len(rs))
	for i, r := range rs {
		if len(r.Patterns) > 0 {
			patterns := make([]string, len(r.Patterns))
			for j, p := range r.Patterns {
				patterns[j] = Expand(p, home, tmpdir)
			}
			r.Patterns = patterns
		} else {
			r.Pattern = Expand(r.Pattern, home, tmpdir)
		}
		out[i] = r
	}
	return out
}

// ExpandPatterns resolves variables in a list of patterns, such as the User
// Ignore List.
func ExpandPatterns(patterns []string, home, tmpdir string) []string {
	out := make([]string, len(patterns))
	for i, p := range patterns {
		out[i] = Expand(p, home, tmpdir)
	}
	return out
}

// WildcardBase reports the literal directory before the first wildcard. It is
// the root the engine walks to enumerate candidates.
func WildcardBase(pattern string) (string, bool) {
	idx := strings.Index(pattern, "*")
	if idx < 0 {
		return "", false
	}
	base := pattern[:idx]
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[:i]
	} else {
		base = ""
	}
	if base == "" {
		base = "/"
	}
	return base, true
}

// Match returns the rule claiming target. When several match, the longest
// literal prefix wins (Rule Precedence), so a specific rule always overrides
// the generic one and a path can never belong to two Junk Items. On an exact
// tie the earlier rule in the list wins, which keeps duplicate patterns
// deterministic.
func Match(target string, rs []Rule) (Rule, bool) {
	var best Rule
	bestPrefix, bestLen := -1, -1
	for _, r := range rs {
		for _, pattern := range r.PathPatterns() {
			prefix, length := specificity(pattern)
			if prefix < bestPrefix || prefix == bestPrefix && length <= bestLen {
				continue
			}
			if patternClaims(pattern, target) {
				best, bestPrefix, bestLen = r, prefix, length
			}
		}
	}
	if bestPrefix < 0 {
		return Rule{}, false
	}
	return best, true
}

// specificity ranks patterns for Rule Precedence: how much of the path is
// literal. A pattern with a wildcard is ranked by its literal prefix; an exact
// pattern by its whole length. The full pattern length only breaks ties.
func specificity(pattern string) (prefix, length int) {
	if base, wildcard := WildcardBase(pattern); wildcard {
		return len(base), len(pattern)
	}
	return len(pattern), len(pattern)
}

// Ignored reports whether target matches any User Ignore List pattern.
func Ignored(target string, patterns []string) bool {
	for _, pattern := range patterns {
		if patternClaims(pattern, target) {
			return true
		}
	}
	return false
}

// patternClaims reports whether pattern claims target itself or one of its
// ancestors. A rule for a directory claims the whole tree beneath it, which is
// what makes a specific nested rule override the generic one.
func patternClaims(pattern, target string) bool {
	if matchPattern(pattern, target) {
		return true
	}
	for target != "/" && target != "." {
		target = path.Dir(target)
		if matchPattern(pattern, target) {
			return true
		}
	}
	return false
}

// Matches reports whether pattern's glob matches target itself, without the
// directory-claims-its-tree rule used by Match. The engine uses it to enumerate
// the exact targets of a ** pattern.
func Matches(pattern, target string) bool {
	return matchPattern(pattern, target)
}

// matchPattern is a glob match for absolute paths. * stays inside one path
// segment; ** matches zero or more segments.
func matchPattern(pattern, target string) bool {
	return matchSegments(splitPath(pattern), splitPath(target))
}

func splitPath(p string) []string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return []string{""}
	}
	return strings.Split(p, "/")
}

func matchSegments(pattern, target []string) bool {
	if len(pattern) == 0 {
		return len(target) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(target); i++ {
			if matchSegments(pattern[1:], target[i:]) {
				return true
			}
		}
		return false
	}
	if len(target) == 0 {
		return false
	}
	ok, err := path.Match(pattern[0], target[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pattern[1:], target[1:])
}

func validate(r Rule) error {
	if r.Name == "" {
		return errors.New("name is required")
	}
	if (r.Pattern == "") == (len(r.Patterns) == 0) {
		return errors.New("exactly one of pattern or patterns is required")
	}
	if r.Pattern != "" {
		if err := validatePathPattern(r.Pattern); err != nil {
			return fmt.Errorf("pattern: %w", err)
		}
	}
	for _, p := range r.Patterns {
		if err := validatePathPattern(p); err != nil {
			return fmt.Errorf("patterns: %w", err)
		}
	}
	switch r.Kind {
	case KindCache, KindLog, KindDevResidue, KindTemp, KindOrphan:
	default:
		return fmt.Errorf("unknown kind %q", r.Kind)
	}
	switch r.Risk {
	case RiskSafe, RiskCaution:
	default:
		return fmt.Errorf("unknown risk %q", r.Risk)
	}
	switch r.Rebuild {
	case RebuildNone, RebuildLow, RebuildHigh:
	default:
		return fmt.Errorf("unknown rebuild %q", r.Rebuild)
	}
	return nil
}

func validatePathPattern(p string) error {
	if p == "" {
		return errors.New("path pattern is empty")
	}
	if !strings.HasPrefix(p, "/") &&
		!strings.HasPrefix(p, "${HOME}") &&
		!strings.HasPrefix(p, "${TMPDIR}") {
		return fmt.Errorf("path pattern %q must be a full path", p)
	}
	return nil
}

func cloneRules(rs []Rule) []Rule {
	out := make([]Rule, len(rs))
	for i, r := range rs {
		if r.Patterns != nil {
			r.Patterns = append([]string(nil), r.Patterns...)
		}
		out[i] = r
	}
	return out
}
