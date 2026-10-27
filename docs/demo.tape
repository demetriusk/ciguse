# Records docs/demo.gif for the README: install, set up, log, report.
# Uses the real install script and release, in a throwaway home folder.
# Run from the repository root: vhs docs/demo.tape
Output docs/demo.gif

Set Shell "bash"
Set FontSize 18
Set Width 1100
Set Height 900
Set Padding 28
Set Theme "Dracula"
Set WindowBar Colorful
Set TypingSpeed 45ms

Hide
Type `unset CIGUSE_HOME; export HOME="$(mktemp -d)"; export PATH="$HOME/.local/bin:$PATH" PS1='$ '; cd "$HOME" && clear`
Enter
Show

Type "curl -fsSL https://raw.githubusercontent.com/demetriusk/ciguse/main/install.sh | sh"
Sleep 500ms
Enter
Wait+Screen@30s /installed to/
Sleep 2s

Hide
Type "clear"
Enter
Show

Type "ciguse init"
Sleep 300ms
Enter
Sleep 1.5s
Type "2"
Sleep 500ms
Enter
Sleep 900ms
Type "6.50"
Sleep 500ms
Enter
Sleep 900ms
Enter
Sleep 700ms
Enter
Sleep 700ms
Enter
Sleep 3s

Hide
Type "clear"
Enter
Show

Type "ciguse use --ago 40m"
Sleep 300ms
Enter
Sleep 1.2s
Type "ciguse use"
Sleep 300ms
Enter
Sleep 1.5s
Type "ciguse report --days 7"
Sleep 300ms
Enter
Sleep 5s
