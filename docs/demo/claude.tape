Source docs/demo/agent-style.tape

Hide
Type `unset $(env | cut -d= -f1 | grep -E '^(ORCA_|CLAUDE)'); export CIGUSE_HOME="$DEMO_DATA" PS1='$ '; cd "$DEMO_PROJECT"; clear; claude`
Enter
Sleep 9s
Show
Sleep 1.5s
Type "cig"
Sleep 500ms
Enter
Sleep 3s
# Claude Code puts a blocked prompt back in the input; the status line refreshes within 15 s.
Hide
Backspace 3
Sleep 15s
Show
Sleep 3s
Hide
Ctrl+C
Ctrl+C
Sleep 1s
