package format_test

import (
	"testing"

	"github.com/thewindear/free-clean-my-macos/internal/format"
)

func TestBytesReadsLikeAPersonWroteIt(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{10 * 1024 * 1024, "10.0 MB"},
		{338 * 1024 * 1024, "338.0 MB"},
		{1536 * 1024 * 1024, "1.5 GB"},
		{3 * 1024 * 1024 * 1024 * 1024, "3.0 TB"},
	}
	for _, tt := range tests {
		if got := format.Bytes(tt.bytes); got != tt.want {
			t.Errorf("Bytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestCount(t *testing.T) {
	tests := []struct {
		n    int
		word string
		want string
	}{
		{0, "item", "0 items"},
		{1, "item", "1 item"},
		{2, "item", "2 items"},
		{1, "smaller item", "1 smaller item"},
		{3, "smaller item", "3 smaller items"},
	}
	for _, tt := range tests {
		if got := format.Count(tt.n, tt.word); got != tt.want {
			t.Errorf("Count(%d, %q) = %q, want %q", tt.n, tt.word, got, tt.want)
		}
	}
}

func TestHomeAbbreviatesOnlyPathsInsideIt(t *testing.T) {
	tests := []struct {
		home, path, want string
	}{
		{"/Users/x", "/Users/x/Library/Caches/Arc", "~/Library/Caches/Arc"},
		{"/Users/x", "/Users/x", "/Users/x"},                                 // home itself is not a path under home
		{"/Users/x", "/Users/xy/Library/Caches", "/Users/xy/Library/Caches"}, // not a prefix sibling
		{"/Users/x", "/Library/Caches", "/Library/Caches"},
		{"", "/Users/x/Library/Caches", "/Users/x/Library/Caches"}, // no HOME to abbreviate
	}
	for _, tt := range tests {
		if got := format.Home(tt.home, tt.path); got != tt.want {
			t.Errorf("Home(%q, %q) = %q, want %q", tt.home, tt.path, got, tt.want)
		}
	}
}
