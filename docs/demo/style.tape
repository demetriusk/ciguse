# Shared look and a clean shell for every scene. record.sh sets DEMO_HOME and DEMO_BIN.
Set Shell "bash"
Set FontSize 17
Set Width 1100
Set Height 900
Set Padding 32
Set Framerate 25
Set Theme "Dracula"
Set WindowBar Colorful
Set TypingSpeed 40ms

Hide
Type `export HOME="$DEMO_HOME" CIGUSE_HOME="$DEMO_HOME/data" PATH="$DEMO_BIN:$PATH" PS1='$ '; cd "$HOME"; clear`
Enter
Show
