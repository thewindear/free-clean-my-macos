package ui

import (
	"context"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/thewindear/free-clean-my-macos/internal/engine"
)

// progressInterval is the Scan Progress repaint budget: Bubble Tea renders on
// every message, so the scan is not allowed to interrupt it more than ten times
// a second.
const progressInterval = 100 * time.Millisecond

// tickInterval is how often the elapsed time on the stats bar advances.
const tickInterval = 100 * time.Millisecond

// Supported reports whether the list screen can be opened: without a terminal
// there is nobody to read keys from, and fcm must say so instead of hanging.
func Supported() bool {
	return isatty.IsTerminal(os.Stdin.Fd())
}

// Run opens the list screen and returns when the user quits. The scan runs in
// the background, so the screen is responsive — and cancellable — from the
// first frame.
func Run(stdout io.Writer, opts engine.Options) error {
	p := tea.NewProgram(newScreen(opts), tea.WithOutput(stdout), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// progressMsg carries one engine.Progress event.
type progressMsg engine.Progress

// scanDoneMsg is the terminal message of a scan: the Report, and the reason it
// stopped early if it did.
type scanDoneMsg struct {
	report engine.Report
	err    error
}

// tickMsg advances the live elapsed time.
type tickMsg time.Time

// screen is the Bubble Tea shell: it translates terminal input into Keys and
// engine events into model calls, and owns the scan's context.
type screen struct {
	model  *Model
	opts   engine.Options
	msgs   chan tea.Msg
	quit   chan struct{}
	cancel context.CancelFunc
	closed bool
}

func newScreen(opts engine.Options) *screen {
	return &screen{
		model: New(opts.Home, time.Now()),
		opts:  opts,
		msgs:  make(chan tea.Msg, 64),
		quit:  make(chan struct{}),
	}
}

func (s *screen) Init() tea.Cmd {
	s.startScan()
	return tea.Batch(s.readMsg, tick())
}

// startScan runs engine.Scan in the background and forwards its progress. Only
// one goroutine writes to msgs, and it closes the channel when the scan is done,
// so the reader always terminates.
func (s *screen) startScan() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	opts := s.opts
	var lastEmit time.Time
	opts.Emit = func(p engine.Progress) {
		if !lastEmit.IsZero() && time.Since(lastEmit) < progressInterval {
			return
		}
		lastEmit = time.Now()
		select {
		case s.msgs <- progressMsg(p):
		default:
			// The reader is behind. Progress is cumulative, so dropping an
			// intermediate event loses nothing.
		}
	}

	go func() {
		report, err := engine.Scan(ctx, opts)
		select {
		case s.msgs <- scanDoneMsg{report: report, err: err}:
		case <-s.quit:
		}
		close(s.msgs)
	}()
}

// readMsg waits for the next engine message.
func (s *screen) readMsg() tea.Msg {
	msg, ok := <-s.msgs
	if !ok {
		return nil
	}
	return msg
}

// tick asks for the next elapsed-time update.
func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (s *screen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.model.SetSize(msg.Width, msg.Height)
		return s, nil

	case tea.KeyMsg:
		switch action := s.model.HandleKey(keyOf(msg)); action {
		case ActionQuit:
			s.shutdown()
			return s, tea.Quit
		case ActionCancelScan:
			if s.cancel != nil {
				s.cancel()
			}
		}
		return s, nil

	case progressMsg:
		s.model.ApplyProgress(engine.Progress(msg))
		return s, s.readMsg

	case scanDoneMsg:
		s.cancel = nil
		s.model.FinishScan(msg.report, msg.err)
		return s, nil

	case tickMsg:
		s.model.Tick(time.Time(msg))
		if s.model.Stats().Phase == PhaseScanning {
			return s, tick()
		}
		return s, nil
	}
	return s, nil
}

func (s *screen) View() string { return s.model.View() }

// shutdown unblocks the scan goroutine so it can finish and close its channel.
func (s *screen) shutdown() {
	if s.closed {
		return
	}
	s.closed = true
	close(s.quit)
	if s.cancel != nil {
		s.cancel()
	}
}

// keyOf normalizes one Bubble Tea key press into the model's vocabulary.
func keyOf(msg tea.KeyMsg) Key {
	switch msg.Type {
	case tea.KeySpace:
		return KeySpace
	case tea.KeyEnter:
		return KeyEnter
	case tea.KeyUp:
		return KeyUp
	case tea.KeyDown:
		return KeyDown
	case tea.KeyHome:
		return KeyHome
	case tea.KeyEnd:
		return KeyEnd
	case tea.KeyEsc:
		return KeyEsc
	case tea.KeyBackspace:
		return KeyBackspace
	case tea.KeyCtrlC:
		return KeyQuit
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			if msg.Runes[0] == ' ' {
				return KeySpace
			}
			return Key(string(msg.Runes[0]))
		}
	}
	return ""
}
