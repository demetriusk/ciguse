package store

import (
	"slices"
	"time"
)

// Away is how long a session can wait on the user, or go without any hook event, before
// the rest of that time counts as away: the user has left, so it's excluded.
const Away = 30 * time.Minute

// Split divides a time range and the uses in it by what Claude Code was doing.
type Split struct {
	Sessions int                      // sessions seen in the range; 0 means no hook ever ran
	Hours    map[string]time.Duration // Working and WaitingOnYou
	Uses     map[string]int           // Working, WaitingOnYou, and Other: no session or away
	Projects []ProjectSplit           // time and uses while exactly one project was open
}

type ProjectSplit struct {
	Alias string
	Hours time.Duration
	Uses  int
}

// Other collects uses outside a single clear agent state.
const Other = "other"

type segment struct {
	from, to int64 // epoch ms, to exclusive
	state    string
	project  string
}

// Split works out the agent state for every moment in [from, to) and for each use in it.
// A use counts toward the state it started in, not the time it lasted.
func (s *Store) Split(from, to time.Time) (Split, error) {
	sp := Split{Hours: map[string]time.Duration{}, Uses: map[string]int{}}
	f, t := from.UnixMilli(), to.UnixMilli()
	rows, err := s.db.Query(`SELECT s.session_key, s.last_seen_at, p.alias, tr.ts, tr.state
		FROM sessions s JOIN projects p USING (project_key) JOIN transitions tr USING (session_key)
		WHERE s.last_seen_at >= ? AND s.started_at < ?
		ORDER BY s.session_key, tr.ts, tr.id`, f-Away.Milliseconds(), t)
	if err != nil {
		return sp, err
	}
	var segs []segment
	var prev struct {
		session, alias, state string
		ts, lastSeen          int64
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
			segs = append(segs, segment{a, b, prev.state, prev.alias})
		}
	}
	for rows.Next() {
		var session, alias, state string
		var lastSeen, ts int64
		if err := rows.Scan(&session, &lastSeen, &alias, &ts, &state); err != nil {
			rows.Close()
			return sp, err
		}
		if session == prev.session {
			flush(ts)
		} else {
			flush(prev.lastSeen + Away.Milliseconds()) // a session's last state lasts until it goes stale
			sp.Sessions++
		}
		prev.session, prev.alias, prev.state, prev.ts, prev.lastSeen = session, alias, state, ts, lastSeen
	}
	flush(prev.lastSeen + Away.Milliseconds())
	rows.Close()
	if err := rows.Err(); err != nil {
		return sp, err
	}

	projects := map[string]*ProjectSplit{}
	project := func(alias string) *ProjectSplit {
		if projects[alias] == nil {
			projects[alias] = &ProjectSplit{Alias: alias}
		}
		return projects[alias]
	}

	// Between consecutive boundaries the set of open sessions doesn't change.
	cuts := []int64{f, t}
	for _, sg := range segs {
		cuts = append(cuts, sg.from, sg.to)
	}
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)
	for i := 0; i+1 < len(cuts); i++ {
		state, alias := at(segs, cuts[i])
		d := time.Duration(cuts[i+1]-cuts[i]) * time.Millisecond
		if state == Other {
			continue
		}
		sp.Hours[state] += d
		if alias != "" {
			project(alias).Hours += d
		}
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
		state, alias := at(segs, ts)
		sp.Uses[state]++
		if state != Other && alias != "" {
			project(alias).Uses++
		}
	}
	for _, p := range projects {
		sp.Projects = append(sp.Projects, *p)
	}
	slices.SortFunc(sp.Projects, func(a, b ProjectSplit) int { return b.Uses - a.Uses })
	return sp, ev.Err()
}

// at is the combined state at ts. Any open session waiting on the user makes it
// WaitingOnYou: with agents in parallel, the user is idle only while every one of them
// works. No open session is Other. alias is set when exactly one project is open.
func at(segs []segment, ts int64) (state, alias string) {
	multi := false
	for _, sg := range segs {
		if sg.from > ts || ts >= sg.to {
			continue
		}
		if state == "" {
			alias = sg.project
		}
		if state != WaitingOnYou {
			state = sg.state
		}
		if sg.project != alias {
			multi = true
		}
	}
	if state == "" {
		state = Other
	}
	if multi {
		alias = ""
	}
	return state, alias
}
