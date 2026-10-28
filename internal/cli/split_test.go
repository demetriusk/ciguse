package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/demetriusk/ciguse/internal/store"
)

// seedSession records a Claude Code session on 27 Oct that works 10:00–11:00 and waits on
// the user 11:00–11:30, with working uses every 5 minutes from 10:00 and waiting uses every
// 2 minutes from 11:00.
func seedSession(t *testing.T, dir string, working, waiting int) {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, "ciguse.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := func(h, m int) time.Time { return time.Date(2026, 10, 27, h, m, 0, 0, time.Local) }
	for _, e := range []store.AgentEvent{
		{At: at(10, 0), State: store.Working}, {At: at(11, 0), State: store.WaitingOnYou}, {At: at(11, 30), State: store.Ended},
	} {
		e.Session, e.Project, e.Alias, e.Cause = "s1", "p1", "api", "test"
		if err := s.RecordAgent(e); err != nil {
			t.Fatal(err)
		}
	}
	var uses []time.Time
	for i := range working {
		uses = append(uses, at(10, i*5))
	}
	for i := range waiting {
		uses = append(uses, at(11, i*2))
	}
	for _, ts := range uses {
		if _, err := s.Log(store.Use{At: ts, Now: ts, Source: "cli", Force: true}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReportSplitsWorkingAndWaiting(t *testing.T) {
	dir, _ := setup(t)
	seedSession(t, dir, 12, 12)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 27, h, m, 0, 0, time.Local) }

	head := "ciguse · last 1 day · 24 uses · 24.0 a day\n\n" +
		"                      hours   uses   per hour\n" +
		"agent working           1.0     12      12.00\n" +
		"waiting on you          0.5     12      24.00\n" +
		"no session / away         –      0          –\n\n" +
		"waiting vs working     2.0×   (95% CI 0.9–4.5)\n" +
		"early: 24 uses across these states; ~190 for a firm read\n\n" +
		"by agent (per hour, working + waiting)\n"
	bars := "\nTue 27 Oct   24  ▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆  today\n\n" +
		"nicotine  ≈24 mg a day\n" +
		"          estimate: cigarette 1 mg a use · a pack a day ≈ 20 mg\n"
	expect(t, []string{"report", "--days", "1"}, 0, head+"  Claude Code  16.00 · 24 uses\n"+bars)

	// Codex sessions open for a few minutes alongside Claude Code: under 0.1 hours a rate is
	// noise, so the row has none, and without uses it's left out.
	codex := func(session string, from, to time.Time) {
		s, err := store.Open(filepath.Join(dir, "ciguse.db"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		for _, e := range []store.AgentEvent{{At: from, State: store.Working}, {At: to, State: store.Ended}} {
			e.Session, e.Project, e.Alias, e.Cause, e.Agent = session, "p1", "api", "test", "codex"
			if err := s.RecordAgent(e); err != nil {
				t.Fatal(err)
			}
		}
	}
	codex("c1", at(10, 11), at(10, 13)) // between the uses at 10:10 and 10:15
	expect(t, []string{"report", "--days", "1"}, 0, head+"  Claude Code  16.36 · 24 uses\n"+bars)
	codex("c2", at(11, 10), at(11, 13)) // the uses at 11:10 and 11:12
	expect(t, []string{"report", "--days", "1"}, 0, head+"  Claude Code  15.53 · 22 uses\n  both open       – · 2 uses\n"+bars)
}

func TestReportBrief(t *testing.T) {
	dir, _ := setup(t)
	seedSession(t, dir, 3, 12)
	early := "waiting vs working   too early: 3 of 10 while working\n"
	out, _ := run(t, "report", "--days", "1")
	contains(t, out, "\n"+early+"\n")
	expect(t, []string{"report", "--brief"}, 0, "ciguse · last 1 day · 15 uses · 15.0 a day\n"+
		"agent working        3.00 an hour\n"+
		"waiting on you       24.00 an hour\n"+
		early+
		"nicotine             ≈15 mg a day\n")

	seedSession(t, dir, 9, 0) // 12 while working now
	expect(t, []string{"report", "--brief"}, 0, "ciguse · last 1 day · 24 uses · 24.0 a day\n"+
		"agent working        12.00 an hour\n"+
		"waiting on you       24.00 an hour\n"+
		"waiting vs working   2.0×  (95% CI 0.9–4.5)\n"+
		"nicotine             ≈24 mg a day\n")
}

// Once the first week is over and both states have enough uses, the status line says the
// ratio is ready until a report has shown it. --ratio shows the ratio itself instead.
func TestStatuslineRatioReady(t *testing.T) {
	dir, clock := setup(t)
	t.Setenv("NO_COLOR", "1")
	today := *clock
	*clock = today.AddDate(0, 0, -8)
	run(t, "use")
	*clock = today
	seedSession(t, dir, 12, 3)
	expect(t, []string{"statusline"}, 0, "ciguse · 15 today\n")
	seedSession(t, dir, 0, 9)
	expect(t, []string{"statusline"}, 0, "ciguse · 24 today · ratio ready: ciguse report\n")
	expect(t, []string{"statusline", "--ratio"}, 0, "ciguse · 24 today · waiting 2.0× working\n")
	// Codex and the hotkey have no ciguse status line, so a logged use says it too.
	expect(t, []string{"use"}, 0, "ciguse · cigarette logged 12:41 · 25 today · ratio ready: ciguse report\n")
	run(t, "report")
	expect(t, []string{"statusline"}, 0, "ciguse · 25 today\n")
	*clock = today.Add(2 * time.Minute)
	expect(t, []string{"use"}, 0, "ciguse · cigarette logged 12:43 · 26 today\n")
}
