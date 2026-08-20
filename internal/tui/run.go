package tui

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/notify"
)

// Option tweaks how the program is started.
type Option func(*[]tea.ProgramOption)

// WithoutTerminal runs the program detached from a terminal. Tests use it to
// drive the event loop without a TTY.
func WithoutTerminal() Option {
	return func(opts *[]tea.ProgramOption) {
		*opts = append(*opts, tea.WithInput(nil), tea.WithOutput(io.Discard))
	}
}

// newModel builds the model Run drives, with the feed wired in when there is
// one.
//
// It is separate from Run so the wiring can be asserted directly. Reaching it
// through a running program means racing the event loop, which is how a test
// ends up proving only that nothing panicked.
func newModel(a *app.App, n *notify.Notifier, f *feed.Server, problems []string) Model {
	m := New(a, n)
	// Whatever went wrong before the screen existed. Printed on the way in, it
	// would be swallowed by the alternate screen a millisecond later, which is
	// how "the status feed is unavailable" became something nobody ever read.
	for _, problem := range problems {
		m.log(problem, true)
	}
	m.showLogs = len(problems) > 0
	if f == nil {
		// Assigning a nil *feed.Server to the interface would leave a non-nil
		// interface holding a nil pointer, and every publish would panic.
		return m
	}
	m.feed = f
	m.requests = f.Requests()
	return m
}

// Run starts the interactive interface and blocks until the user quits or the
// context is cancelled. A nil feed means nothing is published.
//
// problems are the things that went wrong before there was a screen to say them
// on. They open the log pane and are shown in red, because the alternative -
// writing them to the terminal a moment before the alternate screen covers it -
// is the same as not saying them at all.
func Run(ctx context.Context, a *app.App, n *notify.Notifier, f *feed.Server, problems []string, opts ...Option) error {
	programOpts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	for _, o := range opts {
		o(&programOpts)
	}

	m := newModel(a, n, f, problems)

	_, err := tea.NewProgram(m, programOpts...).Run()
	if ctx.Err() != nil {
		// An interrupt is how the program is asked to stop, so whatever the
		// event loop reported on its way out is not a failure to report.
		return nil //nolint:nilerr // deliberate: cancellation is not an error
	}
	return err
}
