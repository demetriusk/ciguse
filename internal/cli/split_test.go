package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/demetriusk/ciguse/internal/store"
)

func TestReportSplitsWorkingAndWaiting(t *testing.T) {
	dir, clock := setup(t)
	s, err := store.Open(filepath.Join(dir, "ciguse.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	at := func(h, m int) time.Time { return time.Date(2026, 10, 27, h, m, 0, 0, time.Local) }
	for _, e := range []store.AgentEvent{
		{At: at(10, 0), State: store.Working}, {At: at(11, 0), State: store.WaitingOnYou}, {At: at(11, 30), State: store.Ended},
	} {
		e.Session, e.Project, e.Alias, e.Cause = "s1", "p1", "api", "test"
		if err := s.RecordAgent(e); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 12 {
		for _, ts := range []time.Time{at(10, i*5), at(11, i*2)} {
			if _, err := s.Log(store.Use{At: ts, Now: ts, Source: "cli", Force: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.Close()
	*clock = at(12, 41)

	expect(t, []string{"report", "--days", "1"}, 0, "ciguse · last 1 days · 24 uses · 24.0 a day\n\n"+
		"                      hours   uses   per hour\n"+
		"agent working           1.0     12      12.00\n"+
		"waiting on you          0.5     12      24.00\n"+
		"no session / away         –      0          –\n\n"+
		"waiting vs working     2.0×   (95% CI 0.9–4.5)\n"+
		"early: 24 uses across these states; ~190 for a firm read\n\n"+
		"by agent (per hour, working + waiting)\n"+
		"  Claude Code  16.00 · 24 uses\n\n"+
		"Tue 27 Oct   24  ▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆  today\n")
}
