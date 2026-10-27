package cli

import (
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
	"runtime"
	"strings"
	"time"

	"github.com/demetriusk/ciguse/internal/store"
)

// stdin is replaced in tests.
var stdin io.Reader = os.Stdin

// hookInput declares the only hook fields ciguse reads. Everything else Claude Code
// sends, such as prompts, tool input and transcript paths, is never decoded.
type hookInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
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

// runHook handles `ciguse hook claude <Event>`. It always exits 0 and prints nothing:
// a non-zero exit can block the user's prompt, and stdout can end up in Claude's context.
// Failures go to hook.log in the data directory instead.
func runHook(args []string) int {
	at := now()
	event := "hook"
	if len(args) == 2 {
		if _, ok := hookEvents[args[1]]; ok {
			event = args[1]
		}
	}
	// A recovered panic returns 0 too, where an unrecovered one would exit 2.
	defer func() {
		if r := recover(); r != nil {
			logHookError(at, event, fmt.Errorf("panic: %v", r))
		}
	}()
	if err := hook(args, at); err != nil {
		logHookError(at, event, err)
	}
	return 0
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
