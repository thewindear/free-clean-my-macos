package owner_test

import (
	"testing"

	"github.com/thewindear/free-clean-my-macos/internal/owner"
)

func TestIsRunning(t *testing.T) {
	processes := []string{
		"/Applications/Slack.app/Contents/MacOS/Slack",
		"/Applications/Xcode.app/Contents/MacOS/Xcode Helper",
		"/usr/sbin/cfprefsd",
		"/bin/sleep",
	}

	tests := []struct {
		name string
		own  owner.Owner
		want bool
	}{
		{"display name matches the executable", owner.Owner{ID: "com.tinyspeck.slackmacgap", Name: "Slack"}, true},
		{"app bundle path matches a helper process", owner.Owner{ID: "com.apple.dt.Xcode", Name: "Xcode"}, true},
		{"explicit owner name matches", owner.Owner{ID: "sleep", Name: "sleep"}, true},
		{"case insensitive", owner.Owner{ID: "SLACK", Name: "slack"}, true},
		{"bundle id alone is not a process name", owner.Owner{ID: "com.tinyspeck.slackmacgap"}, false},
		{"unrelated owner", owner.Owner{ID: "com.example.Gone", Name: "Gone"}, false},
		{"empty owner", owner.Owner{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := owner.IsRunning(tt.own, processes); got != tt.want {
				t.Fatalf("IsRunning(%+v) = %v, want %v", tt.own, got, tt.want)
			}
		})
	}
}
