// Package format renders the numbers and paths fcm shows a human. There is one
// implementation so the list, the text output and the Cleanup plan can never
// disagree about how big something is.
package format

import (
	"fmt"
	"strings"
)

// Bytes is a byte count the way a person reads it: "0 B", "1023 B", "1.0 KB",
// "11.4 MB". Binary units, one decimal above a kilobyte.
func Bytes(b int64) string {
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

// Count is a counted noun with the right plural: "1 item", "3 items".
func Count(n int, word string) string {
	return fmt.Sprintf("%d %s", n, Noun(n, word))
}

// Noun is the bare noun for labels that put the count first, such as "3 other
// items".
func Noun(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// Home is the Display Path: a path under home is shown with ~ so more of it
// fits, and the absolute path stays one `d` away.
func Home(home, path string) string {
	if home != "" && strings.HasPrefix(path, home+"/") {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
