// This file is the one white-box test in the package, and it exists for one
// reason: the key translation table is where a typo silently breaks the whole
// screen, and it cannot be observed through the pure model. No terminal, no
// goroutine, no rendering is involved.
package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

func TestKeyOfMapsTerminalKeysOntoTheModelVocabulary(t *testing.T) {
	tests := []struct {
		name string
		in   tea.KeyMsg
		want Key
	}{
		{"space", tea.KeyMsg{Type: tea.KeySpace}, KeySpace},
		{"space as a rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}, KeySpace},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, KeyEnter},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, KeyUp},
		{"down", tea.KeyMsg{Type: tea.KeyDown}, KeyDown},
		{"home", tea.KeyMsg{Type: tea.KeyHome}, KeyHome},
		{"end", tea.KeyMsg{Type: tea.KeyEnd}, KeyEnd},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, KeyEsc},
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, KeyBackspace},
		{"ctrl+c quits", tea.KeyMsg{Type: tea.KeyCtrlC}, KeyQuit},
		{"q", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}, KeyQuit},
		{"a", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, KeyToggleOwner},
		{"A", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}}, KeyToggleAll},
		{"/", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}, KeyFilter},
		{"d", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}, KeyDetail},
		{"unmapped", tea.KeyMsg{Type: tea.KeyF1}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyOf(tt.in); got != tt.want {
				t.Fatalf("keyOf(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestScreenDrivesTheModelFromKeys(t *testing.T) {
	s := newScreen(engine.Options{Home: "/home"})
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	s.model.FinishScan(engine.Report{Owners: []engine.Owner{{
		ID: "com.example.App", Name: "App", Total: 20 * 1024 * 1024,
		Items: []engine.Item{{
			Path: "/home/Library/Caches/com.example.App", Size: 20 * 1024 * 1024,
			Kind: rules.KindCache, Risk: rules.RiskSafe, Rebuild: rules.RebuildLow, Recommended: true,
		}},
	}}, Summary: engine.Summary{Items: 1, TotalBytes: 20 * 1024 * 1024}}, nil)

	if _, ok := s.model.Detail(); ok {
		t.Fatal("the detail panel should start closed")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if _, ok := s.model.Detail(); !ok {
		t.Fatal("d should open the detail panel")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := s.model.Detail(); ok {
		t.Fatal("esc should close the detail panel")
	}

	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q should return a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("q returned %T, want tea.QuitMsg", cmd())
	}
}
