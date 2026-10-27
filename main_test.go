package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/demetriusk/ciguse/internal/store"
)

// TestHookLatency runs the built binary the way Claude Code does and logs where the time goes.
// The recording hooks all run async, so only the sync cig check holds the user's prompt: it
// must stay under the 30 ms budget (spec section 6) for a prompt that isn't cig.
// It only runs with CIGUSE_LATENCY=1, on its own: alongside other packages' tests the
// machine is busy and the numbers mean nothing.
func TestHookLatency(t *testing.T) {
	if os.Getenv("CIGUSE_LATENCY") != "1" {
		t.Skip("set CIGUSE_LATENCY=1 and run alone: CIGUSE_LATENCY=1 go test -run Latency -v .")
	}
	bin := filepath.Join(t.TempDir(), "ciguse")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := t.TempDir()
	cwd, _ := json.Marshal(t.TempDir())
	prompt, _ := json.Marshal(strings.Repeat("refactor the parser and keep the tests green ", 50))
	input := `{"session_id":"bench","transcript_path":"/tmp/t.jsonl","cwd":` + string(cwd) +
		`,"permission_mode":"default","hook_event_name":"UserPromptSubmit","prompt":` + string(prompt) + `}`

	hook := func(event string) func() {
		return func() {
			cmd := exec.Command(bin, "hook", "claude", event)
			cmd.Env = append(os.Environ(), "CIGUSE_HOME="+home)
			cmd.Stdin = strings.NewReader(input)
			if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
				t.Fatalf("hook %s: %v, output %q", event, err, out)
			}
		}
	}
	hook("UserPromptSubmit")() // the first run creates the database and the secret

	// The breakdown shows where the time goes when the budget is missed.
	measure(t, "process start (ciguse version)", func() {
		if err := exec.Command(bin, "version").Run(); err != nil {
			t.Fatal(err)
		}
	})
	db := filepath.Join(t.TempDir(), "ciguse.db")
	measure(t, "database open + write, in process", func() {
		s, err := store.Open(db, 250*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.RecordAgent(store.AgentEvent{Session: "s", Project: "p", Alias: "a", At: time.Now(), State: store.Working, Cause: "UserPromptSubmit"}); err != nil {
			t.Fatal(err)
		}
	})
	measure(t, "recording hook (async)", hook("UserPromptSubmit"))
	if p95 := measure(t, "cig check on another prompt (sync)", hook("cig")); p95 > 30*time.Millisecond {
		t.Errorf("cig check p95 %v, budget 30ms", p95)
	}
	statusline := func() {
		cmd := exec.Command(bin, "statusline")
		cmd.Env = append(os.Environ(), "CIGUSE_HOME="+home)
		cmd.Stdin = strings.NewReader(`{"session_id":"bench"}`)
		if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "today") {
			t.Fatalf("statusline: %v, output %q", err, out)
		}
	}
	if p95 := measure(t, "status line (sync, reads the database)", statusline); p95 > 30*time.Millisecond {
		t.Errorf("status line p95 %v, budget 30ms", p95)
	}
	if log, err := os.ReadFile(filepath.Join(home, "hook.log")); err == nil {
		t.Errorf("hook logged errors:\n%s", log)
	}
}

// measure runs fn 60 times, logs p50, p95 and max, and returns p95.
func measure(t *testing.T, name string, fn func()) time.Duration {
	t.Helper()
	var ds []time.Duration
	for range 60 {
		start := time.Now()
		fn()
		ds = append(ds, time.Since(start))
	}
	slices.Sort(ds)
	t.Logf("%s on %s: p50 %v, p95 %v, max %v", name, runtime.GOOS, ds[len(ds)/2], ds[len(ds)*95/100], ds[len(ds)-1])
	return ds[len(ds)*95/100]
}
