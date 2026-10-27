package store

import (
	"testing"
	"time"
)

func agent(t *testing.T, s *Store, session, project string, at time.Time, state string) {
	t.Helper()
	if err := s.RecordAgent(AgentEvent{Session: session, Project: project, Alias: project, At: at, State: state, Cause: "test"}); err != nil {
		t.Fatal(err)
	}
}

func forceLog(t *testing.T, s *Store, at time.Time) {
	t.Helper()
	if _, err := s.Log(Use{At: at, Now: at, Source: "cli", Force: true}); err != nil {
		t.Fatal(err)
	}
}

func TestSplitAttributesUsesToTheStateTheyStartIn(t *testing.T) {
	s := open(t)
	m := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	agent(t, s, "s1", "api", m(0), Working)
	agent(t, s, "s1", "api", m(20), WaitingOnYou) // waits 60 min: 30 count, 30 are away
	agent(t, s, "s1", "api", m(80), Working)
	agent(t, s, "s1", "api", m(90), Ended)
	for _, n := range []int{5, 25, 60, 85, 100, -10} {
		forceLog(t, s, m(n))
	}

	sp, err := s.Split(m(-60), m(120))
	if err != nil {
		t.Fatal(err)
	}
	if sp.Sessions != 1 || sp.Hours[Working] != 30*time.Minute || sp.Hours[WaitingOnYou] != 30*time.Minute {
		t.Fatalf("sessions %d, hours %v", sp.Sessions, sp.Hours)
	}
	// 5 and 85 working; 25 waiting; 60 is away, 100 after the end, -10 before the start.
	if sp.Uses[Working] != 2 || sp.Uses[WaitingOnYou] != 1 || sp.Uses[Other] != 3 {
		t.Fatalf("uses %v", sp.Uses)
	}
}

// Acceptance test 5: with two projects open, a use is counted once, under no project.
// While one session waits on the user, the time counts as waiting, whatever the others do.
func TestSplitTwoProjects(t *testing.T) {
	s := open(t)
	m := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	agent(t, s, "s1", "api", m(0), Working)
	agent(t, s, "s2", "web", m(10), Working)
	agent(t, s, "s2", "web", m(20), WaitingOnYou)
	agent(t, s, "s1", "api", m(40), Ended)
	agent(t, s, "s2", "web", m(45), Ended)
	for _, n := range []int{5, 15, 30, 42} {
		forceLog(t, s, m(n))
	}

	sp, err := s.Split(m(0), m(60))
	if err != nil {
		t.Fatal(err)
	}
	// 5: api alone, working. 15: both working. 30: web waits while api works. 42: web alone, waiting.
	if sp.Uses[Working] != 2 || sp.Uses[WaitingOnYou] != 2 || sp.Uses[Other] != 0 {
		t.Fatalf("uses %v", sp.Uses)
	}
	if sp.Hours[Working] != 20*time.Minute || sp.Hours[WaitingOnYou] != 25*time.Minute {
		t.Fatalf("hours %v", sp.Hours)
	}
	got := map[string]ProjectSplit{}
	for _, p := range sp.Projects {
		got[p.Alias] = p
	}
	if got["api"].Uses != 1 || got["api"].Hours != 10*time.Minute || got["web"].Uses != 1 || got["web"].Hours != 5*time.Minute {
		t.Fatalf("projects %+v", sp.Projects)
	}
}

func TestSplitStaleSessionCloses(t *testing.T) {
	s := open(t)
	m := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	agent(t, s, "s1", "api", m(0), Working) // no hook after this: stale 30 min after it
	forceLog(t, s, m(20))
	forceLog(t, s, m(40))

	sp, err := s.Split(m(0), m(120))
	if err != nil {
		t.Fatal(err)
	}
	if sp.Hours[Working] != 30*time.Minute || sp.Uses[Working] != 1 || sp.Uses[Other] != 1 {
		t.Fatalf("hours %v, uses %v", sp.Hours, sp.Uses)
	}
}
