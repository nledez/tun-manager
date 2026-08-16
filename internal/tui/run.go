package tui

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"

	"ledez.net/tun-manager/internal/app"
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
// context is cancelled.
func Run(ctx context.Context, a *app.App, n *notify.Notifier, opts ...Option) error {
	programOpts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	for _, o := range opts {
		o(&programOpts)
	}

	_, err := tea.NewProgram(New(a, n), programOpts...).Run()
	if ctx.Err() != nil {
		// An interrupt is how the program is asked to stop, so whatever the
		// event loop reported on its way out is not a failure to report.
		return nil //nolint:nilerr // deliberate: cancellation is not an error
	}
	return err
}
