package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Confirm asks whether to go on with something.
//
// A function rather than a flag threaded through every signature, because the
// two answers come from different places: a person at a terminal, or --yes on
// the command line. Whoever builds one has decided which.
type Confirm func(question string) (bool, error)

// Ask reads the answer from in, having asked on w.
//
// Anything but yes is no, and an empty line is no: the default has to be the
// one that changes nothing, because somebody pressing return to get their
// prompt back must not have imported a configuration by doing it.
func Ask(in io.Reader, w io.Writer) Confirm {
	return func(question string) (bool, error) {
		if _, err := fmt.Fprintf(w, "%s [y/N] ", question); err != nil {
			// A question nobody saw must not be answered on their behalf.
			return false, err
		}

		answer, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, fmt.Errorf("read the answer: %w", err)
		}
		if errors.Is(err, io.EOF) && strings.TrimSpace(answer) == "" {
			// Nobody is there: a script, a pipe, a cron entry. Reading that as
			// "no" would leave somebody staring at an import that silently did
			// nothing.
			return false, errors.New(
				"there is nobody to answer: run it from a terminal, or pass --yes to import " +
					"without being asked")
		}

		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			return true, nil
		default:
			return false, nil
		}
	}
}

// Assumed answers without asking and without reading anything, which is what
// --yes means. Whatever is on standard input belongs to whoever comes next in
// the pipeline.
func Assumed(answer bool) Confirm {
	return func(string) (bool, error) { return answer, nil }
}
