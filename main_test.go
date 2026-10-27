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
)

// TestHookLatency runs the built binary the way Claude Code does. A UserPromptSubmit hook
// holds the user's prompt until it exits, so its p95 must stay under 30 ms (spec section 6).
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

	run := func() time.Duration {
		cmd := exec.Command(bin, "hook", "claude", "UserPromptSubmit")
		cmd.Env = append(os.Environ(), "CIGUSE_HOME="+home)
		cmd.Stdin = strings.NewReader(input)
		start := time.Now()
		out, err := cmd.CombinedOutput()
		d := time.Since(start)
		if err != nil || len(out) > 0 {
			t.Fatalf("hook: %v, output %q", err, out)
		}
		return d
	}
	run() // the first run creates the database and the secret
	var ds []time.Duration
	for range 60 {
		ds = append(ds, run())
	}
	slices.Sort(ds)
	p50, p95 := ds[len(ds)/2], ds[len(ds)*95/100]
	t.Logf("UserPromptSubmit hook on %s: p50 %v, p95 %v, max %v over %d runs", runtime.GOOS, p50, p95, ds[len(ds)-1], len(ds))
	if p95 > 30*time.Millisecond {
		t.Errorf("p95 %v is over the 30ms budget", p95)
	}
	if log, err := os.ReadFile(filepath.Join(home, "hook.log")); err == nil {
		t.Errorf("hook logged errors:\n%s", log)
	}
}
