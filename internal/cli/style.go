package cli

import "charm.land/lipgloss/v2"

// Colors are semantic roles mapped to the terminal's own 16-color palette, so they follow
// the user's light or dark theme without querying the terminal. Output that isn't a
// terminal, or runs with NO_COLOR, is stripped to plain text by the writer in Run.
var (
	colorAccent = lipgloss.Color("3") // ember: the lit tip, focus, today
	colorMuted  = lipgloss.Color("8") // smoke and ash: prefixes, hints
	colorFilter = lipgloss.Color("3") // the cigarette's filter

	accent = lipgloss.NewStyle().Foreground(colorAccent)
	muted  = lipgloss.NewStyle().Foreground(colorMuted)
	filter = lipgloss.NewStyle().Foreground(colorFilter)
	bold   = lipgloss.NewStyle().Bold(true)
)

// prefix starts every one-line confirmation.
var prefix = muted.Render("ciguse ·")

// mark is the lit-cigarette logo for the welcome screen and setup.
func mark() string {
	return muted.Render("              ) )") + "\n" +
		muted.Render("             ( (") + "\n" +
		"  " + filter.Render("▀▀▀▀") + "▀▀▀▀▀▀▀▀▀" + muted.Render("▒▒") + accent.Render("░") + "   " + bold.Render("ciguse") + "\n"
}
