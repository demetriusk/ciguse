package store

import (
	"slices"
	"time"
)

// Away is how long a session can wait on the user, or go without any hook event, before
// the rest of that time counts as away: the user has left, so it's excluded.
const Away = 30 * time.Minute

// Split divides a time range and the uses in it by what the agents were doing.
type Split struct {
	Sessions int                      // sessions seen in the range; 0 means no hook ever ran
	Hours    map[string]time.Duration // Working and WaitingOnYou
	Uses     map[string]int           // Working, WaitingOnYou, and Other: no session or away
	Projects []Group                  // time and uses while exactly one project was open
	Agents   []Group                  // per agent, Both while both had sessions open, then Other
}

// Group is one row of a breakdown: a project or an agent.
type Group struct {
	Name  string
	Hours time.Duration
	Uses  int
}

// Other collects uses outside a single clear agent state.
const Other = "other"

// Both names the time when Claude Code and Codex sessions were open together.
const Both = "both"

type segment struct {
	from, to int64 // epoch ms, to exclusive
	state    string
	project  string
	agent    string
}

// Split works out the agent state for every moment in [from, to) and for each use in it.
// A use counts toward the state it started in, not the time it lasted.
func (s *Store) Split(from, to time.Time) (Split, error) {
	sp := Split{Hours: map[string]time.Duration{}, Uses: map[string]int{}}
	f, t := from.UnixMilli(), to.UnixMilli()
	rows, err := s.db.Query(`SELECT s.session_key, s.last_seen_at, s.agent, p.alias, tr.ts, tr.state
		FROM sessions s JOIN projects p USING (project_key) JOIN transitions tr USING (session_key)
		WHERE s.last_seen_at >= ? AND s.started_at < ?
		ORDER BY s.session_key, tr.ts, tr.id`, f-Away.Milliseconds(), t)
	if err != nil {
		return sp, err
	}
	var segs []segment
	var prev struct {
		session, agent, alias, state string
		ts, lastSeen                 int64
	}
	// flush closes the previous transition's span at end.
	flush := func(end int64) {
		if prev.session == "" || prev.state == Ended {
			return
		}
		if prev.state == WaitingOnYou {
			end = min(end, prev.ts+Away.Milliseconds())
		}
		a, b := max(prev.ts, f), min(end, t)
		if a < b {
			segs = append(segs, segment{a, b, prev.state, prev.alias, prev.agent})
		}
	}
	for rows.Next() {
		var session, agent, alias, state string
		var lastSeen, ts int64
		if err := rows.Scan(&session, &lastSeen, &agent, &alias, &ts, &state); err != nil {
			rows.Close()
			return sp, err
		}
		if session == prev.session {
			flush(ts)
		} else {
			flush(prev.lastSeen + Away.Milliseconds()) // a session's last state lasts until it goes stale
			sp.Sessions++
		}
		prev.session, prev.agent, prev.alias, prev.state, prev.ts, prev.lastSeen = session, agent, alias, state, ts, lastSeen
	}
	flush(prev.lastSeen + Away.Milliseconds())
	rows.Close()
	if err := rows.Err(); err != nil {
		return sp, err
	}

	projects, agents := map[string]*Group{}, map[string]*Group{}
	group := func(m map[string]*Group, name string) *Group {
		if m[name] == nil {
			m[name] = &Group{Name: name}
		}
		return m[name]
	}

	// Between consecutive boundaries the set of open sessions doesn't change.
	cuts := []int64{f, t}
	for _, sg := range segs {
		cuts = append(cuts, sg.from, sg.to)
	}
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)
	for i := 0; i+1 < len(cuts); i++ {
		state, alias, agent := at(segs, cuts[i])
		d := time.Duration(cuts[i+1]-cuts[i]) * time.Millisecond
		if state == Other {
			continue
		}
		sp.Hours[state] += d
		if alias != "" {
			group(projects, alias).Hours += d
		}
		group(agents, agent).Hours += d
	}

	ev, err := s.db.Query(`SELECT ts FROM events WHERE deleted_at IS NULL AND ts >= ? AND ts < ?`, f, t)
	if err != nil {
		return sp, err
	}
	defer ev.Close()
	for ev.Next() {
		var ts int64
		if err := ev.Scan(&ts); err != nil {
			return sp, err
		}
		state, alias, agent := at(segs, ts)
		sp.Uses[state]++
		if state == Other {
			continue
		}
		if alias != "" {
			group(projects, alias).Uses++
		}
		group(agents, agent).Uses++
	}
	for _, g := range projects {
		sp.Projects = append(sp.Projects, *g)
	}
	slices.SortFunc(sp.Projects, func(a, b Group) int { return b.Uses - a.Uses })
	// Agents in a fixed order, so the rows don't swap places from one report to the next.
	for _, name := range []string{"claude", "codex", Both} {
		if g := agents[name]; g != nil {
			sp.Agents = append(sp.Agents, *g)
		}
	}
	// Uses outside any agent session are part of the picture too; they have no hours.
	if n := sp.Uses[Other]; n > 0 {
		sp.Agents = append(sp.Agents, Group{Name: Other, Uses: n})
	}
	return sp, ev.Err()
}

// at is the combined state at ts. Any open session waiting on the user makes it
// WaitingOnYou: with agents in parallel, the user is idle only while every one of them
// works. No open session is Other. alias is set when exactly one project is open, and
// agent names the one agent with open sessions, or Both.
func at(segs []segment, ts int64) (state, alias, agent string) {
	multi := false
	for _, sg := range segs {
		if sg.from > ts || ts >= sg.to {
			continue
		}
		if state == "" {
			alias, agent = sg.project, sg.agent
		}
		if state != WaitingOnYou {
			state = sg.state
		}
		if sg.project != alias {
			multi = true
		}
		if sg.agent != agent {
			agent = Both
		}
	}
	if state == "" {
		state = Other
	}
	if multi {
		alias = ""
	}
	return state, alias, agent
}
