package cli

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// runHookInput runs `ciguse hook <args>` with input on stdin and fails unless it exits 0 silently.
func runHookInput(t *testing.T, input string, args ...string) {
	t.Helper()
	stdin = strings.NewReader(input)
	t.Cleanup(func() { stdin = os.Stdin })
	var out, errb bytes.Buffer
	if code := Run(append([]string{"hook"}, args...), "test", &out, &errb); code != 0 || out.Len()+errb.Len() > 0 {
		t.Fatalf("ciguse hook %s: exit %d, output %q", strings.Join(args, " "), code, out.String()+errb.String())
	}
}

// repo makes <parent>/<name>/.git and returns the repo path.
func repo(t *testing.T, parent, name string) string {
	t.Helper()
	r := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(r, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return r
}

func queryDB(t *testing.T, dir, q string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ciguse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestHookRecordsTurn(t *testing.T) {
	dir, clock := setup(t)
	sub := filepath.Join(repo(t, t.TempDir(), "api-server"), "internal")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	input := `{"session_id":"abc123","cwd":` + quote(sub) + `,"hook_event_name":"x"}`
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "PermissionRequest", "PostToolUse", "Stop", "SessionEnd"} {
		*clock = clock.Add(time.Minute)
		runHookInput(t, input, "claude", event)
	}

	got := strings.Join(queryDB(t, dir, `SELECT state || ' ' || cause FROM transitions ORDER BY ts, id`), ", ")
	want := "working UserPromptSubmit, waiting_on_you PermissionRequest, working PostToolUse, waiting_on_you Stop, ended SessionEnd"
	if got != want {
		t.Fatalf("transitions:\n got %s\nwant %s", got, want)
	}
	if got := queryDB(t, dir, `SELECT alias FROM projects`); len(got) != 1 || got[0] != "api-server" {
		t.Fatalf("projects %q", got)
	}
	if got := queryDB(t, dir, `SELECT session_key FROM sessions`); len(got) != 1 || len(got[0]) != 64 || got[0] == "abc123" {
		t.Fatalf("sessions %q", got)
	}
}

// Acceptance test 3: hook input never reaches disk beyond the repo folder name.
func TestHookPrivacy(t *testing.T) {
	dir, clock := setup(t)
	const marker = "SECRET_MARKER"
	cwd := filepath.Join(repo(t, filepath.Join(t.TempDir(), marker+"-parent"), "proj"), marker+"-sub")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	full := `{"session_id":"` + marker + `-session","cwd":` + quote(cwd) + `,
		"transcript_path":"/home/u/.claude/projects/` + marker + `.jsonl","prompt":"cig ` + marker + `",
		"tool_name":"Bash","tool_input":{"command":"echo ` + marker + `"},"tool_response":{"stdout":"` + marker + `"},
		"last_assistant_message":"` + marker + `","permission_mode":"default"}`
	inputs := []string{
		full,
		`{"session_id":"s2","cwd":` + quote(filepath.Join(cwd, "gone-"+marker)) + `}`, // cwd that doesn't exist
		`{"session_id":"` + marker + `", oops ` + marker + `}`,                        // malformed
		`{"session_id":5,"cwd":"` + marker + `"}`,                                     // wrong type
		`{"prompt":"` + marker + `"}`,                                                 // missing fields
	}
	for _, in := range inputs {
		for event := range hookEvents {
			*clock = clock.Add(time.Second)
			runHookInput(t, in, "claude", event)
		}
	}

	if n := queryDB(t, dir, `SELECT count(*) FROM transitions`); n[0] == "0" {
		t.Fatal("nothing recorded, so the check below proves nothing")
	}
	log, err := os.ReadFile(filepath.Join(dir, "hook.log"))
	if err != nil || len(log) == 0 {
		t.Fatalf("expected logged errors from the bad inputs: %v", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(marker)) {
			t.Errorf("%s contains hook input", f.Name())
		}
	}
}

// Acceptance test 4: whatever breaks, the hook exits 0 and prints nothing.
func TestHookFailsOpen(t *testing.T) {
	dir, _ := setup(t)
	input := `{"session_id":"s","cwd":` + quote(t.TempDir()) + `}`

	runHookInput(t, input)
	runHookInput(t, input, "codex", "Stop")
	runHookInput(t, input, "claude", "NotAnEvent")
	runHookInput(t, "not json", "claude", "Stop")

	if err := os.WriteFile(filepath.Join(dir, "ciguse.db"), []byte("this is not a database, just junk bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHookInput(t, input, "claude", "UserPromptSubmit")
	if log, _ := os.ReadFile(filepath.Join(dir, "hook.log")); !strings.Contains(string(log), "UserPromptSubmit: ") {
		t.Fatalf("corrupt database not logged:\n%s", log)
	}

	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("CIGUSE_HOME", file) // data dir can't be created
	runHookInput(t, input, "claude", "Stop")
}

func TestHookGivesUpQuicklyOnLockedDatabase(t *testing.T) {
	dir, _ := setup(t)
	input := `{"session_id":"s","cwd":` + quote(t.TempDir()) + `}`
	runHookInput(t, input, "claude", "SessionStart") // create the database

	db, err := sql.Open("sqlite", filepath.Join(dir, "ciguse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("ROLLBACK")

	start := time.Now()
	runHookInput(t, input, "claude", "UserPromptSubmit")
	if d := time.Since(start); d > time.Second {
		t.Fatalf("UserPromptSubmit waited %v on a locked database, want about 250ms", d)
	}
	if log, _ := os.ReadFile(filepath.Join(dir, "hook.log")); !strings.Contains(string(log), "UserPromptSubmit: ") {
		t.Fatalf("lock failure not logged:\n%s", log)
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

func TestHookKeyRaceAgreesOnOneSecret(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	keys := make(chan []byte, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			k, err := hookKey(dir)
			if err != nil {
				t.Error(err)
			}
			keys <- k
		}()
	}
	close(start)
	wg.Wait()
	close(keys)
	first := <-keys
	for k := range keys {
		if !bytes.Equal(k, first) || len(k) != 32 {
			t.Fatal("concurrent first runs produced different secrets")
		}
	}
	if files, _ := os.ReadDir(dir); len(files) != 1 {
		t.Fatalf("leftover temp files: %d entries", len(files))
	}
}
