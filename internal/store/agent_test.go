package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func record(t *testing.T, s *Store, at time.Time, state, cause string, onlyOnChange bool) {
	t.Helper()
	err := s.RecordAgent(AgentEvent{Session: "s1", Project: "p1", Alias: "api", At: at, State: state, Cause: cause, OnlyOnChange: onlyOnChange})
	if err != nil {
		t.Fatal(err)
	}
}

// timeline lists s1's transitions as "minute:state" in time order.
func timeline(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.db.Query(`SELECT ts, state FROM transitions WHERE session_key = 's1' ORDER BY ts, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ts int64
		var state string
		if err := rows.Scan(&ts, &state); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d:%s", (ts-base.UnixMilli())/60000, state))
	}
	return strings.Join(out, " ")
}

func TestRecordAgentTurn(t *testing.T) {
	s := open(t)
	m := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	record(t, s, m(0), "", "SessionStart", false)
	record(t, s, m(1), Working, "UserPromptSubmit", false)
	record(t, s, m(2), Working, "PostToolUse", true) // already working: skipped
	record(t, s, m(3), WaitingOnYou, "PermissionRequest", false)
	record(t, s, m(4), Working, "PostToolUse", true) // permission granted
	record(t, s, m(5), WaitingOnYou, "Stop", false)
	record(t, s, m(6), Ended, "SessionEnd", false)

	if got, want := timeline(t, s), "1:working 3:waiting_on_you 4:working 5:waiting_on_you 6:ended"; got != want {
		t.Fatalf("timeline %q, want %q", got, want)
	}
	var started, lastSeen int64
	var alias string
	if err := s.db.QueryRow(`SELECT started_at, last_seen_at, alias FROM sessions JOIN projects USING (project_key)`).
		Scan(&started, &lastSeen, &alias); err != nil {
		t.Fatal(err)
	}
	if started != m(0).UnixMilli() || lastSeen != m(6).UnixMilli() || alias != "api" {
		t.Fatalf("session %d–%d alias %q", started, lastSeen, alias)
	}
}

func TestRecordAgentOutOfOrder(t *testing.T) {
	s := open(t)
	m := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	// Async hooks: Stop at minute 3 finishes writing before the last PostToolUse at minute 2.
	record(t, s, m(1), Working, "UserPromptSubmit", false)
	record(t, s, m(3), WaitingOnYou, "Stop", false)
	record(t, s, m(2), Working, "PostToolUse", true)

	if got, want := timeline(t, s), "1:working 3:waiting_on_you"; got != want {
		t.Fatalf("timeline %q, want %q", got, want)
	}
	var started, lastSeen int64
	if err := s.db.QueryRow(`SELECT started_at, last_seen_at FROM sessions`).Scan(&started, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if started != m(1).UnixMilli() || lastSeen != m(3).UnixMilli() {
		t.Fatalf("session %d–%d", started, lastSeen)
	}
}

func TestRecordAgentFirstSeenMidTurn(t *testing.T) {
	s := open(t)
	// ciguse installed while the agent was already working: the first PostToolUse still records.
	record(t, s, base, Working, "PostToolUse", true)
	if got := timeline(t, s); got != "0:working" {
		t.Fatalf("timeline %q", got)
	}
}
