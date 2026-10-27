package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run runs ciguse and returns everything it printed, for checks on part of the output.
func run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, "test", &out, &errb)
	return out.String() + errb.String(), code
}

func TestProjectAlias(t *testing.T) {
	dir, _ := setup(t)
	r := repo(t, t.TempDir(), "api-server")
	runHookInput(t, `{"session_id":"s","cwd":`+quote(r)+`}`, "claude", "UserPromptSubmit")

	t.Chdir(filepath.Join(r, ".git")) // a subfolder of the project counts too
	if out, code := run(t, "project", "alias", "api"); code != 0 || !strings.Contains(out, "now shows as api") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := queryDB(t, dir, `SELECT alias FROM projects`); len(got) != 1 || got[0] != "api" {
		t.Fatalf("aliases %q", got)
	}
	t.Chdir(t.TempDir())
	out, _ := run(t, "project", "alias", "x")
	contains(t, out, "no Claude Code session seen in")
	if out, code := run(t, "project", "alias"); code != 2 {
		t.Fatalf("missing name: exit %d, %s", code, out)
	}
}

func TestExport(t *testing.T) {
	_, clock := setup(t)
	run(t, "init", "--product", "iqos", "--pack-price", "6", "--currency", "EUR")
	run(t, "use")
	*clock = clock.Add(5 * 60e9)
	run(t, "use", "pouch")
	run(t, "undo")

	out, code := run(t, "export", "--csv")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 3 || lines[0] != "id,time,logged_at,product,pack_price,pack_size,currency,source,undone_at" ||
		!strings.Contains(lines[1], ",iqos,6.00,20,EUR,cli,") || !strings.HasSuffix(lines[2], ",pouch,,,,cli,"+clock.Format("2006-01-02T15:04:05Z07:00")) {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	out, _ = run(t, "export")
	if !strings.Contains(out, `"pack_price":"6.00","pack_size":20,"product":"iqos","source":"cli","time":`) ||
		!strings.Contains(out, `"pack_price":null,"pack_size":null,"product":"pouch"`) {
		t.Fatalf("json:\n%s", out)
	}
}

func TestDoctor(t *testing.T) {
	dir, _ := setup(t)
	claudeHome(t, "")
	out, code := run(t, "doctor")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	contains(t, out, "version      test", "database     ok · 0 uses · 0 Claude Code sessions", "last hook    none yet",
		"status line  not set · ciguse setup claude", "hook errors  none")

	os.WriteFile(filepath.Join(dir, "hook.log"), []byte("2026-10-27T11:00:00Z Stop: database is locked\n"), 0o600)
	runHookInput(t, `{"session_id":"s","cwd":`+quote(t.TempDir())+`}`, "claude", "Stop")
	out, _ = run(t, "doctor")
	contains(t, out, "1 Claude Code sessions", "last hook    27 Oct 12:41 · Stop", "hook errors  last 1 of hook.log", "Stop: database is locked")
}

func TestPurge(t *testing.T) {
	dir, _ := setup(t)
	claudeHome(t, "")
	run(t, "use")

	if out, code := run(t, "purge"); code != 2 || !strings.Contains(out, "pass --yes") {
		t.Fatalf("without a terminal: exit %d, %s", code, out)
	}
	if out, code := ask(t, "no\n", "purge"); code != 1 || !strings.Contains(out, "nothing deleted") {
		t.Fatalf("wrong word: exit %d, %s", code, out)
	}
	run(t, "setup", "claude")
	if out, code := run(t, "purge", "--yes"); code != 1 || !strings.Contains(out, "setup claude --remove") {
		t.Fatalf("with the status line set: exit %d, %s", code, out)
	}
	run(t, "setup", "claude", "--remove")
	if out, code := ask(t, "delete\n", "purge"); code != 0 || !strings.Contains(out, "deleted all data") {
		t.Fatalf("exit %d, %s", code, out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("data dir still there: %v", err)
	}
}
