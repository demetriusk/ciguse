package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// runHotkey prints how to bind a global key to `ciguse use --notify` on this OS. ciguse
// runs no daemon, so the key belongs to the OS or a tool the user already has.
func runHotkey(args []string, stdout, stderr io.Writer) int {
	if _, ok := parse(newFlags("hotkey", stderr), args, 0); !ok {
		return 2
	}
	// The PATH entry, such as Homebrew's link, keeps working across upgrades.
	bin, err := exec.LookPath("ciguse")
	if err != nil {
		if bin, err = os.Executable(); err != nil {
			return fail(stderr, err)
		}
	}
	fmt.Fprint(stdout, hotkeyGuide(runtime.GOOS, bin))
	return 0
}

func hotkeyGuide(goos, bin string) string {
	cmd := accent.Render(bin + " use --notify")
	switch goos {
	case "darwin":
		return prefix + " a global key on macOS\n\n" +
			"  1  Open Shortcuts and make a new shortcut with one action: Run Shell Script\n" +
			"  2  Paste this as the script:\n" +
			"       " + cmd + "\n" +
			"  3  In the shortcut's details, Add Keyboard Shortcut, e.g. ⌃⌥C\n" +
			"  4  If Shortcuts asks, allow running scripts in Settings › Advanced\n\n" +
			"  " + muted.Render("with skhd instead:") + "  ctrl + alt - c : " + bin + " use --notify\n"
	case "windows":
		return prefix + " a global key on Windows\n\n" +
			"  1  Open PowerToys › Keyboard Manager › Remap a shortcut\n" +
			"  2  Add a shortcut, e.g. Ctrl+Alt+C, with the action Run Program\n" +
			"  3  Program: " + accent.Render(bin) + "\n" +
			"     Arguments: " + accent.Render("use --notify") + "\n" +
			"     Visibility: Hidden, so no window flashes\n\n" +
			"  " + muted.Render("with AutoHotkey v2 instead:") + "  ^!c::Run('\"" + bin + "\" use --notify', , 'Hide')\n"
	}
	return prefix + " a global key on Linux\n\n" +
		"  GNOME  Settings › Keyboard › Custom Shortcuts, add this command:\n" +
		"           " + cmd + "\n" +
		"  KDE    System Settings › Shortcuts › Add New › Command or Script\n" +
		"  Sway   bindsym Ctrl+Mod1+c exec " + bin + " use --notify\n" +
		"  Hyprland  bind = CTRL ALT, C, exec, " + bin + " use --notify\n\n" +
		"  " + muted.Render("Notifications use notify-send (package libnotify-bin or libnotify).") + "\n"
}

// notify shows text as a desktop notification titled ciguse, with what the OS already
// has. Replaced in tests.
var notify = func(text string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// The text goes in as an argument, never into the script, so nothing in it runs.
		c = exec.Command("osascript", "-e", "on run argv", "-e",
			`display notification (item 1 of argv) with title "ciguse"`, "-e", "end run", text)
	case "windows":
		c = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", windowsToast)
		c.Env = append(os.Environ(), "CIGUSE_NOTIFY="+text)
		hideWindow(c)
	default:
		c = exec.Command("notify-send", "--app-name=ciguse", "--", "ciguse", text)
	}
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("%v %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// windowsToast shows $env:CIGUSE_NOTIFY as a toast from Windows PowerShell 5.1, which every
// Windows 10 and 11 has, under PowerShell's own app id since ciguse has none registered.
const windowsToast = `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$t = $x.GetElementsByTagName('text')
$t.Item(0).AppendChild($x.CreateTextNode('ciguse')) | Out-Null
$t.Item(1).AppendChild($x.CreateTextNode($env:CIGUSE_NOTIFY)) | Out-Null
$app = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($app).Show([Windows.UI.Notifications.ToastNotification]::new($x))`
