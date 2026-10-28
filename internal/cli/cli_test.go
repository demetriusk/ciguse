package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setup points ciguse at a temp data dir and a fixed clock it returns for advancing.
func setup(t *testing.T) (dir string, clock *time.Time) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("CIGUSE_HOME", dir)
	c := time.Date(2026, 10, 27, 12, 41, 0, 0, time.Local)
	now = func() time.Time { return c }
	t.Cleanup(func() { now = time.Now })
	return dir, &c
}

func expect(t *testing.T, args []string, code int, want string) {
	t.Helper()
	var out, errb bytes.Buffer
	got := Run(args, "test", &out, &errb)
	if got != code || out.String()+errb.String() != want {
		t.Fatalf("ciguse %s\nexit %d, want %d\n--- got\n%s--- want\n%s", strings.Join(args, " "), got, code, out.String()+errb.String(), want)
	}
}

func TestUseUndoFlow(t *testing.T) {
	_, clock := setup(t)

	expect(t, []string{"use", "iqos"}, 0, "ciguse · iqos logged 12:41 · 1 today\n")
	*clock = clock.Add(30 * time.Second)
	expect(t, []string{"use"}, 0, "ciguse · already logged one at 12:41, skipped (--force logs anyway)\n")
	expect(t, []string{"use", "--ago", "20m"}, 0, "ciguse · cigarette logged 12:21 · 2 today\n")
	expect(t, []string{"use", "--force"}, 0, "ciguse · cigarette logged 12:41 · 3 today\n")
	expect(t, []string{"undo"}, 0, "ciguse · removed cigarette 12:41 · 2 today\n")
	expect(t, []string{"undo"}, 0, "ciguse · removed cigarette 12:21 · 1 today\n")
	expect(t, []string{"undo"}, 0, "ciguse · removed iqos 12:41 · 0 today\n")
	expect(t, []string{"undo"}, 0, "ciguse · nothing logged in the past 24h to undo\n")
}

func TestUseRejectsBadInput(t *testing.T) {
	setup(t)
	expect(t, []string{"use", "vape"}, 2, "ciguse: unknown product \"vape\" (products: cigarette, iqos, glo, stick, pouch)\n")
	expect(t, []string{"use", "--ago", "13h"}, 2, "ciguse: --ago must be between 0 and 12h\n")
	expect(t, []string{"use", "iqos", "glo"}, 2, "ciguse use: unexpected argument \"glo\"\n")
	expect(t, []string{"nope"}, 2, "ciguse: unknown command \"nope\"\n\n"+usage)
}

func TestInitAndReport(t *testing.T) {
	dir, clock := setup(t)

	expect(t, []string{"report"}, 0, "ciguse · nothing logged yet. Log a use with: ciguse use\n")
	expect(t, []string{"init", "--product", "iqos", "--pack-price", "6,00", "--currency", "eur"}, 0,
		"ciguse · ready\n"+
			"  default     iqos\n"+
			"  price       iqos 6.00 EUR / 20\n"+
			"  day starts  04:00\n"+
			"  data        "+tildePath(filepath.Join(dir, "ciguse.db"))+"\n")
	expect(t, []string{"use"}, 0, "ciguse · iqos logged 12:41 · 1 today\n")
	expect(t, []string{"use", "cigarette", "--ago", "3h"}, 0, "ciguse · cigarette logged 09:41 · 2 today\n")
	expect(t, []string{"report", "--days", "3"}, 0,
		"ciguse · last 1 day · 2 uses · 2.0 a day\n\n"+
			"no Claude Code or Codex sessions in this range, so no working vs waiting split.\n"+
			"the plugins record them: ciguse setup claude, ciguse setup codex\n\n"+
			"Tue 27 Oct    2  ▆▆  today\n\n"+
			"value  0.30 EUR  (1 of 2 uses priced)\n")

	// Days before the first use weren't tracked, so they're left out; a day without uses after it shows as 0.
	*clock = clock.AddDate(0, 0, -2)
	expect(t, []string{"use"}, 0, "ciguse · iqos logged 12:41 · 1 today\n")
	*clock = clock.AddDate(0, 0, 2)
	expect(t, []string{"report", "--days", "4"}, 0,
		"ciguse · last 3 days · 3 uses · 1.0 a day\n\n"+
			"no Claude Code or Codex sessions in this range, so no working vs waiting split.\n"+
			"the plugins record them: ciguse setup claude, ciguse setup codex\n\n"+
			"Sun 25 Oct    1  ▆\n"+
			"Mon 26 Oct    0\n"+
			"Tue 27 Oct    2  ▆▆  today\n\n"+
			"value  0.60 EUR  (2 of 3 uses priced)\n")
}

func TestInitValidation(t *testing.T) {
	setup(t)
	expect(t, []string{"init", "--pack-price", "5.00"}, 2, "ciguse: --pack-price needs --currency, a 3-letter code such as EUR\n")
	expect(t, []string{"init", "--pack-price", "five", "--currency", "EUR"}, 2, "ciguse: --pack-price: \"five\" is not a price like 5.00\n")
	expect(t, []string{"init", "--day-start", "25:00"}, 2, "ciguse: --day-start: \"25:00\" is not a time like 04:00\n")
}

func TestParseMoney(t *testing.T) {
	for in, want := range map[string]int64{"5": 500, "5.5": 550, "5,50": 550, "12.05": 1205, " 0.99 ": 99} {
		got, err := parseMoney(in)
		if err != nil || got != want {
			t.Errorf("parseMoney(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "-5", "5.555", "5.", "1e3", "€5"} {
		if _, err := parseMoney(in); err == nil {
			t.Errorf("parseMoney(%q) should fail", in)
		}
	}
}

func TestTildePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	if got, want := tildePath(filepath.Join(home, "Library", "ciguse.db")), filepath.Join("~", "Library", "ciguse.db"); got != want {
		t.Fatalf("under home: %q, want %q", got, want)
	}
	other := filepath.Join(filepath.Dir(home), "elsewhere", "ciguse.db")
	if got := tildePath(other); got != other {
		t.Fatalf("outside home: %q", got)
	}
}
