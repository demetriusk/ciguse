package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/demetriusk/ciguse/internal/store"
)

// Config keys for the status line ciguse wrapped and the settings file it wrote.
const (
	cfgWrapped = "statusline_wrapped"
	cfgWritten = "statusline_written_sha256"
)

// runStatusline prints the ciguse segment for Claude Code's status line. It runs every few
// seconds and on every message, so it only reads, and on any error it prints what it can.
func runStatusline(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("statusline", stderr)
	wrap := fs.Bool("wrap", false, "run the status line ciguse replaced, then append the segment")
	since := fs.Bool("since", false, "add time since the last use, after the first week")
	ratio := fs.Bool("ratio", false, "add waiting vs working, once there's enough data")
	if _, ok := parse(fs, args, 0); !ok {
		return 2
	}
	// Claude Code renders ANSI colors, though stdout is a pipe.
	w := &colorprofile.Writer{Forward: stdout, Profile: colorprofile.ANSI}
	if os.Getenv("NO_COLOR") != "" {
		w.Profile = colorprofile.NoTTY
	}

	var seg, wrapped string
	dir, err := dataDir()
	if err == nil {
		var s *store.Store
		if s, err = store.Open(filepath.Join(dir, "ciguse.db"), 250*time.Millisecond); err == nil {
			seg, err = segment(s, now(), *since, *ratio)
			if *wrap {
				wrapped, _ = s.Config(cfgWrapped)
			}
			s.Close()
		}
	}
	if *wrap && wrapped != "" {
		in, _ := io.ReadAll(stdin)
		out := strings.TrimRight(runWrapped(wrapped, in), "\n")
		if out != "" && seg != "" {
			seg = out + "  " + seg
		} else if out != "" {
			seg = out
		}
	}
	if seg != "" {
		fmt.Fprintln(w, seg)
	}
	if err != nil {
		fmt.Fprintf(stderr, "ciguse: %v\n", err)
	}
	return 0
}

// segment is "ciguse · 5 today", plus the opt-in parts once the first week is over.
func segment(s *store.Store, t time.Time, since, ratio bool) (string, error) {
	n, err := s.Today(t)
	if err != nil {
		return "", err
	}
	parts := []string{prefix + " " + bold.Render(fmt.Sprintf("%d today", n))}
	if since || ratio {
		first, last, ok, err := s.Span()
		if err != nil {
			return "", err
		}
		// The first week is a baseline: just the count, nothing that might work as a cue.
		if ok && t.Sub(first) >= 7*24*time.Hour {
			if since {
				parts = append(parts, ago(t.Sub(last))+" since last")
			}
			if ratio {
				sp, err := s.Split(t.AddDate(0, 0, -14), t)
				if err != nil {
					return "", err
				}
				if rr, _, _, ok := rateRatio(sp); ok {
					parts = append(parts, fmt.Sprintf("waiting %.1f× working", rr))
				}
			}
		}
	}
	return strings.Join(parts, muted.Render(" · ")), nil
}

func ago(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// runWrapped runs the user's previous status line command with the same input and returns
// its output. A command that fails or hangs gives no output instead of blocking ciguse's.
func runWrapped(command string, in []byte) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := shellCommand(ctx, command)
	c.Stdin = bytes.NewReader(in)
	c.Stderr = io.Discard
	out, _ := c.Output()
	return string(out)
}

// shellCommand runs a status line command the way Claude Code does: through sh, or on
// Windows through Git Bash when it's installed and PowerShell otherwise.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS != "windows" {
		return exec.CommandContext(ctx, "sh", "-c", command)
	}
	if bash := gitBash(); bash != "" {
		return exec.CommandContext(ctx, bash, "-c", command)
	}
	return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
}

// gitBash finds Git for Windows' bash, not the WSL launcher that may also be on PATH.
func gitBash() string {
	if p := os.Getenv("CLAUDE_CODE_GIT_BASH_PATH"); p != "" {
		return p
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe") // <root>\cmd\git.exe
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// claudeCLI runs the claude command. Replaced in tests.
var claudeCLI = func(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, err
	}
	return exec.Command("claude", args...).CombinedOutput()
}

// runSetup handles `ciguse setup claude|codex [--remove]`. For Claude Code: the plugin, and
// ciguse in the status line. For Codex: the plugin.
func runSetup(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("setup", stderr)
	remove := fs.Bool("remove", false, "undo: the status line for claude, the plugin for codex")
	pos, ok := parse(fs, args, 1)
	if !ok {
		return 2
	}
	switch first(pos) {
	case "codex":
		setupCodex(*remove, stdout)
		return 0
	case "claude":
	default:
		fmt.Fprintln(stderr, "ciguse: usage: ciguse setup claude|codex [--remove]")
		return 2
	}
	s, code := openStore(stderr)
	if s == nil {
		return code
	}
	defer s.Close()
	dir, err := dataDir()
	if err != nil {
		return fail(stderr, err)
	}
	settings := claudeSettingsPath()
	backup := filepath.Join(dir, "claude-settings.backup.json")
	if *remove {
		return removeStatusline(s, settings, backup, stdout, stderr)
	}

	setupPlugin(stdout)
	old, err := os.ReadFile(settings)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(stderr, err)
	}
	line, err := statusLineValue(old)
	if err != nil {
		return fail(stderr, fmt.Errorf("%s: %w", tildePath(settings), err))
	}
	before, _ := line["command"].(string)
	// Without a refresh timer, a log from another session or the hotkey shows only after
	// this session's next message. A timer the user set stays as it is.
	_, timed := line["refreshInterval"]
	again := strings.Contains(before, "ciguse statusline")
	if again && timed {
		fmt.Fprintln(stdout, prefix, "status line already shows ciguse")
		return 0
	}
	switch {
	case again: // set up by ciguse 0.6.0, which kept a missing timer missing
	case before != "":
		line["command"] = "ciguse statusline --wrap"
	default:
		line = map[string]any{"type": "command", "command": "ciguse statusline"}
	}
	if !timed {
		line["refreshInterval"] = 15
	}
	value, _ := json.MarshalIndent(line, "  ", "  ")
	updated, err := setKey(old, "statusLine", value)
	if err != nil {
		return fail(stderr, fmt.Errorf("%s: %w", tildePath(settings), err))
	}

	// The backup and the wrapped command come from the first setup only; a later fix keeps
	// them, so --remove still puts back the original byte for byte.
	written, err := s.Config(cfgWritten)
	if err != nil {
		return fail(stderr, err)
	}
	if !again {
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return fail(stderr, err)
		}
		if err := s.SetConfig(cfgWrapped, before); err != nil {
			return fail(stderr, err)
		}
	}
	if !again || written != "" {
		sum := sha256.Sum256(updated)
		if err := s.SetConfig(cfgWritten, hex.EncodeToString(sum[:])); err != nil {
			return fail(stderr, err)
		}
	}
	if err := writeFileAtomic(settings, updated); err != nil {
		return fail(stderr, err)
	}
	if again {
		fmt.Fprintln(stdout, prefix, "status line now refreshes every 15 seconds, so logs from other sessions show up")
		return 0
	}
	if before == "" {
		before = "none"
	}
	fmt.Fprintln(stdout, prefix, "status line set in", tildePath(settings))
	fmt.Fprintf(stdout, "  %s     %s\n  %s     %s\n  %s  %s\n  %s    %s\n",
		muted.Render("was"), before, muted.Render("now"), line["command"],
		muted.Render("backup"), tildePath(backup), muted.Render("undo"), accent.Render("ciguse setup claude --remove"))
	return 0
}

// codexCLI runs the codex command. Replaced in tests.
var codexCLI = func(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return nil, err
	}
	return exec.Command("codex", args...).CombinedOutput()
}

// setupCodex installs or removes the Codex plugin, served from the same marketplace as the
// Claude Code one. Codex runs a plugin's hooks only once the user trusts them in /hooks.
func setupCodex(remove bool, stdout io.Writer) {
	if remove {
		if _, err := codexCLI("plugin", "remove", "ciguse@ciguse"); err != nil {
			fmt.Fprintf(stdout, "%s remove the Codex plugin with:\n  %s\n", prefix, accent.Render("codex plugin remove ciguse@ciguse"))
			return
		}
		fmt.Fprintln(stdout, prefix, "Codex plugin removed")
		return
	}
	list, err := codexCLI("plugin", "list")
	listed, installed := false, false
	for _, line := range strings.Split(string(list), "\n") {
		if strings.Contains(line, "ciguse@ciguse") {
			listed, installed = true, !strings.Contains(line, "not installed")
		}
	}
	if err == nil && !listed {
		_, err = codexCLI("plugin", "marketplace", "add", "demetriusk/ciguse")
	}
	if err == nil && !installed {
		_, err = codexCLI("plugin", "add", "ciguse@ciguse")
	}
	if err != nil {
		fmt.Fprintf(stdout, "%s install the Codex plugin with:\n  %s\n  %s\n", prefix,
			accent.Render("codex plugin marketplace add demetriusk/ciguse"), accent.Render("codex plugin add ciguse@ciguse"))
	} else {
		fmt.Fprintln(stdout, prefix, "Codex plugin installed")
	}
	fmt.Fprintf(stdout, "  %s  in Codex, run %s and trust the ciguse hooks. Codex skips new hooks until you do\n",
		muted.Render("then"), accent.Render("/hooks"))
}

// setupPlugin installs the plugin with the claude command, or says how to.
func setupPlugin(stdout io.Writer) {
	list, err := claudeCLI("plugin", "list")
	if err == nil && bytes.Contains(list, []byte("ciguse@ciguse")) {
		fmt.Fprintln(stdout, prefix, "Claude Code plugin installed")
		return
	}
	if err == nil {
		if _, err = claudeCLI("plugin", "marketplace", "add", "demetriusk/ciguse"); err == nil {
			_, err = claudeCLI("plugin", "install", "ciguse@ciguse")
		}
	}
	if err != nil {
		fmt.Fprintf(stdout, "%s install the Claude Code plugin from a session:\n  %s\n", prefix,
			accent.Render("/plugin install ciguse --marketplace demetriusk/ciguse"))
		return
	}
	fmt.Fprintln(stdout, prefix, "Claude Code plugin installed, active in new sessions")
}

func removeStatusline(s *store.Store, settings, backup string, stdout, stderr io.Writer) int {
	written, err := s.Config(cfgWritten)
	if err != nil {
		return fail(stderr, err)
	}
	orig, berr := os.ReadFile(backup)
	if written == "" || berr != nil {
		fmt.Fprintln(stdout, prefix, "ciguse isn't in the status line, nothing to undo")
		return 0
	}
	cur, err := os.ReadFile(settings)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(stderr, err)
	}
	sum := sha256.Sum256(cur)
	switch {
	case hex.EncodeToString(sum[:]) == written && len(orig) == 0:
		err = os.Remove(settings) // ciguse created it
	case hex.EncodeToString(sum[:]) == written:
		err = writeFileAtomic(settings, orig) // unchanged since setup: the original, byte for byte
	default:
		// Changed since setup: put back only the status line, keeping the other edits.
		var prev map[string]any
		if prev, err = statusLineValue(orig); err == nil {
			var value []byte
			if len(prev) > 0 {
				value, _ = json.MarshalIndent(prev, "  ", "  ")
			}
			var updated []byte
			if updated, err = setKey(cur, "statusLine", value); err == nil {
				err = writeFileAtomic(settings, updated)
			}
		}
	}
	if err != nil {
		return fail(stderr, err)
	}
	s.SetConfig(cfgWrapped, "")
	s.SetConfig(cfgWritten, "")
	os.Remove(backup)
	fmt.Fprintln(stdout, prefix, "status line restored in", tildePath(settings))
	return 0
}

// claudeSettingsPath is Claude Code's user settings file, in $CLAUDE_CONFIG_DIR when set.
func claudeSettingsPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// statusLineValue returns the statusLine object in a settings file, empty when there's none.
func statusLineValue(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var v struct {
		StatusLine map[string]any `json:"statusLine"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, errors.New("not valid JSON, left unchanged")
	}
	if v.StatusLine == nil {
		v.StatusLine = map[string]any{}
	}
	return v.StatusLine, nil
}

// setKey sets a top-level key of a JSON object to value, or deletes it when value is nil,
// editing only those bytes so the rest of the file keeps its formatting.
func setKey(data []byte, key string, value []byte) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}\n")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object, left unchanged")
	}
	open := dec.InputOffset()
	prevEnd, members := open, 0
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, errors.New("not valid JSON, left unchanged")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, errors.New("not valid JSON, left unchanged")
		}
		end := dec.InputOffset()
		if t == key {
			if value != nil {
				return join(data[:end-int64(len(raw))], value, data[end:]), nil
			}
			if members > 0 { // drop ", key: value" after the previous member
				return join(data[:prevEnd], data[end:]), nil
			}
			rest := data[end:] // first member: drop "key: value," and keep what followed
			if i := bytes.IndexByte(rest, ','); i >= 0 && len(bytes.TrimSpace(rest[:i])) == 0 {
				rest = rest[i+1:]
			}
			return join(data[:open], rest), nil
		}
		prevEnd, members = end, members+1
	}
	if value == nil {
		return data, nil
	}
	member := []byte("\n  \"" + key + "\": " + string(value))
	if members > 0 {
		return join(data[:prevEnd], []byte(","), member, data[prevEnd:]), nil
	}
	close := bytes.LastIndexByte(data, '}')
	return join(data[:open], member, []byte("\n"), data[close:]), nil
}

func join(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

// writeFileAtomic replaces path so Claude Code never reads a half-written settings file.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
