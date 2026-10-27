package cli

import (
	"errors"
	"strings"
	"testing"
)

// notified replaces notify for the test and returns the texts it was given.
func notified(t *testing.T, err error) *[]string {
	t.Helper()
	var got []string
	old := notify
	notify = func(text string) error { got = append(got, text); return err }
	t.Cleanup(func() { notify = old })
	return &got
}

// Acceptance test 6: two hotkey presses within 90 s make one event, and each press
// gets a notification.
func TestUseNotify(t *testing.T) {
	dir, _ := setup(t)
	got := notified(t, nil)
	expect(t, []string{"use", "--notify"}, 0, "ciguse · cigarette logged 12:41 · 1 today\n")
	expect(t, []string{"use", "--notify"}, 0, "ciguse · already logged one at 12:41, skipped (--force logs anyway)\n")
	expect(t, []string{"use", "--notify", "nope"}, 2, "ciguse: unknown product \"nope\" (products: cigarette, iqos, glo, stick, pouch)\n")

	want := []string{"cigarette logged 12:41 · 1 today", "already logged one at 12:41, skipped (--force logs anyway)",
		`unknown product "nope" (products: cigarette, iqos, glo, stick, pouch)`}
	if strings.Join(*got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("notifications\n got %q\nwant %q", *got, want)
	}
	if ev := queryDB(t, dir, `SELECT source FROM events`); len(ev) != 1 || ev[0] != "hotkey" {
		t.Fatalf("events %q", ev)
	}
}

func TestUseNotifyFailureStillLogs(t *testing.T) {
	dir, _ := setup(t)
	notified(t, errors.New("no notification service"))
	expect(t, []string{"use", "--notify"}, 0,
		"ciguse · cigarette logged 12:41 · 1 today\nciguse: couldn't show a notification: no notification service\n")
	if ev := queryDB(t, dir, `SELECT count(*) FROM events`); ev[0] != "1" {
		t.Fatalf("%s events, want 1", ev[0])
	}
}

func TestHotkeyGuide(t *testing.T) {
	for goos, bin := range map[string]string{"darwin": "/opt/homebrew/bin/ciguse", "windows": `C:\Users\u\AppData\Local\Programs\ciguse\ciguse.exe`, "linux": "/home/u/.local/bin/ciguse"} {
		g := hotkeyGuide(goos, bin)
		if !strings.Contains(g, bin) || !strings.Contains(g, "use --notify") {
			t.Errorf("%s guide lacks the command:\n%s", goos, g)
		}
	}
}
