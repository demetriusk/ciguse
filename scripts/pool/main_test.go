package main

import (
	"strings"
	"testing"
	"time"
)

func TestPool(t *testing.T) {
	at := func(day int) time.Time { return time.Date(2026, 11, day, 12, 0, 0, 0, time.UTC) }
	block := func(rows ...string) string {
		return "```csv\n" + strings.Join(header, ",") + "\n" + strings.Join(rows, "\n") + "\n```\n"
	}
	replies := []reply{
		// ann shared twice: the later reply wins for the day both have.
		{Login: "ann", UpdatedAt: at(3), Body: "<!-- ciguse share -->\n" + block(
			"2026-11-01,2.00,1.00,10,10,1,12.6,3.00,20,0.00,0",
			"2026-11-02,2.00,1.00,8,12,0,12.0,2.00,14,1.00,6") + "<sub>ciguse 0.9.0</sub>\n"},
		{Login: "ann", UpdatedAt: at(2), Body: block("2026-11-01,1.00,1.00,1,1,0,1.2,2.00,2,0.00,0")},
		// bob pasted by hand, without the marker or a version.
		{Login: "bob", UpdatedAt: at(4), Body: "here's mine\n\n" + block("2026-11-01,4.00,0.00,5,0,3,8.0,4.00,5,0.00,0")},
		{Login: "cy", UpdatedAt: at(4), Body: "nice tool!"},
		{Login: "dee", UpdatedAt: at(4), Body: "```csv\ndate,uses\n2026-11-01,3\n```"},
		{Login: "eve", UpdatedAt: at(4), Body: block("2026-11-01,x,1.00,1,1,0,1.2,2.00,2,0.00,0")},
	}
	rows, skipped := pool(replies)
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = r.person + " " + r.version + " " + strings.Join(r.fields, ",")
	}
	ann, bob := personID("ann"), personID("bob")
	want := []string{
		ann + " 0.9.0 2026-11-01,2.00,1.00,10,10,1,12.6,3.00,20,0.00,0",
		ann + " 0.9.0 2026-11-02,2.00,1.00,8,12,0,12.0,2.00,14,1.00,6",
		bob + "  2026-11-01,4.00,0.00,5,0,3,8.0,4.00,5,0.00,0",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") || skipped != 3 {
		t.Fatalf("skipped %d, rows:\n%s", skipped, strings.Join(got, "\n"))
	}

	sum := summary(rows, skipped)
	for _, line := range []string{
		"pooled · 2 people · 3 days",
		"working           8.00     23       2.88",
		"waiting           2.00     22      11.00",
		"away                 –      4          –",
		"waiting vs working   3.8×  (95% CI 2.1–6.9)",
		"more while waiting   1 of 1 people with 10+ uses in each state",
		"skipped              3 replies without a valid ciguse csv block",
	} {
		if !strings.Contains(sum, line) {
			t.Fatalf("missing %q in:\n%s", line, sum)
		}
	}
}
