package cli

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/demetriusk/ciguse/internal/store"
)

// Guards from spec section 8: below tooEarly uses in either state there's no ratio at all,
// and below firm the ratio is labelled early.
const (
	tooEarly = 10
	firm     = 95
)

// rateRatio is uses per hour waiting over uses per hour working, with a Poisson 95% CI on
// the log scale. ok is false below the tooEarly guard.
func rateRatio(sp store.Split) (rr, lo, hi float64, ok bool) {
	a, b := sp.Uses[store.Working], sp.Uses[store.WaitingOnYou]
	if a < tooEarly || b < tooEarly {
		return 0, 0, 0, false
	}
	rr = (float64(b) / sp.Hours[store.WaitingOnYou].Hours()) / (float64(a) / sp.Hours[store.Working].Hours())
	se := math.Sqrt(1/float64(a) + 1/float64(b))
	return rr, rr * math.Exp(-1.96*se), rr * math.Exp(1.96*se), true
}

// printSplit prints the report's answer: uses per hour while Claude Code works versus
// while it waits on the user.
func printSplit(w io.Writer, sp store.Split) {
	if sp.Sessions == 0 {
		fmt.Fprintf(w, "%s\n%s\n\n", muted.Render("no Claude Code or Codex sessions in this range, so no working vs waiting split."),
			muted.Render("the plugins record them: ciguse setup claude, ciguse setup codex"))
		return
	}
	a, b := sp.Uses[store.Working], sp.Uses[store.WaitingOnYou]
	ha, hb := sp.Hours[store.Working], sp.Hours[store.WaitingOnYou]
	row := func(label string, h time.Duration, n int) {
		hours, rate := "–", "–"
		if h > 0 {
			hours, rate = fmt.Sprintf("%.1f", h.Hours()), fmt.Sprintf("%.2f", float64(n)/h.Hours())
		}
		fmt.Fprintf(w, "%-18s %8s %6d %10s\n", label, hours, n, rate)
	}
	fmt.Fprintln(w, muted.Render(fmt.Sprintf("%-18s %8s %6s %10s", "", "hours", "uses", "per hour")))
	row("agent working", ha, a)
	row("waiting on you", hb, b)
	row("no session / away", 0, sp.Uses[store.Other])
	fmt.Fprintln(w)

	if rr, lo, hi, ok := rateRatio(sp); !ok {
		fmt.Fprintf(w, "%-18s   %s\n\n", "waiting vs working", muted.Render(fmt.Sprintf("too early: needs %d uses in each state", tooEarly)))
	} else {
		fmt.Fprintf(w, "%-18s %s   %s\n", "waiting vs working", bold.Render(fmt.Sprintf("%8s", fmt.Sprintf("%.1f×", rr))),
			muted.Render(fmt.Sprintf("(95%% CI %.1f–%.1f)", lo, hi)))
		if a < firm || b < firm {
			fmt.Fprintln(w, muted.Render(fmt.Sprintf("early: %d uses across these states; ~%d for a firm read", a+b, 2*firm)))
		}
		fmt.Fprintln(w)
	}

	agents := make([]store.Group, len(sp.Agents))
	for i, g := range sp.Agents {
		g.Name = map[string]string{"claude": "Claude Code", "codex": "Codex", store.Both: "both open", store.Other: "no agent / away"}[g.Name]
		agents[i] = g
	}
	printGroups(w, "by agent", agents)
	if len(sp.Projects) > 1 {
		printGroups(w, "by project", sp.Projects)
	}
}

// printGroups prints a breakdown's rows as uses per hour, working and waiting together.
// Under 0.1 hours, the precision the hours column shows, a rate is noise: such a row gets
// none, and is left out if it has no uses either.
func printGroups(w io.Writer, title string, groups []store.Group) {
	groups = slices.DeleteFunc(slices.Clone(groups), func(g store.Group) bool { return g.Hours < 6*time.Minute && g.Uses == 0 })
	if len(groups) == 0 {
		return
	}
	width := 0
	for _, g := range groups {
		width = max(width, len(g.Name))
	}
	fmt.Fprintln(w, title+" "+muted.Render("(per hour, working + waiting)"))
	for _, g := range groups {
		rate := "   –"
		if g.Hours >= 6*time.Minute {
			rate = fmt.Sprintf("%.2f", float64(g.Uses)/g.Hours.Hours())
		}
		fmt.Fprintf(w, "  %s  %s · %d uses\n", g.Name+strings.Repeat(" ", width-len(g.Name)), rate, g.Uses)
	}
	fmt.Fprintln(w)
}
