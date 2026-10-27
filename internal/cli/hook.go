package cli

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/demetriusk/ciguse/internal/store"
)

// stdin is replaced in tests.
var stdin io.Reader = os.Stdin

// hookInput declares the only hook fields ciguse reads. Everything else Claude Code
// sends, such as tool input and transcript paths, is never decoded.
type hookInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Prompt    string `json:"prompt"` // only compared against cig commands, never stored or logged
}

// cigRe matches a prompt typed as a ciguse command: cig, cig <product>, cig --force, cig undo.
var cigRe = regexp.MustCompile(`^cig(?:\s+([a-z]+))?(?:\s+(--force))?$`)

// cigArgs turns a cig prompt into ciguse arguments, such as [use pouch --force] or [undo].
func cigArgs(prompt string) ([]string, bool) {
	m := cigRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(prompt)))
	switch {
	case m == nil:
		return nil, false
	case m[1] == "undo" && m[2] == "":
		return []string{"undo"}, true
	case m[1] == "undo":
		return nil, false
	}
	args := []string{"use"}
	for _, a := range m[1:] {
		if a != "" {
			args = append(args, a)
		}
	}
	return args, true
}

// hookEvents maps each Claude Code hook the plugin registers to what it records.
var hookEvents = map[string]store.AgentEvent{
	"SessionStart":      {},
	"UserPromptSubmit":  {State: store.Working},
	"PostToolUse":       {State: store.Working, OnlyOnChange: true}, // closes a permission wait
	"PermissionRequest": {State: store.WaitingOnYou},
	"Stop":              {State: store.WaitingOnYou},
	"StopFailure":       {State: store.WaitingOnYou},
	"SessionEnd":        {State: store.Ended},
}

// runHook handles `ciguse hook claude <Event>` and `ciguse hook claude cig`. It always
// exits 0 and prints nothing but the cig block decision: a non-zero exit can block the
// user's prompt, and stdout can end up in Claude's context. Failures go to hook.log in
// the data directory instead.
func runHook(args []string, stdout io.Writer) int {
	at := now()
	event := "hook"
	if len(args) == 2 {
		if _, ok := hookEvents[args[1]]; ok || args[1] == "cig" {
			event = args[1]
		}
	}
	// A recovered panic returns 0 too, where an unrecovered one would exit 2.
	defer func() {
		if r := recover(); r != nil {
			logHookError(at, event, fmt.Errorf("panic: %v", r))
		}
	}()
	var err error
	if event == "cig" && args[0] == "claude" {
		err = cigHook(at, stdout)
	} else {
		err = hook(args, at)
	}
	if err != nil {
		logHookError(at, event, err)
	}
	return 0
}

// cigHook is the one sync hook, on UserPromptSubmit. A cig prompt is logged and blocked,
// so it never reaches Claude and the user sees only the confirmation. Any other prompt
// passes with no output, before anything is read from disk.
func cigHook(at time.Time, stdout io.Writer) error {
	var in hookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return errors.New("hook input is not valid JSON") // the decoder's own message can quote input
	}
	args, ok := cigArgs(in.Prompt)
	if !ok {
		return nil
	}
	// The confirmation is the CLI's own line, without color: it shows as plain text.
	var out, errb bytes.Buffer
	o, e := colorprofile.NewWriter(&out, nil), colorprofile.NewWriter(&errb, nil)
	var code int
	if args[0] == "undo" {
		code = runUndo(nil, o, e)
	} else {
		code = runUse(args[1:], "prompt", o, e)
	}
	reason := strings.TrimSpace(out.String())
	var err error
	switch code {
	case 0:
	case 2: // an unknown product: show it, the user can retype
		reason = strings.TrimSpace(errb.String())
	default: // never drop a cig silently or let it reach Claude
		err = errors.New(strings.TrimSpace(errb.String()))
		reason = "ciguse · couldn't save, run: ciguse " + strings.Join(args, " ")
	}
	json.NewEncoder(stdout).Encode(map[string]any{
		"decision":           "block",
		"reason":             reason,
		"hookSpecificOutput": map[string]any{"hookEventName": "UserPromptSubmit", "suppressOriginalPrompt": true},
	})
	return err
}

func hook(args []string, at time.Time) error {
	if len(args) != 2 || args[0] != "claude" {
		return errors.New("usage: ciguse hook claude <Event>")
	}
	e, ok := hookEvents[args[1]]
	if !ok {
		return errors.New("unknown hook event")
	}
	var in hookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return errors.New("hook input is not valid JSON") // the decoder's own message can quote input
	}
	// The cig hook blocks a cig prompt, so no turn starts.
	if _, ok := cigArgs(in.Prompt); ok && args[1] == "UserPromptSubmit" {
		return nil
	}
	if in.SessionID == "" || in.Cwd == "" {
		return errors.New("hook input lacks session_id or cwd")
	}

	dir, err := dataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, err := hookKey(dir)
	if err != nil {
		return err
	}
	root := projectRoot(in.Cwd)
	e.Session = keyed(key, "session", in.SessionID)
	e.Project = keyed(key, "project", normalizePath(root))
	e.Alias = filepath.Base(root)
	e.At = at
	e.Cause = args[1]

	// Every recording hook runs async, so nothing the user waits on sits behind this lock wait.
	s, err := store.Open(filepath.Join(dir, "ciguse.db"), 2*time.Second)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.RecordAgent(e)
}

// projectRoot is the nearest ancestor of dir holding .git (a directory, or a file in
// worktrees), or dir itself outside a repository.
func projectRoot(dir string) string {
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	dir = filepath.Clean(dir)
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// normalizePath makes one folder map to one key on case-insensitive Windows paths.
func normalizePath(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// keyed hides a session id or path behind an HMAC so the database never holds the raw value.
func keyed(key []byte, kind, value string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(kind + "\x00" + value))
	return hex.EncodeToString(m.Sum(nil))
}

// hookKey returns the local HMAC secret, creating it on first use. It lives outside the
// database so a copied database can't be matched back to paths by guessing them.
func hookKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, "secret")
	if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".secret-*") // created 0600
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	// Link refuses to replace an existing file, so when two hooks race the first secret wins.
	if err := os.Link(tmp.Name(), path); err != nil {
		if existing, rerr := os.ReadFile(path); rerr == nil && len(existing) == 32 {
			return existing, nil
		}
		return nil, fmt.Errorf("hook secret: %w", err)
	}
	return b, nil
}

// logHookError appends one line to hook.log, rolling it over past 64 KB.
// Callers must keep hook input out of err: messages here name failures, never values.
func logHookError(at time.Time, event string, err error) {
	dir, derr := dataDir()
	if derr != nil {
		return
	}
	path := filepath.Join(dir, "hook.log")
	if fi, serr := os.Stat(path); serr == nil && fi.Size() > 64<<10 {
		os.Rename(path, path+".1")
	}
	f, ferr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s: %v\n", at.UTC().Format(time.RFC3339), event, err)
}
