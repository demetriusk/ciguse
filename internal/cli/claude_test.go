package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/demetriusk/ciguse/internal/store"
)

func TestSetKey(t *testing.T) {
	v := []byte(`{"command": "x"}`)
	cases := []struct{ in, want string }{
		{"", "{\n  \"statusLine\": {\"command\": \"x\"}\n}\n"},
		{"{}", "{\n  \"statusLine\": {\"command\": \"x\"}\n}"},
		{"{\n  \"model\": \"opus\"\n}\n", "{\n  \"model\": \"opus\",\n  \"statusLine\": {\"command\": \"x\"}\n}\n"},
		{"{\"statusLine\": {\"command\": \"old\"}, \"a\": [1, 2]}", "{\"statusLine\": {\"command\": \"x\"}, \"a\": [1, 2]}"},
	}
	for _, c := range cases {
		got, err := setKey([]byte(c.in), "statusLine", v)
		if err != nil || string(got) != c.want {
			t.Errorf("set in %q:\n got %q, %v\nwant %q", c.in, got, err, c.want)
		}
	}
	deletes := []struct{ in, want string }{
		{"{\n  \"a\": 1,\n  \"statusLine\": {}\n}\n", "{\n  \"a\": 1\n}\n"},
		{"{\n  \"statusLine\": {},\n  \"a\": 1\n}\n", "{\n  \"a\": 1\n}\n"},
		{"{\"a\": 1}", "{\"a\": 1}"},
	}
	for _, c := range deletes {
		got, err := setKey([]byte(c.in), "statusLine", nil)
		if err != nil || string(got) != c.want {
			t.Errorf("delete in %q:\n got %q, %v\nwant %q", c.in, got, err, c.want)
		}
	}
	if _, err := setKey([]byte("[1]"), "statusLine", v); err == nil {
		t.Error("an array was accepted as settings")
	}
}

// claudeHome points Claude Code's config at a temp dir with settings.json holding content,
// or no file when content is empty, and fakes the claude command.
func claudeHome(t *testing.T, content string) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", d)
	if content != "" {
		if err := os.WriteFile(filepath.Join(d, "settings.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := claudeCLI
	claudeCLI = func(args ...string) ([]byte, error) { return []byte("ciguse@ciguse\n"), nil }
	t.Cleanup(func() { claudeCLI = old })
	return filepath.Join(d, "settings.json")
}

// Acceptance test 8: a custom status line is wrapped, only statusLine changes, and
// --remove restores the original byte for byte.
func TestSetupClaudeWrapsAndRestores(t *testing.T) {
	dir, _ := setup(t)
	const orig = "{\n    \"model\": \"opus\",\n    \"statusLine\": {\"type\": \"command\", \"command\": \"echo mine\", \"refreshInterval\": 5},\n    \"theme\": \"dark\"\n}\n"
	path := claudeHome(t, orig)

	var out, errb bytes.Buffer
	if code := Run([]string{"setup", "claude"}, "test", &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	got, _ := os.ReadFile(path)
	want := "{\n    \"model\": \"opus\",\n    \"statusLine\": {\n    \"command\": \"ciguse statusline --wrap\",\n    \"refreshInterval\": 5,\n    \"type\": \"command\"\n  },\n    \"theme\": \"dark\"\n}\n"
	if string(got) != want {
		t.Fatalf("settings:\n%s\nwant:\n%s", got, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "claude-settings.backup.json")); string(b) != orig {
		t.Fatalf("backup %q", b)
	}
	contains(t, out.String(), "was     echo mine", "now     ciguse statusline --wrap")

	// The wrapped line runs first, with the segment after it.
	t.Setenv("NO_COLOR", "1")
	stdin = strings.NewReader(`{"session_id":"s"}`)
	t.Cleanup(func() { stdin = os.Stdin })
	expect(t, []string{"statusline", "--wrap"}, 0, "mine  ciguse · 0 today\n")

	expect(t, []string{"setup", "claude"}, 0, "ciguse · Claude Code plugin installed\nciguse · status line already shows ciguse\n")
	out.Reset()
	if code := Run([]string{"setup", "claude", "--remove"}, "test", &out, &errb); code != 0 {
		t.Fatalf("remove exit %d: %s", code, errb.String())
	}
	if got, _ := os.ReadFile(path); string(got) != orig {
		t.Fatalf("not restored byte for byte:\n%s", got)
	}
	expect(t, []string{"setup", "claude", "--remove"}, 0, "ciguse · ciguse isn't in the status line, nothing to undo\n")
}

// A wrapped status line without a timer gets one, so logs from other sessions show up.
// 0.6.0 left it out; running setup again adds it and --remove still restores the original.
func TestSetupClaudeAddsARefreshTimer(t *testing.T) {
	setup(t)
	const orig = "{\n  \"statusLine\": {\"type\": \"command\", \"command\": \"echo mine\"}\n}\n"
	path := claudeHome(t, orig)
	run(t, "setup", "claude")
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"refreshInterval": 15`) {
		t.Fatalf("no timer added:\n%s", got)
	}

	// What 0.6.0 wrote: the wrap without a timer, with its hash stored.
	old := strings.Replace(string(got), ",\n    \"refreshInterval\": 15", "", 1)
	os.WriteFile(path, []byte(old), 0o600)
	sum := sha256.Sum256([]byte(old))
	s, _ := store.Open(filepath.Join(os.Getenv("CIGUSE_HOME"), "ciguse.db"), time.Second)
	s.SetConfig(cfgWritten, hex.EncodeToString(sum[:]))
	s.Close()

	out, _ := run(t, "setup", "claude")
	contains(t, out, "now refreshes every 15 seconds")
	want := strings.Replace(old, `--wrap",`, `--wrap",`+"\n    \"refreshInterval\": 15,", 1)
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Fatalf("timer not added in place:\n%s", got)
	}
	run(t, "setup", "claude", "--remove")
	if got, _ := os.ReadFile(path); string(got) != orig {
		t.Fatalf("not restored byte for byte:\n%s", got)
	}
}

func TestSetupClaudeWithoutSettingsFile(t *testing.T) {
	setup(t)
	path := claudeHome(t, "")
	claudeCLI = func(args ...string) ([]byte, error) { return nil, errors.New("not found") }

	var out, errb bytes.Buffer
	Run([]string{"setup", "claude"}, "test", &out, &errb)
	contains(t, out.String(), "install the Claude Code plugin from a session:\n  /plugin install ciguse --marketplace demetriusk/ciguse")
	got, _ := os.ReadFile(path)
	if string(got) != "{\n  \"statusLine\": {\n    \"command\": \"ciguse statusline\",\n    \"refreshInterval\": 15,\n    \"type\": \"command\"\n  }\n}\n" {
		t.Fatalf("settings:\n%s", got)
	}

	// Edited since setup: only statusLine is taken out, the other edit stays.
	edited := strings.Replace(string(got), "{\n", "{\n  \"model\": \"opus\",\n", 1)
	os.WriteFile(path, []byte(edited), 0o600)
	Run([]string{"setup", "claude", "--remove"}, "test", &out, &errb)
	if got, _ := os.ReadFile(path); string(got) != "{\n  \"model\": \"opus\"\n}\n" {
		t.Fatalf("after remove:\n%q", got)
	}
}

func TestStatuslineSegment(t *testing.T) {
	_, clock := setup(t)
	// Claude Code shows colors though the output is a pipe.
	expect(t, []string{"statusline"}, 0, "\x1b[90mciguse ·\x1b[m \x1b[1m0 today\x1b[m\n")
	t.Setenv("NO_COLOR", "1")
	expect(t, []string{"statusline"}, 0, "ciguse · 0 today\n")
	expect(t, []string{"use"}, 0, "ciguse · cigarette logged 12:41 · 1 today\n")
	// The first week shows only the count, even when more is asked for.
	expect(t, []string{"statusline", "--since", "--ratio"}, 0, "ciguse · 1 today\n")
	*clock = clock.AddDate(0, 0, 7)
	expect(t, []string{"use"}, 0, "ciguse · cigarette logged 12:41 · 1 today\n")
	*clock = clock.Add(47 * 60e9)
	expect(t, []string{"statusline", "--since", "--ratio"}, 0, "ciguse · 1 today · 47m since last\n")
}

func TestSetupCodex(t *testing.T) {
	setup(t)
	var calls []string
	old := codexCLI
	t.Cleanup(func() { codexCLI = old })
	codexCLI = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte("PLUGIN  STATUS  VERSION  SOURCE\n"), nil
	}
	out, code := run(t, "setup", "codex")
	if code != 0 || strings.Join(calls, "; ") != "plugin list; plugin marketplace add demetriusk/ciguse; plugin add ciguse@ciguse" {
		t.Fatalf("exit %d, calls %q:\n%s", code, calls, out)
	}
	contains(t, out, "Codex plugin installed", "/hooks and trust the ciguse hooks")

	// Already installed: nothing to do but the trust reminder.
	calls = nil
	codexCLI = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte("ciguse@ciguse  installed  0.7.0  /x\n"), nil
	}
	if out, _ := run(t, "setup", "codex"); len(calls) != 1 || !strings.Contains(out, "Codex plugin installed") || !strings.Contains(out, "/hooks") {
		t.Fatalf("calls %q:\n%s", calls, out)
	}

	// No codex command: the steps to run by hand.
	codexCLI = func(args ...string) ([]byte, error) { return nil, errors.New("not found") }
	out, _ = run(t, "setup", "codex")
	contains(t, out, "codex plugin marketplace add demetriusk/ciguse", "codex plugin add ciguse@ciguse")
}
