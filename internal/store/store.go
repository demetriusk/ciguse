// Package store keeps ciguse data in a local SQLite file.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// DuplicateWindow is how close two logs can be before the second counts as an accidental repeat.
const DuplicateWindow = 90 * time.Second

// UndoWindow is how far back undo reaches.
const UndoWindow = 24 * time.Hour

var ErrUnknownProduct = errors.New("unknown product")

// migrations[i] moves the schema from user_version i to i+1.
var migrations = []string{`
CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
INSERT INTO settings (key, value) VALUES ('default_product', 'cigarette'), ('day_start_min', '240');

CREATE TABLE products (
	id               TEXT PRIMARY KEY,
	label            TEXT NOT NULL,
	pack_price_minor INTEGER,
	pack_size        INTEGER,
	currency         TEXT,
	updated_at       INTEGER
);
INSERT INTO products (id, label) VALUES
	('cigarette', 'Cigarette'), ('iqos', 'IQOS'), ('glo', 'Glo'), ('stick', 'Other stick');

CREATE TABLE events (
	id               INTEGER PRIMARY KEY,
	ts               INTEGER NOT NULL,
	logged_at        INTEGER NOT NULL,
	utc_offset_min   INTEGER NOT NULL,
	product_id       TEXT NOT NULL REFERENCES products(id),
	pack_price_minor INTEGER,
	pack_size        INTEGER,
	currency         TEXT,
	source           TEXT NOT NULL,
	deleted_at       INTEGER
);
CREATE INDEX events_ts ON events(ts) WHERE deleted_at IS NULL;
`, `
CREATE TABLE projects (
	project_key TEXT PRIMARY KEY,
	alias       TEXT NOT NULL,
	created_at  INTEGER NOT NULL
);

CREATE TABLE sessions (
	session_key  TEXT PRIMARY KEY,
	project_key  TEXT NOT NULL REFERENCES projects(project_key),
	started_at   INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL
);

CREATE TABLE transitions (
	id          INTEGER PRIMARY KEY,
	session_key TEXT NOT NULL REFERENCES sessions(session_key),
	ts          INTEGER NOT NULL,
	state       TEXT NOT NULL,
	cause       TEXT NOT NULL
);
CREATE INDEX transitions_session_ts ON transitions(session_key, ts);
`, `
INSERT INTO products (id, label) VALUES ('pouch', 'Nicotine pouch');
`, `
ALTER TABLE sessions ADD COLUMN agent TEXT NOT NULL DEFAULT 'claude';
UPDATE sessions SET agent = 'codex'
	WHERE session_key IN (SELECT session_key FROM transitions WHERE cause LIKE 'codex %');
`}

type Store struct{ db *sql.DB }

// Open opens or creates the database at path and brings its schema up to date.
// busy is how long to wait for another process's lock before giving up.
func Open(path string, busy time.Duration) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection per process: pragmas apply to it and BEGIN IMMEDIATE stays on it.
	db.SetMaxOpenConns(1)
	s := &Store{db}
	if err := s.setup(busy); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) setup(busy time.Duration) error {
	for _, p := range []string{fmt.Sprintf("PRAGMA busy_timeout = %d", busy.Milliseconds()), "PRAGMA foreign_keys = ON"} {
		if _, err := s.db.Exec(p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	// Switching a new file to WAL can return SQLITE_BUSY without consulting the busy
	// timeout when several processes create the database at once, so retry briefly.
	for deadline := time.Now().Add(busy); ; time.Sleep(10 * time.Millisecond) {
		_, err := s.db.Exec("PRAGMA journal_mode = WAL")
		if err == nil {
			break
		}
		var se *sqlite.Error
		if !errors.As(err, &se) || se.Code() != sqlite3.SQLITE_BUSY || time.Now().After(deadline) {
			return fmt.Errorf("enable WAL: %w", err)
		}
	}
	// Most opens find the schema current, so check with a plain read first: on Windows a
	// committed write costs two disk flushes, which a status line refresh can't afford.
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v == len(migrations) {
		return nil
	}
	return s.immediate(func(ctx context.Context, c *sql.Conn) error {
		if err := c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
			return err
		}
		if v > len(migrations) {
			return fmt.Errorf("database was written by a newer ciguse (schema %d), update ciguse", v)
		}
		if v == len(migrations) {
			return nil // another process migrated while this one waited for the lock
		}
		for i := v; i < len(migrations); i++ {
			if _, err := c.ExecContext(ctx, migrations[i]); err != nil {
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		_, err := c.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(migrations)))
		return err
	})
}

// immediate runs fn in a write transaction that takes the lock up front,
// so concurrent processes queue instead of racing.
func (s *Store) immediate(fn func(ctx context.Context, c *sql.Conn) error) error {
	ctx := context.Background()
	c, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	if err := fn(ctx, c); err != nil {
		c.ExecContext(ctx, "ROLLBACK")
		return err
	}
	_, err = c.ExecContext(ctx, "COMMIT")
	return err
}

type Settings struct {
	DefaultProduct string
	DayStart       time.Duration // local time at which a new day begins
}

func (s *Store) Settings() (Settings, error) {
	var st Settings
	var dayStartMin string
	err := s.db.QueryRow(`SELECT
		(SELECT value FROM settings WHERE key = 'default_product'),
		(SELECT value FROM settings WHERE key = 'day_start_min')`).Scan(&st.DefaultProduct, &dayStartMin)
	if err != nil {
		return st, err
	}
	m, err := strconv.Atoi(dayStartMin)
	st.DayStart = time.Duration(m) * time.Minute
	return st, err
}

func (s *Store) SetDefaultProduct(id string) error {
	if _, err := s.product(id); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE settings SET value = ? WHERE key = 'default_product'`, id)
	return err
}

func (s *Store) SetDayStart(d time.Duration) error {
	_, err := s.db.Exec(`UPDATE settings SET value = ? WHERE key = 'day_start_min'`, strconv.Itoa(int(d/time.Minute)))
	return err
}

type Product struct {
	ID, Label      string
	PackPriceMinor sql.NullInt64
	PackSize       sql.NullInt64
	Currency       sql.NullString
}

func (s *Store) Products() ([]Product, error) {
	rows, err := s.db.Query(`SELECT id, label, pack_price_minor, pack_size, currency FROM products ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ps []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.Label, &p.PackPriceMinor, &p.PackSize, &p.Currency); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, rows.Err()
}

func (s *Store) product(id string) (Product, error) {
	p := Product{ID: id}
	err := s.db.QueryRow(`SELECT label, pack_price_minor, pack_size, currency FROM products WHERE id = ?`, id).
		Scan(&p.Label, &p.PackPriceMinor, &p.PackSize, &p.Currency)
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("%w %q", ErrUnknownProduct, id)
	}
	return p, err
}

// SetPrice sets a product's pack price. Past events keep the price they were logged with.
func (s *Store) SetPrice(id string, packPriceMinor, packSize int64, currency string, now time.Time) error {
	if _, err := s.product(id); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE products SET pack_price_minor = ?, pack_size = ?, currency = ?, updated_at = ? WHERE id = ?`,
		packPriceMinor, packSize, currency, now.UnixMilli(), id)
	return err
}

type Use struct {
	Product string    // empty means the default product
	At      time.Time // when it was used
	Now     time.Time // when it was logged
	Source  string    // cli | hotkey | prompt
	Force   bool      // log even inside the duplicate window
}

type Logged struct {
	Product   string
	At        time.Time
	Duplicate bool      // nothing was stored
	Previous  time.Time // the existing use that made this a duplicate
	Today     int
}

// Log records one use unless another use sits within DuplicateWindow of it.
func (s *Store) Log(u Use) (Logged, error) {
	st, err := s.Settings()
	if err != nil {
		return Logged{}, err
	}
	if u.Product == "" {
		u.Product = st.DefaultProduct
	}
	p, err := s.product(u.Product)
	if err != nil {
		return Logged{}, err
	}
	out := Logged{Product: p.ID, At: u.At}
	at := u.At.UnixMilli()
	_, offset := u.At.Zone()
	err = s.immediate(func(ctx context.Context, c *sql.Conn) error {
		if !u.Force {
			var prev int64
			err := c.QueryRowContext(ctx, `SELECT ts FROM events WHERE deleted_at IS NULL AND ts BETWEEN ? AND ?
				ORDER BY abs(ts - ?) LIMIT 1`, at-DuplicateWindow.Milliseconds(), at+DuplicateWindow.Milliseconds(), at).Scan(&prev)
			if err == nil {
				out.Duplicate = true
				out.Previous = time.UnixMilli(prev).In(u.At.Location())
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		_, err := c.ExecContext(ctx, `INSERT INTO events
			(ts, logged_at, utc_offset_min, product_id, pack_price_minor, pack_size, currency, source)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			at, u.Now.UnixMilli(), offset/60, p.ID, p.PackPriceMinor, p.PackSize, p.Currency, u.Source)
		return err
	})
	if err != nil {
		return out, err
	}
	out.Today, err = s.countDay(u.Now, st.DayStart)
	return out, err
}

type Undone struct {
	OK      bool // false when there was nothing to undo
	Product string
	At      time.Time
	Today   int
}

// Undo removes the most recently logged use from the last UndoWindow. The row is kept, marked deleted.
func (s *Store) Undo(now time.Time) (Undone, error) {
	st, err := s.Settings()
	if err != nil {
		return Undone{}, err
	}
	var out Undone
	err = s.immediate(func(ctx context.Context, c *sql.Conn) error {
		var id, ts int64
		var offsetMin int
		err := c.QueryRowContext(ctx, `SELECT id, ts, utc_offset_min, product_id FROM events
			WHERE deleted_at IS NULL AND logged_at >= ? ORDER BY logged_at DESC, id DESC LIMIT 1`,
			now.Add(-UndoWindow).UnixMilli()).Scan(&id, &ts, &offsetMin, &out.Product)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out.OK = true
		out.At = eventTime(ts, offsetMin)
		_, err = c.ExecContext(ctx, `UPDATE events SET deleted_at = ? WHERE id = ?`, now.UnixMilli(), id)
		return err
	})
	if err != nil {
		return out, err
	}
	out.Today, err = s.countDay(now, st.DayStart)
	return out, err
}

type Day struct {
	Date time.Time // calendar date; only year, month and day are meaningful
	Uses int
}

type Report struct {
	Days   []Day // oldest first, ending today
	Uses   int
	Active int                 // days in range on or after the first use ever logged
	Value  map[string]*big.Rat // currency → total in major units
	Priced int                 // uses that had a price
}

// Report summarises the last days local days, today included.
func (s *Store) Report(now time.Time, days int) (Report, error) {
	st, err := s.Settings()
	if err != nil {
		return Report{}, err
	}
	r := Report{Value: map[string]*big.Rat{}}
	index := map[string]int{}
	today := now.Add(-st.DayStart)
	for i := days - 1; i >= 0; i-- {
		d := today.AddDate(0, 0, -i)
		index[d.Format(time.DateOnly)] = len(r.Days)
		r.Days = append(r.Days, Day{Date: d})
	}

	var firstTS int64
	var firstOffset int
	err = s.db.QueryRow(`SELECT ts, utc_offset_min FROM events WHERE deleted_at IS NULL ORDER BY ts LIMIT 1`).
		Scan(&firstTS, &firstOffset)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	first := localDay(eventTime(firstTS, firstOffset), st.DayStart)
	for _, d := range r.Days {
		if d.Date.Format(time.DateOnly) >= first {
			r.Active++
		}
	}

	// Offsets differ by at most a day, so a two-day margin catches every event in range.
	since := now.AddDate(0, 0, -days-2).UnixMilli()
	rows, err := s.db.Query(`SELECT ts, utc_offset_min, pack_price_minor, pack_size, currency FROM events
		WHERE deleted_at IS NULL AND ts >= ?`, since)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var ts int64
		var offsetMin int
		var price, size sql.NullInt64
		var currency sql.NullString
		if err := rows.Scan(&ts, &offsetMin, &price, &size, &currency); err != nil {
			return r, err
		}
		i, ok := index[localDay(eventTime(ts, offsetMin), st.DayStart)]
		if !ok {
			continue
		}
		r.Days[i].Uses++
		r.Uses++
		if price.Valid && size.Valid && size.Int64 > 0 && currency.Valid {
			if r.Value[currency.String] == nil {
				r.Value[currency.String] = new(big.Rat)
			}
			r.Value[currency.String].Add(r.Value[currency.String], big.NewRat(price.Int64, size.Int64*100))
			r.Priced++
		}
	}
	return r, rows.Err()
}

func (s *Store) countDay(now time.Time, dayStart time.Duration) (int, error) {
	rows, err := s.db.Query(`SELECT ts, utc_offset_min FROM events WHERE deleted_at IS NULL AND ts >= ?`,
		now.AddDate(0, 0, -2).UnixMilli())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	today := localDay(now, dayStart)
	n := 0
	for rows.Next() {
		var ts int64
		var offsetMin int
		if err := rows.Scan(&ts, &offsetMin); err != nil {
			return 0, err
		}
		if localDay(eventTime(ts, offsetMin), dayStart) == today {
			n++
		}
	}
	return n, rows.Err()
}

// eventTime returns an event's time on the wall clock where it was logged.
func eventTime(ts int64, offsetMin int) time.Time {
	return time.UnixMilli(ts).In(time.FixedZone("", offsetMin*60))
}

func localDay(t time.Time, dayStart time.Duration) string {
	return t.Add(-dayStart).Format(time.DateOnly)
}
