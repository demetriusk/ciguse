package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Agent states recorded from Claude Code hooks.
const (
	Working      = "working"
	WaitingOnYou = "waiting_on_you"
	Ended        = "ended"
)

// AgentEvent is one hook call. Session and project arrive as keys, never as raw ids or paths.
type AgentEvent struct {
	Session string    // keyed session id
	Agent   string    // claude or codex
	Project string    // keyed project root
	Alias   string    // project folder name, shown in reports
	At      time.Time // when the hook process started; async hooks can finish out of order
	State   string    // the state from At on, or "" to only mark the session as seen
	Cause   string    // hook event name
	// OnlyOnChange skips the transition when the session is already in State at At.
	// Used for frequent hooks; rare ones always record, so out-of-order writes can't drop them.
	OnlyOnChange bool
}

// RecordAgent stores a hook call: the project and session it belongs to, and any state change.
func (s *Store) RecordAgent(e AgentEvent) error {
	at := e.At.UnixMilli()
	return s.immediate(func(ctx context.Context, c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, `INSERT INTO projects (project_key, alias, created_at) VALUES (?, ?, ?)
			ON CONFLICT DO NOTHING`, e.Project, e.Alias, at); err != nil {
			return err
		}
		agent := e.Agent
		if agent == "" {
			agent = "claude"
		}
		if _, err := c.ExecContext(ctx, `INSERT INTO sessions (session_key, project_key, started_at, last_seen_at, agent)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (session_key) DO UPDATE SET
				started_at = min(started_at, excluded.started_at),
				last_seen_at = max(last_seen_at, excluded.last_seen_at)`, e.Session, e.Project, at, at, agent); err != nil {
			return err
		}
		if e.State == "" {
			return nil
		}
		if e.OnlyOnChange {
			var state string
			err := c.QueryRowContext(ctx, `SELECT state FROM transitions WHERE session_key = ? AND ts <= ?
				ORDER BY ts DESC, id DESC LIMIT 1`, e.Session, at).Scan(&state)
			if err == nil && state == e.State {
				return nil
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		_, err := c.ExecContext(ctx, `INSERT INTO transitions (session_key, ts, state, cause) VALUES (?, ?, ?, ?)`,
			e.Session, at, e.State, e.Cause)
		return err
	})
}

// SessionState returns a session's latest recorded state, or "" when it has none.
func (s *Store) SessionState(session string) (string, error) {
	var state string
	err := s.db.QueryRow(`SELECT state FROM transitions WHERE session_key = ? ORDER BY ts DESC, id DESC LIMIT 1`,
		session).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return state, err
}
