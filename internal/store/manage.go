package store

import (
	"database/sql"
	"errors"
	"time"
)

// SetAlias renames a project in reports. ok is false when no session was seen there.
func (s *Store) SetAlias(projectKey, alias string) (ok bool, err error) {
	r, err := s.db.Exec(`UPDATE projects SET alias = ? WHERE project_key = ?`, alias, projectKey)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// Event is one stored use, as export writes it.
type Event struct {
	ID             int64
	At, LoggedAt   time.Time // At in the UTC offset of when it was logged
	Product        string
	PackPriceMinor sql.NullInt64
	PackSize       sql.NullInt64
	Currency       sql.NullString
	Source         string
	Deleted        sql.NullTime // set by undo
}

// Events returns every use, undone ones included, oldest first.
func (s *Store) Events() ([]Event, error) {
	rows, err := s.db.Query(`SELECT id, ts, logged_at, utc_offset_min, product_id, pack_price_minor, pack_size,
		currency, source, deleted_at FROM events ORDER BY ts, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var es []Event
	for rows.Next() {
		var e Event
		var ts, logged int64
		var offset int
		var deleted sql.NullInt64
		if err := rows.Scan(&e.ID, &ts, &logged, &offset, &e.Product, &e.PackPriceMinor, &e.PackSize,
			&e.Currency, &e.Source, &deleted); err != nil {
			return nil, err
		}
		e.At, e.LoggedAt = eventTime(ts, offset), eventTime(logged, offset)
		if deleted.Valid {
			e.Deleted = sql.NullTime{Time: eventTime(deleted.Int64, offset), Valid: true}
		}
		es = append(es, e)
	}
	return es, rows.Err()
}

// Health is what doctor reports about the stored data.
type Health struct {
	Uses, Sessions int
	LastHook       time.Time // last state change any hook recorded; zero when none
	LastCause      string
}

func (s *Store) Health() (Health, error) {
	var h Health
	if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM events WHERE deleted_at IS NULL), (SELECT count(*) FROM sessions)`).
		Scan(&h.Uses, &h.Sessions); err != nil {
		return h, err
	}
	var ts int64
	err := s.db.QueryRow(`SELECT ts, cause FROM transitions ORDER BY ts DESC, id DESC LIMIT 1`).Scan(&ts, &h.LastCause)
	if errors.Is(err, sql.ErrNoRows) {
		return h, nil
	}
	h.LastHook = time.UnixMilli(ts)
	return h, err
}
