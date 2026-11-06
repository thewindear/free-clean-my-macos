// Package owner resolves which software entity owns a path: the Owner in the
// tool's domain language. Resolution has three steps, in order: an explicit
// owner on the Scan Rule, a reverse-DNS bundle id looked up against installed
// app bundles, then the directory name verbatim marked unrecognized.
package owner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Owner is the software entity a Junk Item belongs to.
type Owner struct {
	ID         string
	Name       string
	Recognized bool
	// Orphan marks an Owner that is a bundle id with no installed app: the
	// app was uninstalled and left its data behind.
	Orphan bool
}

// Resolver answers ownership questions, caching the installed-app index for
// the life of one scan.
type Resolver struct {
	appsDirs []string
	index    map[string]string
	built    bool
}

// NewResolver builds a resolver that looks for app bundles in appsDirs.
func NewResolver(appsDirs []string) *Resolver {
	return &Resolver{appsDirs: appsDirs}
}

// Resolve names the Owner of dirName according to the three-step fallback.
func (r *Resolver) Resolve(ruleOwner, dirName string) Owner {
	if ruleOwner != "" {
		name := ruleOwner
		if display, ok := r.apps()[ruleOwner]; ok {
			name = display
		}
		return Owner{ID: ruleOwner, Name: name, Recognized: true}
	}

	if LooksLikeBundleID(dirName) {
		if display, ok := r.apps()[dirName]; ok {
			return Owner{ID: dirName, Name: display, Recognized: true}
		}
		return Owner{ID: dirName, Name: dirName, Orphan: true}
	}

	return Owner{ID: dirName, Name: "unrecognized"}
}

// LooksLikeBundleID reports whether a directory name is a reverse-DNS
// identifier such as com.example.App.
func LooksLikeBundleID(name string) bool {
	parts := strings.Split(name, ".")
	if len(parts) < 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// apps lazily builds bundle id -> display name from installed app bundles.
func (r *Resolver) apps() map[string]string {
	if r.built {
		return r.index
	}
	r.built = true
	r.index = map[string]string{}
	for _, dir := range r.appsDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.HasSuffix(e.Name(), ".app") {
				continue
			}
			id, name := readAppPlist(filepath.Join(dir, e.Name()))
			if id == "" {
				continue
			}
			if name == "" {
				name = strings.TrimSuffix(e.Name(), ".app")
			}
			r.index[id] = name
		}
	}
	return r.index
}

// readAppPlist reads the bundle identifier and display name of an app bundle.
// plutil keeps us free of cgo and of a hand-rolled binary plist parser.
func readAppPlist(appPath string) (id, name string) {
	plist := filepath.Join(appPath, "Contents", "Info.plist")
	out, err := exec.Command("plutil", "-convert", "json", "-o", "-", plist).Output()
	if err != nil {
		return "", ""
	}
	var fields map[string]any
	if err := json.Unmarshal(out, &fields); err != nil {
		return "", ""
	}
	id, _ = fields["CFBundleIdentifier"].(string)
	name, _ = fields["CFBundleDisplayName"].(string)
	if name == "" {
		name, _ = fields["CFBundleName"].(string)
	}
	return id, name
}
