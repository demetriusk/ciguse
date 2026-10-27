package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/demetriusk/ciguse/internal/store"
)

// canAsk reports whether init can ask questions instead of reading flags. Replaced in tests.
var canAsk = func() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

var errSetupCancelled = errors.New("setup cancelled")

// chat asks questions as plain lines, so questions and answers stay on screen like a
// chat transcript, in any terminal and with screen readers.
type chat struct {
	in  *bufio.Reader // one reader for every question, so typed-ahead lines aren't lost
	out io.Writer
}

// ask prints the question, then reads answers until check accepts one. An empty answer
// keeps current. check returns the value to store, or an error to show before asking again.
func (c chat) ask(question, hint, current string, check func(string) (string, error)) (string, error) {
	fmt.Fprintf(c.out, "\n  %s", bold.Render(question))
	if hint != "" {
		fmt.Fprintf(c.out, "  %s", muted.Render(hint))
	}
	fmt.Fprintln(c.out)
	for {
		fmt.Fprint(c.out, "  "+accent.Render("›")+" ")
		if current != "" {
			fmt.Fprint(c.out, muted.Render("["+current+"] "))
		}
		line, err := c.in.ReadString('\n')
		if err != nil && line == "" {
			return "", errSetupCancelled // input closed
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			answer = current
		}
		v, cerr := check(answer)
		if cerr == nil {
			return v, nil
		}
		fmt.Fprintf(c.out, "  %s\n", accent.Render(cerr.Error()))
	}
}

// askSetup asks init's questions, prefilled with what's stored now. Answers land in the
// variables the flags fill, so both paths share the same checks and saving.
func askSetup(s *store.Store, out io.Writer, product, price, currency *string, size *int, dayStart *string) error {
	st, err := s.Settings()
	if err != nil {
		return err
	}
	products, err := s.Products()
	if err != nil {
		return err
	}
	*product = st.DefaultProduct
	*dayStart = fmt.Sprintf("%02d:%02d", int(st.DayStart.Hours()), int(st.DayStart.Minutes())%60)
	*currency = "EUR"
	sizeText := strconv.Itoa(*size)
	var ids, choices []string
	for i, p := range products {
		ids = append(ids, p.ID)
		choices = append(choices, accent.Render(strconv.Itoa(i+1))+" "+p.ID)
		if p.ID == st.DefaultProduct && p.PackPriceMinor.Valid {
			*price = formatMinor(p.PackPriceMinor.Int64)
			*currency = p.Currency.String
			sizeText = strconv.FormatInt(p.PackSize.Int64, 10)
		}
	}

	c := chat{bufio.NewReader(stdin), out}
	fmt.Fprintln(out, "  "+muted.Render("A few questions. Enter keeps the value in brackets."))

	*product, err = c.ask("What do you smoke?", strings.Join(choices, "  "), *product, func(v string) (string, error) {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(ids) {
			return ids[n-1], nil
		}
		if v = strings.ToLower(v); slices.Contains(ids, v) {
			return v, nil
		}
		return "", fmt.Errorf("pick 1 to %d, or type its name", len(ids))
	})
	if err != nil {
		return err
	}
	*price, err = c.ask("What does a pack cost?", "optional, only used to show what you spend", *price, func(v string) (string, error) {
		if v == "" {
			return "", nil
		}
		_, err := parseMoney(v)
		return v, err
	})
	if err != nil {
		return err
	}
	if *price != "" {
		*currency, err = c.ask("Currency?", "", *currency, func(v string) (string, error) {
			if !currencyRe.MatchString(v) {
				return "", errors.New("a 3-letter code, such as EUR")
			}
			return strings.ToUpper(v), nil
		})
		if err != nil {
			return err
		}
		sizeText, err = c.ask("How many in a pack?", "", sizeText, func(v string) (string, error) {
			if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 1000 {
				return "", errors.New("a number from 1 to 1000")
			}
			return v, nil
		})
		if err != nil {
			return err
		}
		*size, _ = strconv.Atoi(sizeText)
	}
	*dayStart, err = c.ask("When does your day end?", "use before this counts toward the day before", *dayStart, func(v string) (string, error) {
		_, err := parseClock(v)
		return v, err
	})
	fmt.Fprintln(out)
	return err
}
