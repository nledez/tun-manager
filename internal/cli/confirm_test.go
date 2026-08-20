package cli

import (
	"errors"
	"strings"
	"testing"
)

// answered runs the real prompt against a canned answer and hands back what was
// decided and what the person would have seen.
func answered(t *testing.T, typed string) (bool, string) {
	t.Helper()

	var shown strings.Builder
	yes, err := Ask(strings.NewReader(typed), &shown)("import alpha?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	return yes, shown.String()
}

func TestAskTakesYesForAnAnswer(t *testing.T) {
	for _, typed := range []string{"y\n", "Y\n", "yes\n", "  yes  \n", "YES\n"} {
		t.Run(strings.TrimSpace(typed), func(t *testing.T) {
			yes, _ := answered(t, typed)

			if !yes {
				t.Errorf("%q was not taken as yes", typed)
			}
		})
	}
}

func TestAskTakesAnythingElseAsNo(t *testing.T) {
	// Including an empty line. The default has to be the one that changes
	// nothing: somebody pressing return to get their prompt back must not have
	// imported a configuration by doing it.
	for _, typed := range []string{"\n", "n\n", "no\n", "nope\n", "yeah\n", "ok\n"} {
		t.Run(strings.TrimSpace(typed), func(t *testing.T) {
			yes, _ := answered(t, typed)

			if yes {
				t.Errorf("%q was taken as yes", typed)
			}
		})
	}
}

func TestAskShowsTheQuestionAndTheDefault(t *testing.T) {
	_, shown := answered(t, "\n")

	for _, want := range []string{"import alpha?", "[y/N]"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the prompt %q does not contain %q", shown, want)
		}
	}
}

func TestAskWithNobodyToAskSaysHowToProceedWithoutOne(t *testing.T) {
	// A script, a pipe, a cron entry. Reading end-of-file as "no" would leave
	// somebody staring at an import that silently did nothing.
	_, err := Ask(strings.NewReader(""), &strings.Builder{})("import alpha?")

	if err == nil {
		t.Fatal("Ask answered with nothing to read")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error %q does not say how to import without being asked", err)
	}
}

func TestAskReportsAnInputItCannotRead(t *testing.T) {
	boom := errors.New("input/output error")

	_, err := Ask(failingReader{boom}, &strings.Builder{})("import alpha?")

	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the read failure itself", err)
	}
}

func TestAssumedAnswersWithoutReadingAnything(t *testing.T) {
	// --yes must not touch stdin: whatever is on it belongs to whoever comes
	// next in the pipeline.
	yes, err := Assumed(true)("import alpha?")

	if err != nil || !yes {
		t.Errorf("Assumed(true) = %v, %v", yes, err)
	}
}

// failingReader stands in for a stdin that has gone away mid-question.
type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }
