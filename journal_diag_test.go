package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// Temporary: compares SQLite journal settings per process-style open/write/close.
func TestLatencyJournalModes(t *testing.T) {
	if os.Getenv("CIGUSE_LATENCY") != "1" {
		t.Skip()
	}
	variants := []struct {
		name, journal, sync string
		persist, write      bool
	}{
		{"WAL FULL (current)", "WAL", "FULL", false, true},
		{"WAL NORMAL", "WAL", "NORMAL", false, true},
		{"WAL NORMAL persist-wal", "WAL", "NORMAL", true, true},
		{"WAL open+close, no write", "WAL", "NORMAL", false, false},
		{"DELETE NORMAL", "DELETE", "NORMAL", false, true},
		{"TRUNCATE NORMAL", "TRUNCATE", "NORMAL", false, true},
		{"TRUNCATE FULL", "TRUNCATE", "FULL", false, true},
		{"PERSIST NORMAL", "PERSIST", "NORMAL", false, true},
	}
	for _, v := range variants {
		path := filepath.Join(t.TempDir(), "x.db")
		once := func() {
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			ctx := context.Background()
			c, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, p := range []string{"PRAGMA busy_timeout = 250", "PRAGMA journal_mode = " + v.journal, "PRAGMA synchronous = " + v.sync,
				"CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY, ts INTEGER)"} {
				if _, err := c.ExecContext(ctx, p); err != nil {
					t.Fatal(err)
				}
			}
			if v.persist {
				if err := c.Raw(func(dc any) error {
					_, err := dc.(sqlite.FileControl).FileControlPersistWAL("main", 1)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			if v.write {
				if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
					t.Fatal(err)
				}
				if _, err := c.ExecContext(ctx, "INSERT INTO t (ts) VALUES (?)", time.Now().UnixMilli()); err != nil {
					t.Fatal(err)
				}
				if _, err := c.ExecContext(ctx, "COMMIT"); err != nil {
					t.Fatal(err)
				}
			}
		}
		once()
		measure(t, "journal "+v.name, once)
	}
}
