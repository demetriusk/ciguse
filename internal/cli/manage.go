package cli

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// runProject handles `ciguse project alias <name>`, run in the project's folder.
func runProject(args []string, stdout, stderr io.Writer) int {
	pos, ok := parse(newFlags("project", stderr), args, 2)
	if !ok {
		return 2
	}
	if len(pos) != 2 || pos[0] != "alias" || strings.TrimSpace(pos[1]) == "" {
		fmt.Fprintln(stderr, "ciguse: usage: ciguse project alias <name>, in the project's folder")
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail(stderr, err)
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
	key, err := hookKey(dir)
	if err != nil {
		return fail(stderr, err)
	}
	root := projectRoot(cwd)
	found, err := s.SetAlias(keyed(key, "project", normalizePath(root)), strings.TrimSpace(pos[1]))
	if err != nil {
		return fail(stderr, err)
	}
	if !found {
		fmt.Fprintln(stdout, prefix, "no Claude Code session seen in", tildePath(root), "yet, so nothing to rename")
		return 0
	}
	fmt.Fprintf(stdout, "%s %s now shows as %s\n", prefix, tildePath(root), bold.Render(strings.TrimSpace(pos[1])))
	return 0
}

// runExport writes every use, undone ones included, as JSON lines or CSV.
func runExport(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("export", stderr)
	asCSV := fs.Bool("csv", false, "CSV with a header row instead of JSON lines")
	if _, ok := parse(fs, args, 0); !ok {
		return 2
	}
	s, code := openStore(stderr)
	if s == nil {
		return code
	}
	defer s.Close()
	es, err := s.Events()
	if err != nil {
		return fail(stderr, err)
	}
	header := []string{"id", "time", "logged_at", "product", "pack_price", "pack_size", "currency", "source", "undone_at"}
	c := csv.NewWriter(stdout)
	if *asCSV {
		c.Write(header)
	}
	j := json.NewEncoder(stdout)
	for _, e := range es {
		row := []string{strconv.FormatInt(e.ID, 10), e.At.Format(time.RFC3339), e.LoggedAt.Format(time.RFC3339),
			e.Product, "", "", e.Currency.String, e.Source, ""}
		if e.PackPriceMinor.Valid {
			row[4], row[5] = formatMinor(e.PackPriceMinor.Int64), strconv.FormatInt(e.PackSize.Int64, 10)
		}
		if e.Deleted.Valid {
			row[8] = e.Deleted.Time.Format(time.RFC3339)
		}
		if *asCSV {
			c.Write(row)
			continue
		}
		obj := map[string]any{}
		for i, k := range header {
			obj[k] = row[i]
			if row[i] == "" {
				obj[k] = nil
			}
		}
		obj["id"], obj["pack_size"] = e.ID, nil
		if e.PackSize.Valid {
			obj["pack_size"] = e.PackSize.Int64
		}
		j.Encode(obj)
	}
	c.Flush()
	if err := c.Error(); err != nil {
		return fail(stderr, err)
	}
	return 0
}

// runDoctor checks what the hooks and status line depend on and shows recent hook errors.
func runDoctor(args []string, version string, stdout, stderr io.Writer) int {
	if _, ok := parse(newFlags("doctor", stderr), args, 0); !ok {
		return 2
	}
	row := func(label, value string) {
		fmt.Fprintf(stdout, "  %s %s\n", muted.Render(fmt.Sprintf("%-12s", label)), value)
	}
	fmt.Fprintln(stdout, prefix, "doctor")
	row("version", version)
	if p, err := exec.LookPath("ciguse"); err == nil {
		row("on PATH", tildePath(p))
	} else {
		row("on PATH", accent.Render("not found")+", so Claude Code hooks can't run ciguse")
	}
	dir, err := dataDir()
	if err != nil {
		return fail(stderr, err)
	}
	row("data", tildePath(dir))
	s, code := openStore(stderr)
	if s == nil {
		return code
	}
	h, err := s.Health()
	s.Close()
	if err != nil {
		row("database", accent.Render(err.Error()))
	} else {
		row("database", fmt.Sprintf("ok · %d uses · %d Claude Code sessions", h.Uses, h.Sessions))
	}
	if h.LastHook.IsZero() {
		row("last hook", "none yet · /plugin install ciguse --marketplace demetriusk/ciguse")
	} else {
		row("last hook", h.LastHook.In(time.Local).Format("02 Jan 15:04")+" · "+h.LastCause)
	}
	settings := claudeSettingsPath()
	data, _ := os.ReadFile(settings)
	if line, err := statusLineValue(data); err == nil && strings.Contains(fmt.Sprint(line["command"]), "ciguse statusline") {
		row("status line", "set in "+tildePath(settings))
	} else {
		row("status line", "not set · ciguse setup claude")
	}

	log, err := os.ReadFile(filepath.Join(dir, "hook.log"))
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if err != nil || len(log) == 0 {
		row("hook errors", "none")
		return 0
	}
	row("hook errors", fmt.Sprintf("last %d of hook.log", min(len(lines), 20)))
	for _, l := range lines[max(0, len(lines)-20):] {
		fmt.Fprintln(stdout, "    "+muted.Render(l))
	}
	return 0
}

// runPurge deletes every file ciguse keeps, after the user types "delete".
func runPurge(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("purge", stderr)
	yes := fs.Bool("yes", false, "delete without asking, for scripts")
	if _, ok := parse(fs, args, 0); !ok {
		return 2
	}
	dir, err := dataDir()
	if err != nil {
		return fail(stderr, err)
	}
	s, code := openStore(stderr)
	if s == nil {
		return code
	}
	written, err := s.Config(cfgWritten)
	s.Close()
	if err != nil {
		return fail(stderr, err)
	}
	// The backup is the only copy of the user's own status line.
	if written != "" {
		fmt.Fprintln(stderr, "ciguse: ciguse is still in your Claude Code status line. Run `ciguse setup claude --remove` first, so your own comes back")
		return 1
	}
	if !*yes {
		if !canAsk() {
			fmt.Fprintln(stderr, "ciguse: purge deletes all data. Run it in a terminal to confirm, or pass --yes")
			return 2
		}
		fmt.Fprintf(stdout, "This deletes every use, Claude Code session and setting in %s.\nType %s to confirm: ",
			tildePath(dir), bold.Render("delete"))
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if strings.TrimSpace(line) != "delete" {
			fmt.Fprintln(stdout, prefix, "nothing deleted")
			return 1
		}
	}
	for _, f := range []string{"ciguse.db", "ciguse.db-wal", "ciguse.db-shm", "secret", "hook.log", "hook.log.1", "claude-settings.backup.json"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fail(stderr, err)
		}
	}
	os.Remove(dir) // only when nothing else is in it
	fmt.Fprintln(stdout, prefix, "deleted all data in", tildePath(dir))
	return 0
}
