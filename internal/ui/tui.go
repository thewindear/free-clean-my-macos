package ui

import (
	"context"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/thewindear/free-clean-my-macos/internal/clean"
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
// first frame; a Cleanup runs in the background too, so the per-item progress
// is visible while it happens.
func Run(stdout io.Writer, scan engine.Options, cleanup clean.Options) error {
	p := tea.NewProgram(newScreen(scan, cleanup), tea.WithOutput(stdout), tea.WithAltScreen())
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

// cleanEventMsg carries one Cleanup event: an item started or finished.
type cleanEventMsg clean.Event

// cleanDoneMsg is the terminal message of a Cleanup: the Result, and the reason
// it could not start if it could not.
type cleanDoneMsg struct {
	result clean.Result
	err    error
}

// tickMsg advances the live elapsed time.
type tickMsg time.Time

// screen is the Bubble Tea shell: it translates terminal input into Keys and
// engine events into model calls, and owns the scan's context and the
// goroutines that do the work.
type screen struct {
	model   *Model
	scan    engine.Options
	cleanup clean.Options

	// msgs is the one channel every worker writes to: the scan's progress, the
	// Cleanup's events, and both terminal results. It is never closed, because
	// the scan and the Cleanup are separate lifetimes.
	msgs   chan tea.Msg
	quit   chan struct{}
	cancel context.CancelFunc
	closed bool
}

func newScreen(scan engine.Options, cleanup clean.Options) *screen {
	return &screen{
		model:   New(scan.Home, time.Now()),
		scan:    scan,
		cleanup: cleanup,
		msgs:    make(chan tea.Msg, 64),
		quit:    make(chan struct{}),
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

	opts := s.scan
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
	}()
}

// startClean runs the Cleanup of what the model prepared. It is only called
// when the model asked for it, so the selection has already passed the gate.
func (s *screen) startClean() {
	selected := s.model.PendingCandidates()
	if len(selected) == 0 {
		return
	}
	opts := s.cleanup
	opts.Emit = func(ev clean.Event) {
		// Blocking on purpose: a dropped finished event would make a row lie
		// about what happened. The buffer and the reader keep up in practice.
		select {
		case s.msgs <- cleanEventMsg(ev):
		case <-s.quit:
		}
	}
	go func() {
		result, err := clean.Run(context.Background(), selected, opts)
		select {
		case s.msgs <- cleanDoneMsg{result: result, err: err}:
		case <-s.quit:
		}
	}()
}

// readMsg waits for the next worker message. It gives up when the program
// quits, so no reader goroutine outlives the screen.
func (s *screen) readMsg() tea.Msg {
	select {
	case msg := <-s.msgs:
		return msg
	case <-s.quit:
		return nil
	}
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
		switch s.model.HandleKey(keyOf(msg)) {
		case ActionQuit:
			s.shutdown()
			return s, tea.Quit
		case ActionCancelScan:
			if s.cancel != nil {
				s.cancel()
			}
		case ActionClean:
			s.startClean()
		}

	case progressMsg:
		s.model.ApplyProgress(engine.Progress(msg))

	case scanDoneMsg:
		s.cancel = nil
		s.model.FinishScan(msg.report, msg.err)

	case cleanEventMsg:
		s.model.ApplyCleanEvent(clean.Event(msg))

	case cleanDoneMsg:
		s.model.FinishClean(msg.result, msg.err)

	case tickMsg:
		s.model.Tick(time.Time(msg))
		if s.model.Stats().Phase == PhaseScanning {
			return s, tea.Batch(s.readMsg, tick())
		}
	}
	return s, s.readMsg
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
