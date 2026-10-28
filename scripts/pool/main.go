// Pool collects every `ciguse share` (or pasted `ciguse export --share`) reply in the data
// thread into one CSV on stdout, and prints the pooled working-vs-waiting ratio to stderr.
// Needs the GitHub CLI, signed in: go run ./scripts/pool > pooled.csv
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// threadID is the data thread, github.com/demetriusk/ciguse/discussions/1.
const threadID = "D_kwDOVZY6Es4AqmDo"

// header is the CSV header as ciguse export --share writes it.
var header = strings.Split("date,hours_working,hours_waiting,uses_working,uses_waiting,uses_away,nicotine_mg,hours_claude,uses_claude,hours_codex,uses_codex", ",")

var (
	blockRe   = regexp.MustCompile("(?s)```csv\\s*\\n(.*?)```")
	versionRe = regexp.MustCompile(`<sub>ciguse (\S+)</sub>`)
)

type reply struct {
	Login     string    `json:"login"`
	UpdatedAt time.Time `json:"updatedAt"`
	Body      string    `json:"body"`
}

// row is one person's day. fields are as posted; the numbers are parsed for the summary.
type row struct {
	person, version string
	fields          []string
	hours           [2]float64 // working, waiting
	uses            [3]int     // working, waiting, away
}

func main() {
	replies, err := fetch()
	if err != nil {
		log.Fatal(err)
	}
	rows, skipped := pool(replies)
	w := csv.NewWriter(os.Stdout)
	w.Write(append([]string{"person", "version"}, header...))
	for _, r := range rows {
		w.Write(append([]string{r.person, r.version}, r.fields...))
	}
	w.Flush()
	if err := w.Error(); err != nil {
		log.Fatal(err)
	}
	fmt.Fprint(os.Stderr, summary(rows, skipped))
}

// fetch reads every reply in the thread, and replies to replies, through gh.
func fetch() ([]reply, error) {
	out, err := exec.Command("gh", "api", "graphql", "--paginate", "-f", `query=query($endCursor: String) {
		node(id: "`+threadID+`") { ... on Discussion { comments(first: 50, after: $endCursor) {
			pageInfo { hasNextPage endCursor }
			nodes { author { login } updatedAt body replies(first: 50) { nodes { author { login } updatedAt body } } } } } } }`,
		"--jq", `.data.node.comments.nodes[] | (., .replies.nodes[]) | {login: (.author.login // "ghost"), updatedAt, body} | tojson`).Output()
	if err != nil {
		return nil, fmt.Errorf("gh: %w (is the GitHub CLI installed and signed in?)", err)
	}
	var rs []reply
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<24)
	for sc.Scan() {
		var r reply
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	return rs, sc.Err()
}

// pool turns replies into one row per person and day: a person's later reply wins for a
// day both have. skipped counts replies that gave no valid rows.
func pool(replies []reply) (rows []row, skipped int) {
	replies = slices.Clone(replies)
	slices.SortStableFunc(replies, func(a, b reply) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
	days := map[[2]string]row{}
	for _, rp := range replies {
		version := ""
		if m := versionRe.FindStringSubmatch(rp.Body); m != nil {
			version = m[1]
		}
		valid := 0
		for _, m := range blockRe.FindAllStringSubmatch(rp.Body, -1) {
			records, err := csv.NewReader(strings.NewReader(m[1])).ReadAll()
			if err != nil || len(records) == 0 || !slices.Equal(records[0], header) {
				continue
			}
			for _, rec := range records[1:] {
				if r, ok := parse(rec); ok {
					r.person, r.version = personID(rp.Login), version
					days[[2]string{r.person, rec[0]}] = r
					valid++
				}
			}
		}
		if valid == 0 {
			skipped++
		}
	}
	for _, r := range days {
		rows = append(rows, r)
	}
	slices.SortFunc(rows, func(a, b row) int {
		return strings.Compare(a.person+a.fields[0], b.person+b.fields[0])
	})
	return rows, skipped
}

func parse(rec []string) (row, bool) {
	r := row{fields: rec}
	if len(rec) != len(header) {
		return r, false
	}
	if _, err := time.Parse(time.DateOnly, rec[0]); err != nil {
		return r, false
	}
	for i, f := range rec[1:] {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil || v < 0 || v > 1e4 {
			return r, false
		}
		switch i {
		case 0, 1:
			r.hours[i] = v
		case 2, 3, 4:
			r.uses[i-2] = int(v)
		}
	}
	return r, true
}

// personID is a stable pseudonym, so the pooled file never carries GitHub logins.
func personID(login string) string {
	h := sha256.Sum256([]byte(login))
	return hex.EncodeToString(h[:4])
}

// summary pools everyone's hours and uses, with the report's rate ratio and its Poisson
// 95% CI, and counts the people who on their own use more while waiting.
func summary(rows []row, skipped int) string {
	var hours [2]float64
	var uses [3]int
	type tally struct {
		hours [2]float64
		uses  [3]int
	}
	people := map[string]*tally{}
	for _, r := range rows {
		p := people[r.person]
		if p == nil {
			p = &tally{}
			people[r.person] = p
		}
		for i := range 2 {
			hours[i] += r.hours[i]
			p.hours[i] += r.hours[i]
		}
		for i := range 3 {
			uses[i] += r.uses[i]
			p.uses[i] += r.uses[i]
		}
	}
	var b strings.Builder
	count := func(n int, one, many string) string {
		if n == 1 {
			return "1 " + one
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	fmt.Fprintf(&b, "pooled · %s · %s\n\n", count(len(people), "person", "people"), count(len(rows), "day", "days"))
	fmt.Fprintf(&b, "%-14s %7s %6s %10s\n", "", "hours", "uses", "per hour")
	for i, label := range []string{"working", "waiting"} {
		rate := math.NaN()
		if hours[i] > 0 {
			rate = float64(uses[i]) / hours[i]
		}
		fmt.Fprintf(&b, "%-14s %7.2f %6d %10.2f\n", label, hours[i], uses[i], rate)
	}
	fmt.Fprintf(&b, "%-14s %7s %6d %10s\n\n", "away", "–", uses[2], "–")

	if uses[0] < 10 || uses[1] < 10 || hours[0] == 0 || hours[1] == 0 {
		fmt.Fprintf(&b, "%-18s   too early: needs 10 uses in each state\n", "waiting vs working")
	} else {
		rr := (float64(uses[1]) / hours[1]) / (float64(uses[0]) / hours[0])
		se := math.Sqrt(1/float64(uses[0]) + 1/float64(uses[1]))
		fmt.Fprintf(&b, "%-18s %5.1f×  (95%% CI %.1f–%.1f)\n", "waiting vs working", rr, rr*math.Exp(-1.96*se), rr*math.Exp(1.96*se))
	}
	more, enough := 0, 0
	for _, p := range people {
		if p.uses[0] < 10 || p.uses[1] < 10 || p.hours[0] == 0 || p.hours[1] == 0 {
			continue
		}
		enough++
		if float64(p.uses[1])/p.hours[1] > float64(p.uses[0])/p.hours[0] {
			more++
		}
	}
	fmt.Fprintf(&b, "%-18s   %d of %d people with 10+ uses in each state\n", "more while waiting", more, enough)
	if skipped > 0 {
		fmt.Fprintf(&b, "%-18s   %d replies without a valid ciguse csv block\n", "skipped", skipped)
	}
	return b.String()
}
