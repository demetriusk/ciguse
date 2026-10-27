package store

import (
	"fmt"
	"path/filepath"
	"strings"
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
	got := map[string]Group{}
	for _, p := range sp.Projects {
		got[p.Name] = p
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

func TestSplitByAgent(t *testing.T) {
	s := open(t)
	m := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	rec := func(session, agent string, at time.Time, state string) {
		t.Helper()
		if err := s.RecordAgent(AgentEvent{Session: session, Agent: agent, Project: "p", Alias: "p", At: at, State: state, Cause: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	rec("s1", "claude", m(0), Working)
	rec("s2", "codex", m(10), Working)
	rec("s1", "claude", m(20), Ended)
	rec("s2", "codex", m(30), Ended)
	for _, n := range []int{5, 15, 25, 40} {
		forceLog(t, s, m(n))
	}

	sp, err := s.Split(m(0), m(60))
	if err != nil {
		t.Fatal(err)
	}
	// 5: Claude Code alone. 15: both open. 25: Codex alone. 40: no agent.
	want := []Group{{"claude", 10 * time.Minute, 1}, {"codex", 10 * time.Minute, 1}, {Both, 10 * time.Minute, 1}, {Other, 0, 1}}
	if fmt.Sprint(sp.Agents) != fmt.Sprint(want) {
		t.Fatalf("agents\n got %v\nwant %v", sp.Agents, want)
	}
}

// Sessions recorded before the agent column existed are Claude Code, except those with
// Codex causes.
func TestMigrationBackfillsCodexSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ciguse.db")
	s, err := Open(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []AgentEvent{
		{Session: "c", Project: "p", Alias: "p", At: base, State: Working, Cause: "UserPromptSubmit"},
		{Session: "x", Project: "p", Alias: "p", At: base, State: Working, Cause: "codex UserPromptSubmit"},
	} {
		if err := s.RecordAgent(e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE sessions DROP COLUMN agent; PRAGMA user_version = 3`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.db.Query(`SELECT session_key || ':' || agent FROM sessions ORDER BY session_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var v string
		rows.Scan(&v)
		got = append(got, v)
	}
	if strings.Join(got, " ") != "c:claude x:codex" {
		t.Fatalf("agents %q", got)
	}
}
