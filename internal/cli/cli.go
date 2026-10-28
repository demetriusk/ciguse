// Package cli implements the ciguse commands.
package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/demetriusk/ciguse/internal/store"
)

// now is replaced in tests.
var now = time.Now

const tagline = "nicotine use alongside your coding agent"

const commands = `  ciguse init             set up: what you use, pack price, day end
  ciguse use [product]    log one use  (--ago 5m, --force)
  ciguse undo             remove the last use from the past 24h
  ciguse report           working vs waiting, uses per day  (--days 14)
  ciguse hotkey           set up a global key that logs a use
  ciguse setup claude     plugin, and today's count in the status line
  ciguse setup codex      plugin for Codex
  ciguse project alias    rename this folder's project in reports
  ciguse export           every use as JSON lines  (--csv, --share)
  ciguse share            post your day totals to the ciguse data thread
  ciguse doctor           check the setup, show plugin errors
  ciguse purge            delete all data
  ciguse version

products: cigarette, iqos, glo, stick, pouch
`

const usage = "ciguse: " + tagline + "\n\n" + commands

// Run executes ciguse with args (program name excluded) and returns the exit code.
func Run(args []string, version string, stdout, stderr io.Writer) int {
	// Hooks print nothing, or JSON for Claude Code, so they skip terminal detection entirely.
	if len(args) > 0 && args[0] == "hook" {
		return runHook(args[1:], stdout)
	}
	// The status line runs inside Claude Code, which renders colors from a pipe.
	if len(args) > 0 && args[0] == "statusline" {
		return runStatusline(args[1:], stdout, stderr)
	}
	if f, ok := stdout.(*os.File); ok {
		lipgloss.EnableLegacyWindowsANSI(f)
	}
	// Strips color when the output isn't a terminal or NO_COLOR is set.
	stdout = colorprofile.NewWriter(stdout, os.Environ())
	stderr = colorprofile.NewWriter(stderr, os.Environ())

	if len(args) == 0 || slices.Contains([]string{"help", "-h", "--help"}, args[0]) {
		fmt.Fprint(stdout, mark()+"\n  "+muted.Render(tagline)+"\n\n"+commands)
		return 0
	}
	switch args[0] {
	case "use":
		return runUse(args[1:], "cli", stdout, stderr)
	case "undo":
		return runUndo(args[1:], stdout, stderr)
	case "report":
		return runReport(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "hotkey":
		return runHotkey(args[1:], stdout, stderr)
	case "setup":
		return runSetup(args[1:], stdout, stderr)
	case "project":
		return runProject(args[1:], stdout, stderr)
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "share":
		return runShare(args[1:], version, stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], version, stdout, stderr)
	case "purge":
		return runPurge(args[1:], stdout, stderr)
	case "version", "--version":
		fmt.Fprintln(stdout, "ciguse", version)
		return 0
	}
	fmt.Fprintf(stderr, "ciguse: unknown command %q\n\n%s", args[0], usage)
	return 2
}

// runUse logs one use. source records where it came from: cli, hotkey (--notify), or
// prompt for cig.
func runUse(args []string, source string, stdout, stderr io.Writer) int {
	fs := newFlags("use", stderr)
	ago := fs.Duration("ago", 0, "how long ago it happened, e.g. 5m (max 12h)")
	force := fs.Bool("force", false, "log even if a use was logged in the last 90s")
	notifyFlag := fs.Bool("notify", false, "also show the result as a notification, for a global hotkey")
	pos, ok := parse(fs, args, 1)
	if !ok {
		return 2
	}
	if *ago < 0 || *ago > 12*time.Hour {
		fmt.Fprintln(stderr, "ciguse: --ago must be between 0 and 12h")
		return 2
	}
	// A hotkey runs with no terminal in sight, so whatever is printed also becomes a
	// notification, once the database is closed.
	if *notifyFlag {
		source = "hotkey"
		var note bytes.Buffer
		plain := colorprofile.NewWriter(&note, nil)
		errOut := stderr
		stdout, stderr = io.MultiWriter(stdout, plain), io.MultiWriter(stderr, plain)
		defer func() {
			text := strings.TrimSpace(note.String())
			text = strings.TrimPrefix(strings.TrimPrefix(text, "ciguse · "), "ciguse: ")
			if err := notify(text); err != nil {
				fmt.Fprintf(errOut, "ciguse: couldn't show a notification: %v\n", err)
			}
		}()
	}
	s, code := openStore(stderr)
	if s == nil {
		return code
	}
	defer s.Close()

	t := now()
	l, err := s.Log(store.Use{Product: first(pos), At: t.Add(-*ago), Now: t, Source: source, Force: *force})
	if errors.Is(err, store.ErrUnknownProduct) {
		fmt.Fprintf(stderr, "ciguse: %v (products: cigarette, iqos, glo, stick, pouch)\n", err)
		return 2
	}
	if err != nil {
		return fail(stderr, err)
	}
	if l.Duplicate {
		fmt.Fprintf(stdout, "%s already logged one at %s, skipped %s\n", prefix, l.Previous.Format("15:04"), muted.Render("(--force logs anyway)"))
		return 0
	}
	line := fmt.Sprintf("%s %s logged %s · %s", prefix, bold.Render(l.Product), l.At.Format("15:04"), bold.Render(fmt.Sprintf("%d today", l.Today)))
	// Codex and the hotkey have no ciguse status line, so the confirmation points at the
	// report too. The use is saved either way, so a failed check only skips the pointer.
	if ready, err := ratioReady(s, t); err == nil && ready {
		line += muted.Render(" · ") + ratioReadyText
	}
	fmt.Fprintln(stdout, line)
	return 0
}

func runUndo(args []string, stdout, stderr io.Writer) int {
	if _, ok := parse(newFlags("undo", stderr), args, 0); !ok {
		return 2
	}
	s, code := openStore(stderr)
	if s == nil {
		return code
	}
	defer s.Close()

	u, err := s.Undo(now())
	if err != nil {
		return fail(stderr, err)
	}
	if !u.OK {
		fmt.Fprintln(stdout, prefix, "nothing logged in the past 24h to undo")
		return 0
	}
	fmt.Fprintf(stdout, "%s removed %s %s · %s\n", prefix, u.Product, u.At.Format("15:04"), bold.Render(fmt.Sprintf("%d today", u.Today)))
	return 0
}

func runReport(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("report", stderr)
	days := fs.Int("days", 14, "number of days, today included")
	brief := fs.Bool("brief", false, "a few lines to screenshot or paste: the split and nicotine")
	if _, ok := parse(fs, args, 0); !ok {
		return 2
	}
	if *days < 1 || *days > 366 {
		fmt.Fprintln(stderr, "ciguse: --days must be between 1 and 366")
		return 2
	}
	s, code := openStore(stderr)
	if s == nil {
		return code
	}
	defer s.Close()

	r, err := s.Report(now(), *days)
	if err != nil {
		return fail(stderr, err)
	}
	if r.Active == 0 {
		fmt.Fprintln(stdout, prefix, "nothing logged yet. Log a use with: ciguse use")
		return 0
	}
	// Days before the first use weren't tracked, so they're left out rather than shown as 0.
	tracked := r.Days[len(r.Days)-r.Active:]
	span := fmt.Sprintf("%d days", len(tracked))
	if len(tracked) == 1 {
		span = "1 day"
	}
	fmt.Fprintf(stdout, "%s last %s · %d uses · %.1f a day\n", prefix, span, r.Uses, float64(r.Uses)/float64(r.Active))
	if !*brief {
		fmt.Fprintln(stdout)
	}
	st, err := s.Settings()
	if err != nil {
		return fail(stderr, err)
	}
	d := tracked[0].Date
	sp, err := s.Split(time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location()).Add(st.DayStart), now())
	if err != nil {
		return fail(stderr, err)
	}
	_, _, _, ratioOK := rateRatio(sp)
	if ratioOK {
		if err := s.SetConfig(cfgRatioSeen, "1"); err != nil { // the status line stops pointing here
			return fail(stderr, err)
		}
	}
	if *brief {
		printBrief(stdout, sp)
		if len(r.Nicotine) > 0 {
			fmt.Fprintf(stdout, "%s   %s\n", muted.Render(fmt.Sprintf("%-18s", "nicotine")), nicotineADay(r))
		}
		return 0
	}
	printSplit(stdout, sp)
	for i, d := range tracked {
		line := fmt.Sprintf("%s  %3d", d.Date.Format("Mon 02 Jan"), d.Uses)
		if d.Uses > 0 {
			line += "  " + accent.Render(strings.Repeat("▆", min(d.Uses, 40)))
		}
		if i == len(tracked)-1 {
			if d.Uses == 0 {
				line += "  "
			}
			line += "  " + accent.Render("today")
		}
		fmt.Fprintln(stdout, line)
	}
	currencies := make([]string, 0, len(r.Value))
	for c := range r.Value {
		currencies = append(currencies, c)
	}
	slices.Sort(currencies)
	if len(currencies) > 0 || len(r.Nicotine) > 0 {
		fmt.Fprintln(stdout)
	}
	for i, c := range currencies {
		fmt.Fprintf(stdout, "%s  %s %s", muted.Render("value   "), r.Value[c].FloatString(2), c)
		if i == len(currencies)-1 && r.Priced < r.Uses {
			fmt.Fprint(stdout, muted.Render(fmt.Sprintf("  (%d of %d uses priced)", r.Priced, r.Uses)))
		}
		fmt.Fprintln(stdout)
	}
	if len(r.Nicotine) > 0 {
		each := make([]string, len(r.Nicotine))
		for i, d := range r.Nicotine {
			each[i] = d.Product + " " + formatMG(d.UG)
		}
		fmt.Fprintf(stdout, "%s  %s\n", muted.Render("nicotine"), nicotineADay(r))
		if r.WeeksTracked {
			fmt.Fprintln(stdout, muted.Render(fmt.Sprintf("          last 7 full days ≈%s mg a day · the 7 before ≈%s",
				roughMG(r.Weeks[0]/7), roughMG(r.Weeks[1]/7))))
		}
		fmt.Fprintln(stdout, muted.Render("          estimate: "+strings.Join(each, ", ")+" mg a use · a pack a day ≈ 20 mg"))
	}
	// Two weeks of data is what the data thread asks for, so point there until the user shares.
	if first, _, ok, err := s.Span(); err == nil && ok && ratioOK && now().Sub(first) >= 14*24*time.Hour {
		if shared, err := s.Config(cfgShared); err == nil && shared == "" {
			fmt.Fprintf(stdout, "\n%s%s%s\n", muted.Render("two weeks in: "), accent.Render("ciguse share"), muted.Render(" adds your day totals to the data thread"))
		}
	}
	return 0
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("init", stderr)
	product := fs.String("product", "", "default product: cigarette, iqos, glo, stick, pouch")
	price := fs.String("pack-price", "", "pack price, e.g. 5.00 (applies to --product, or the default product)")
	size := fs.Int("pack-size", 20, "units per pack or can")
	currency := fs.String("currency", "", "currency code for --pack-price, e.g. EUR")
	nicotine := fs.String("nicotine", "", "estimated mg of nicotine a use, e.g. 0.6 (applies to --product, or the default product)")
	dayStart := fs.String("day-start", "", "local time a new day starts, e.g. 04:00")
	if _, ok := parse(fs, args, 0); !ok {
		return 2
	}
	// No flags in a terminal: ask instead, filling the same variables.
	asked := fs.NFlag() == 0 && canAsk()
	if asked {
		s, code := openStore(stderr)
		if s == nil {
			return code
		}
		fmt.Fprintln(stdout, mark())
		err := askSetup(s, stdout, product, price, currency, size, dayStart)
		s.Close()
		if errors.Is(err, errSetupCancelled) {
			fmt.Fprintln(stdout, prefix, "setup cancelled, nothing saved")
			return 1
		}
		if err != nil {
			return fail(stderr, err)
		}
	}
	var priceMinor int64
	var dayStartDur time.Duration
	var err error
	if *price != "" {
		if priceMinor, err = parseMoney(*price); err != nil {
			fmt.Fprintf(stderr, "ciguse: --pack-price: %v\n", err)
			return 2
		}
		if !currencyRe.MatchString(*currency) {
			fmt.Fprintln(stderr, "ciguse: --pack-price needs --currency, a 3-letter code such as EUR")
			return 2
		}
		if *size < 1 || *size > 1000 {
			fmt.Fprintln(stderr, "ciguse: --pack-size must be between 1 and 1000")
			return 2
		}
	}
	var nicotineUG int64
	if *nicotine != "" {
		if nicotineUG, err = parseMG(*nicotine); err != nil {
			fmt.Fprintf(stderr, "ciguse: --nicotine: %v\n", err)
			return 2
		}
	}
	if *dayStart != "" {
		if dayStartDur, err = parseClock(*dayStart); err != nil {
			fmt.Fprintf(stderr, "ciguse: --day-start: %v\n", err)
			return 2
		}
	}

	s, code := openStore(stderr)
	if s == nil {
		return code
	}
	defer s.Close()

	if *product != "" {
		if err := s.SetDefaultProduct(*product); err != nil {
			return fail(stderr, err)
		}
	}
	target := *product
	if target == "" && (*price != "" || *nicotine != "") {
		st, err := s.Settings()
		if err != nil {
			return fail(stderr, err)
		}
		target = st.DefaultProduct
	}
	if *price != "" {
		if err := s.SetPrice(target, priceMinor, int64(*size), strings.ToUpper(*currency), now()); err != nil {
			return fail(stderr, err)
		}
	}
	if *nicotine != "" {
		if err := s.SetNicotine(target, nicotineUG); err != nil {
			return fail(stderr, err)
		}
	}
	if *dayStart != "" {
		if err := s.SetDayStart(dayStartDur); err != nil {
			return fail(stderr, err)
		}
	}

	st, err := s.Settings()
	if err != nil {
		return fail(stderr, err)
	}
	products, err := s.Products()
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, prefix, "ready")
	fmt.Fprintf(stdout, "  %s     %s\n", muted.Render("default"), bold.Render(st.DefaultProduct))
	priced := false
	for _, p := range products {
		if !p.PackPriceMinor.Valid {
			continue
		}
		label := "  " + muted.Render("price") + "       "
		if priced {
			label = "              "
		}
		priced = true
		fmt.Fprintf(stdout, "%s%s %s %s / %d\n", label, p.ID, formatMinor(p.PackPriceMinor.Int64), p.Currency.String, p.PackSize.Int64)
	}
	if !priced {
		fmt.Fprintf(stdout, "  %s       not set %s\n", muted.Render("price"), muted.Render("(optional: --pack-price 5.00 --currency EUR)"))
	}
	for _, p := range products {
		if p.ID == st.DefaultProduct && p.NicotineUG.Valid {
			hint := "(--nicotine to change)"
			if p.ID != "cigarette" {
				hint = "(a cigarette ≈1 mg; --nicotine to change)"
			}
			fmt.Fprintf(stdout, "  %s    %s ≈%s mg a use %s\n", muted.Render("nicotine"), p.ID, formatMG(p.NicotineUG.Int64), muted.Render(hint))
		}
	}
	fmt.Fprintf(stdout, "  %s  %02d:%02d\n", muted.Render("day starts"), int(st.DayStart.Hours()), int(st.DayStart.Minutes())%60)
	dir, _ := dataDir()
	fmt.Fprintf(stdout, "  %s        %s\n", muted.Render("data"), muted.Render(tildePath(filepath.Join(dir, "ciguse.db"))))
	if asked {
		fmt.Fprintf(stdout, "\n%s\n  %s       %s\n  %s     %s\n", bold.Render("next"),
			muted.Render("log a use"), accent.Render("ciguse use"),
			muted.Render("Claude Code"), accent.Render("ciguse setup claude"))
	}
	return 0
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("ciguse "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parse accepts flags before and after positional arguments, up to max positionals.
func parse(fs *flag.FlagSet, args []string, max int) ([]string, bool) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, false
		}
		if fs.NArg() == 0 {
			return pos, true
		}
		pos = append(pos, fs.Arg(0))
		if len(pos) > max {
			fmt.Fprintf(fs.Output(), "%s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
			return nil, false
		}
		args = fs.Args()[1:]
	}
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func openStore(stderr io.Writer) (*store.Store, int) {
	dir, err := dataDir()
	if err != nil {
		return nil, fail(stderr, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fail(stderr, err)
	}
	s, err := store.Open(filepath.Join(dir, "ciguse.db"), 2*time.Second)
	if err != nil {
		return nil, fail(stderr, err)
	}
	return s, 0
}

// dataDir is $CIGUSE_HOME, or the platform's per-user data directory.
func dataDir() (string, error) {
	if d := os.Getenv("CIGUSE_HOME"); d != "" {
		return d, nil
	}
	switch runtime.GOOS {
	case "windows":
		d, err := os.UserCacheDir() // %LOCALAPPDATA%
		return filepath.Join(d, "ciguse"), err
	case "darwin":
		d, err := os.UserHomeDir()
		return filepath.Join(d, "Library", "Application Support", "ciguse"), err
	}
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "ciguse"), nil
	}
	d, err := os.UserHomeDir()
	return filepath.Join(d, ".local", "share", "ciguse"), err
}

// tildePath shows a path under the home directory as ~/..., the way people type it.
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if rel, err := filepath.Rel(home, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "ciguse: %v\n", err)
	return 1
}

var (
	moneyRe    = regexp.MustCompile(`^(\d{1,7})(?:[.,](\d{1,2}))?$`)
	currencyRe = regexp.MustCompile(`^[A-Za-z]{3}$`)
)

// parseMoney turns "5", "5.5" or "5,50" into minor units (500, 550, 550).
func parseMoney(s string) (int64, error) {
	m := moneyRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("%q is not a price like 5.00", s)
	}
	whole, _ := strconv.ParseInt(m[1], 10, 64)
	frac, _ := strconv.ParseInt((m[2] + "00")[:2], 10, 64)
	return whole*100 + frac, nil
}

func formatMinor(v int64) string {
	return fmt.Sprintf("%d.%02d", v/100, v%100)
}

// parseMG turns "0.6" or "0,6" mg into micrograms.
func parseMG(s string) (int64, error) {
	f, err := strconv.ParseFloat(strings.Replace(strings.TrimSpace(s), ",", ".", 1), 64)
	if err != nil || !(f >= 0 && f <= 50) {
		return 0, fmt.Errorf("%q is not mg a use from 0 to 50, like 0.6", s)
	}
	return int64(math.Round(f * 1000)), nil
}

func formatMG(ug int64) string {
	return strconv.FormatFloat(float64(ug)/1000, 'f', -1, 64)
}

// nicotineADay is the report's estimated nicotine a day, "≈7 mg a day, like 7 cigarettes".
func nicotineADay(r store.Report) string {
	var ug int64
	for _, d := range r.Nicotine {
		ug += d.UG * int64(d.Uses)
	}
	day := roughMG(ug / int64(r.Active))
	// A cigarette gives about 1 mg, so mg reads as cigarettes; it would only repeat a cigarette count.
	if !slices.ContainsFunc(r.Nicotine, func(d store.Dose) bool { return d.Product != "cigarette" }) {
		return "≈" + day + " mg a day"
	}
	if day == "1" {
		return "≈1 mg a day, like 1 cigarette"
	}
	return "≈" + day + " mg a day, like " + day + " cigarettes"
}

// roughMG rounds an estimate in micrograms: tenths of a mg below 10, whole mg above, and
// no ".0", which would look more precise than an estimate is.
func roughMG(ug int64) string {
	if t := (ug + 50) / 100; t < 100 && t%10 != 0 {
		return fmt.Sprintf("%d.%d", t/10, t%10)
	}
	return strconv.FormatInt((ug+500)/1000, 10)
}

// parseClock turns "04:00" into the duration since midnight.
func parseClock(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a time like 04:00", s)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}
