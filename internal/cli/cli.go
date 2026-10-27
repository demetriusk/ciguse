// Package cli implements the ciguse commands.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/demetriusk/ciguse/internal/store"
)

// now is replaced in tests.
var now = time.Now

const usage = `ciguse: nicotine use alongside your coding agent

  ciguse use [product] [--ago 5m] [--force]  log one use
  ciguse undo                                remove the last use logged in the past 24h
  ciguse report [--days 14]                  uses per day
  ciguse init [flags]                        default product, pack price, day start
  ciguse version

products: cigarette, iqos, glo, stick
`

// Run executes ciguse with args (program name excluded) and returns the exit code.
func Run(args []string, version string, stdout, stderr io.Writer) int {
	if len(args) == 0 || slices.Contains([]string{"help", "-h", "--help"}, args[0]) {
		fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "use":
		return runUse(args[1:], stdout, stderr)
	case "undo":
		return runUndo(args[1:], stdout, stderr)
	case "report":
		return runReport(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "version", "--version":
		fmt.Fprintln(stdout, "ciguse", version)
		return 0
	}
	fmt.Fprintf(stderr, "ciguse: unknown command %q\n\n%s", args[0], usage)
	return 2
}

func runUse(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("use", stderr)
	ago := fs.Duration("ago", 0, "how long ago it happened, e.g. 5m (max 12h)")
	force := fs.Bool("force", false, "log even if a use was logged in the last 90s")
	pos, ok := parse(fs, args, 1)
	if !ok {
		return 2
	}
	if *ago < 0 || *ago > 12*time.Hour {
		fmt.Fprintln(stderr, "ciguse: --ago must be between 0 and 12h")
		return 2
	}
	s, code := openStore(stderr)
	if s == nil {
		return code
	}
	defer s.Close()

	t := now()
	l, err := s.Log(store.Use{Product: first(pos), At: t.Add(-*ago), Now: t, Source: "cli", Force: *force})
	if errors.Is(err, store.ErrUnknownProduct) {
		fmt.Fprintf(stderr, "ciguse: %v (products: cigarette, iqos, glo, stick)\n", err)
		return 2
	}
	if err != nil {
		return fail(stderr, err)
	}
	if l.Duplicate {
		fmt.Fprintf(stdout, "ciguse · already logged one at %s, skipped (--force logs anyway)\n", l.Previous.Format("15:04"))
		return 0
	}
	fmt.Fprintf(stdout, "ciguse · %s logged %s · %d today\n", l.Product, l.At.Format("15:04"), l.Today)
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
		fmt.Fprintln(stdout, "ciguse · nothing logged in the past 24h to undo")
		return 0
	}
	fmt.Fprintf(stdout, "ciguse · removed %s %s · %d today\n", u.Product, u.At.Format("15:04"), u.Today)
	return 0
}

func runReport(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("report", stderr)
	days := fs.Int("days", 14, "number of days, today included")
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
		fmt.Fprintln(stdout, "ciguse · nothing logged yet. Log a use with: ciguse use")
		return 0
	}
	fmt.Fprintf(stdout, "ciguse · last %d days · %d uses · %.1f a day\n\n", *days, r.Uses, float64(r.Uses)/float64(r.Active))
	for i, d := range r.Days {
		line := fmt.Sprintf("%s  %3d  %s", d.Date.Format("Mon 02 Jan"), d.Uses, strings.Repeat("█", min(d.Uses, 40)))
		if i == len(r.Days)-1 {
			line += "  today"
		}
		fmt.Fprintln(stdout, strings.TrimRight(line, " "))
	}
	currencies := make([]string, 0, len(r.Value))
	for c := range r.Value {
		currencies = append(currencies, c)
	}
	slices.Sort(currencies)
	for i, c := range currencies {
		if i == 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "value  %s %s", r.Value[c].FloatString(2), c)
		if i == len(currencies)-1 && r.Priced < r.Uses {
			fmt.Fprintf(stdout, "  (%d of %d uses priced)", r.Priced, r.Uses)
		}
		fmt.Fprintln(stdout)
	}
	return 0
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("init", stderr)
	product := fs.String("product", "", "default product: cigarette, iqos, glo, stick")
	price := fs.String("pack-price", "", "pack price, e.g. 5.00 (applies to --product, or the default product)")
	size := fs.Int("pack-size", 20, "sticks per pack")
	currency := fs.String("currency", "", "currency code for --pack-price, e.g. EUR")
	dayStart := fs.String("day-start", "", "local time a new day starts, e.g. 04:00")
	if _, ok := parse(fs, args, 0); !ok {
		return 2
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
	if *price != "" {
		target := *product
		if target == "" {
			st, err := s.Settings()
			if err != nil {
				return fail(stderr, err)
			}
			target = st.DefaultProduct
		}
		if err := s.SetPrice(target, priceMinor, int64(*size), strings.ToUpper(*currency), now()); err != nil {
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
	fmt.Fprintln(stdout, "ciguse · ready")
	fmt.Fprintf(stdout, "  default     %s\n", st.DefaultProduct)
	priced := false
	for _, p := range products {
		if !p.PackPriceMinor.Valid {
			continue
		}
		label := "  price       "
		if priced {
			label = "              "
		}
		priced = true
		fmt.Fprintf(stdout, "%s%s %s %s / %d\n", label, p.ID, formatMinor(p.PackPriceMinor.Int64), p.Currency.String, p.PackSize.Int64)
	}
	if !priced {
		fmt.Fprintln(stdout, "  price       not set (optional: --pack-price 5.00 --currency EUR)")
	}
	fmt.Fprintf(stdout, "  day starts  %02d:%02d\n", int(st.DayStart.Hours()), int(st.DayStart.Minutes())%60)
	dir, _ := dataDir()
	fmt.Fprintf(stdout, "  data        %s\n", filepath.Join(dir, "ciguse.db"))
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
	s, err := store.Open(filepath.Join(dir, "ciguse.db"))
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

// parseClock turns "04:00" into the duration since midnight.
func parseClock(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a time like 04:00", s)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}
