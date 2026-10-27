package store

import (
	"database/sql"
	"errors"
	"time"
)

// Today counts today's uses, with the day starting at the configured time.
func (s *Store) Today(now time.Time) (int, error) {
	st, err := s.Settings()
	if err != nil {
		return 0, err
	}
	return s.countDay(now, st.DayStart)
}

// Span returns the first and last use; ok is false when nothing is logged.
func (s *Store) Span() (first, last time.Time, ok bool, err error) {
	var f, l sql.NullInt64
	err = s.db.QueryRow(`SELECT min(ts), max(ts) FROM events WHERE deleted_at IS NULL`).Scan(&f, &l)
	if err != nil || !f.Valid {
		return first, last, false, err
	}
	return time.UnixMilli(f.Int64), time.UnixMilli(l.Int64), true, nil
}

// Config returns a free-form setting, or "" when it isn't set.
func (s *Store) Config(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetConfig stores a free-form setting; an empty value removes it.
func (s *Store) SetConfig(key, value string) error {
	if value == "" {
		_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
