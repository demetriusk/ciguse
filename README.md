# ciguse

Log nicotine use from the terminal and find out whether you use more while your coding agent works or while it waits on you. Works with Claude Code and Codex. Local only: no account, no network, no telemetry.

![Setting up ciguse, logging a use, a two-week report on sample data, and the Claude Code status line](docs/demo.gif)

## Start in 4 steps

1. **Install**

   ```sh
   brew install demetriusk/tap/ciguse
   ```

   No Homebrew, or on Linux or Windows? See [other ways to install](#other-ways-to-install).

2. **Set up.** It asks what you use and, if you like, what a pack costs.

   ```sh
   ciguse init
   ```

3. **Connect your agent.** For Claude Code, this installs the plugin and puts today's count in its status line:

   ```sh
   ciguse setup claude
   ```

   For Codex, this installs the plugin. Then run `/hooks` in Codex and trust the ciguse hooks, since Codex skips new hooks until you do:

   ```sh
   ciguse setup codex
   ```

4. **Log each use**, whichever way is closest:
   - type `cig` in Claude Code or Codex
   - press a global key: run `ciguse hotkey` once to set one up
   - run `ciguse use` in any terminal

After a week or two, run `ciguse report` to see working vs waiting.

## Everyday commands

| Command | What it does |
|---|---|
| `ciguse use` | log one use of your default product |
| `ciguse use pouch` | log another product: `cigarette`, `iqos`, `glo`, `stick`, `pouch` |
| `ciguse use --ago 10m` | log one you forgot |
| `ciguse undo` | remove your last log from the past 24 hours |
| `ciguse report` | the last 14 days: working vs waiting, then uses per day |
| `ciguse doctor` | something looks off? start here |

Run `ciguse` for everything else: `export --csv`, `project alias`, `purge`, `setup claude --remove`.

## Good to know

- **`cig` only works while the agent waits on you.** While Claude Code is working, your message waits in a queue and skips the plugin, so use the hotkey or a terminal then.
- **Two logs within 90 seconds count as one,** in case of a double press. To log a second one, add `--force` (`cig --force` in Claude Code).
- **With several sessions open,** in Claude Code, Codex or both, time counts as waiting whenever any of them waits on you, and as working only while all of them work. A wait longer than 30 minutes counts as away.
- **The ratio needs data:** at least 10 uses while working and 10 while waiting. Until about 95 each, the report marks it early.
- **Your status line is safe.** `ciguse setup claude` changes only `statusLine` in `~/.claude/settings.json` and keeps a backup. If you had a status line, it keeps running, with the ciguse count after it. `ciguse setup claude --remove` puts it back.
- **After your first week** you can add `--since` (time since your last use) or `--ratio` to the `ciguse statusline` command in that file.

## Other ways to install

**macOS or Linux (script)**

```sh
curl -fsSL https://raw.githubusercontent.com/demetriusk/ciguse/main/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/demetriusk/ciguse/main/install.ps1 | iex
```

**Manual:** download the archive for your system from [Releases](https://github.com/demetriusk/ciguse/releases), check it against `checksums.txt`, and put `ciguse` on your PATH.

**From source:** `go install github.com/demetriusk/ciguse@latest`

**Claude Code plugin only**, without the status line: run this in Claude Code.

```text
/plugin install ciguse --marketplace demetriusk/ciguse
```

**Codex plugin by hand:** `codex plugin marketplace add demetriusk/ciguse`, then `codex plugin add ciguse@ciguse`, then `/hooks` in Codex to trust the hooks.

**For scripts,** `ciguse init` takes flags instead of asking: `ciguse init --product iqos --pack-price 6.00 --currency EUR --pack-size 20 --day-start 04:00`.

## Privacy and data

The plugins store, per agent session: a keyed session id, a keyed project path, the repository's folder name, and when the state changed. Prompts, tool input, file paths and transcripts are never stored. A `cig` prompt is checked in memory and dropped.

Everything lives in one folder, readable only by your user:

- macOS: `~/Library/Application Support/ciguse/`
- Linux: `$XDG_DATA_HOME/ciguse/` (default `~/.local/share/ciguse/`)
- Windows: `%LOCALAPPDATA%\ciguse\`

Set `CIGUSE_HOME` to use another folder. `ciguse purge` deletes it all.

## License

MIT
