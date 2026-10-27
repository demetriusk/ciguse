package cli

import (
	"fmt"
	"io"
	"math"
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
		fmt.Fprintf(w, "%s\n%s\n\n", muted.Render("no Claude Code sessions in this range, so no working vs waiting split."),
			muted.Render("the plugin records them: /plugin install ciguse --marketplace demetriusk/ciguse"))
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

	if len(sp.Projects) < 2 {
		return
	}
	width := 0
	for _, p := range sp.Projects {
		width = max(width, len(p.Alias))
	}
	fmt.Fprintln(w, "by project "+muted.Render("(per hour, working + waiting)"))
	for _, p := range sp.Projects {
		rate := "   –"
		if p.Hours > 0 {
			rate = fmt.Sprintf("%.2f", float64(p.Uses)/p.Hours.Hours())
		}
		fmt.Fprintf(w, "  %s  %s · %d uses\n", p.Alias+strings.Repeat(" ", width-len(p.Alias)), rate, p.Uses)
	}
	fmt.Fprintln(w)
}
