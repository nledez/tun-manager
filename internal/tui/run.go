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

// Run starts the interactive interface and blocks until the user quits or the
// context is cancelled. A nil feed means nothing is published.
func Run(ctx context.Context, a *app.App, n *notify.Notifier, f *feed.Server, opts ...Option) error {
	programOpts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	for _, o := range opts {
		o(&programOpts)
	}

	m := New(a, n)
	if f != nil {
		// Assigned through the concrete type rather than passed as an
		// interface: a nil *feed.Server in an interface is not a nil
		// interface, and every publish would panic.
		m.feed = f
		m.requests = f.Requests()
	}

	_, err := tea.NewProgram(m, programOpts...).Run()
	if ctx.Err() != nil {
		// An interrupt is how the program is asked to stop, so whatever the
		// event loop reported on its way out is not a failure to report.
		return nil //nolint:nilerr // deliberate: cancellation is not an error
	}
	return err
}
