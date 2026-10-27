package cli

import (
	"bytes"
	"strings"
	"testing"
)

// ask runs ciguse as if in a terminal, answering setup questions with lines of input.
func ask(t *testing.T, answers string, args ...string) (out string, code int) {
	t.Helper()
	canAsk = func() bool { return true }
	stdin = strings.NewReader(answers)
	t.Cleanup(func() { canAsk, stdin = func() bool { return false }, nil })
	var o, e bytes.Buffer
	code = Run(args, "test", &o, &e)
	return o.String() + e.String(), code
}

func contains(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in:\n%s", w, got)
		}
	}
}

func TestInitAsksInATerminal(t *testing.T) {
	setup(t)
	out, code := ask(t, "2\n6,50\nusd\n10\n05:00\n", "init")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	contains(t, out, "▀▒▒░   ciguse", "What do you use?  1 cigarette  2 iqos  3 glo  4 stick  5 pouch\n  › [cigarette] ",
		"default     iqos\n", "price       iqos 6.50 USD / 10\n", "day starts  05:00\n", "next\n  log a use       ciguse use\n")

	// Settings stick: a later use is logged as IQOS.
	expect(t, []string{"use"}, 0, "ciguse · iqos logged 12:41 · 1 today\n")

	// Asking again offers the stored answers, and Enter keeps them.
	out, _ = ask(t, "\n\n\n\n\n", "init")
	contains(t, out, "› [iqos] ", "› [6.50] ", "› [USD] ", "› [10] ", "› [05:00] ", "price       iqos 6.50 USD / 10\n")
}

func TestInitSkipsPackQuestionsWithoutAPrice(t *testing.T) {
	setup(t)
	out, code := ask(t, "glo\n\n\n", "init")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "Currency?") || strings.Contains(out, "How many") {
		t.Fatalf("asked about the pack after the price was skipped:\n%s", out)
	}
	contains(t, out, "default     glo\n", "price       not set", "day starts  04:00\n")
}

func TestInitAsksAboutACanForPouches(t *testing.T) {
	setup(t)
	out, code := ask(t, "pouch\n5.50\neur\n\n\n", "init")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	contains(t, out, "What does a can cost?", "How many in a can?", "price       pouch 5.50 EUR / 20\n")
	expect(t, []string{"use"}, 0, "ciguse · pouch logged 12:41 · 1 today\n")
}

func TestInitReasksAfterABadAnswer(t *testing.T) {
	setup(t)
	out, code := ask(t, "7\n1\nabout five\n5\nEURO\nEUR\n0\n20\n25:00\n03:30\n", "init")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	contains(t, out, "pick 1 to 5, or type its name", `"about five" is not a price like 5.00`,
		"a 3-letter code, such as EUR", "a number from 1 to 1000", `"25:00" is not a time like 04:00`,
		"price       cigarette 5.00 EUR / 20\n", "day starts  03:30\n")
}

func TestInitCancelledInputSavesNothing(t *testing.T) {
	setup(t)
	out, code := ask(t, "2\n6.50\n", "init") // input ends before the currency question
	if code != 1 || !strings.Contains(out, "setup cancelled, nothing saved") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	expect(t, []string{"use"}, 0, "ciguse · cigarette logged 12:41 · 1 today\n")
}

func TestInitWithFlagsNeverAsks(t *testing.T) {
	setup(t)
	out, _ := ask(t, "", "init", "--day-start", "03:00")
	if strings.Contains(out, "What do you use?") {
		t.Fatalf("asked with flags given:\n%s", out)
	}
	contains(t, out, "day starts  03:00\n")
}
