package store

import (
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

var cet = time.FixedZone("CET", 3600)

// base is a weekday noon, well clear of the 04:00 day boundary.
var base = time.Date(2026, 10, 27, 12, 0, 0, 0, cet)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ciguse.db"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func logAt(t *testing.T, s *Store, at time.Time) Logged {
	t.Helper()
	l, err := s.Log(Use{At: at, Now: at, Source: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLogUsesDefaultProductAndCountsToday(t *testing.T) {
	s := open(t)
	l := logAt(t, s, base)
	if l.Product != "cigarette" || l.Duplicate || l.Today != 1 {
		t.Fatalf("got %+v", l)
	}
	if err := s.SetDefaultProduct("iqos"); err != nil {
		t.Fatal(err)
	}
	l = logAt(t, s, base.Add(time.Hour))
	if l.Product != "iqos" || l.Today != 2 {
		t.Fatalf("got %+v", l)
	}
}

func TestUnknownProduct(t *testing.T) {
	s := open(t)
	_, err := s.Log(Use{Product: "vape", At: base, Now: base, Source: "cli"})
	if !errors.Is(err, ErrUnknownProduct) {
		t.Fatalf("got %v", err)
	}
	if err := s.SetDefaultProduct("vape"); !errors.Is(err, ErrUnknownProduct) {
		t.Fatalf("got %v", err)
	}
}

func TestDuplicateWindow(t *testing.T) {
	s := open(t)
	logAt(t, s, base)

	l := logAt(t, s, base.Add(60*time.Second))
	if !l.Duplicate || !l.Previous.Equal(base) || l.Today != 1 {
		t.Fatalf("60s later should be a duplicate, got %+v", l)
	}
	l = logAt(t, s, base.Add(91*time.Second))
	if l.Duplicate || l.Today != 2 {
		t.Fatalf("91s later should log, got %+v", l)
	}
	l, err := s.Log(Use{At: base.Add(100 * time.Second), Now: base.Add(100 * time.Second), Source: "cli", Force: true})
	if err != nil || l.Duplicate || l.Today != 3 {
		t.Fatalf("force should log, got %+v %v", l, err)
	}
}

func TestAgoChecksDuplicatesAroundTheBackdatedTime(t *testing.T) {
	s := open(t)
	logAt(t, s, base.Add(-5*time.Minute))

	l, err := s.Log(Use{At: base.Add(-4*time.Minute - 30*time.Second), Now: base, Source: "cli"})
	if err != nil || !l.Duplicate {
		t.Fatalf("backdated log next to an existing one should be a duplicate, got %+v %v", l, err)
	}
	l, err = s.Log(Use{At: base.Add(-20 * time.Minute), Now: base, Source: "cli"})
	if err != nil || l.Duplicate || !l.At.Equal(base.Add(-20*time.Minute)) || l.Today != 2 {
		t.Fatalf("got %+v %v", l, err)
	}
}

func TestPriceSnapshotSurvivesPriceChange(t *testing.T) {
	s := open(t)
	if err := s.SetPrice("cigarette", 500, 20, "EUR", base); err != nil {
		t.Fatal(err)
	}
	logAt(t, s, base.Add(-24*time.Hour))
	if err := s.SetPrice("cigarette", 600, 20, "EUR", base); err != nil {
		t.Fatal(err)
	}
	logAt(t, s, base)
	logAt(t, s, base.Add(time.Hour))

	r, err := s.Report(base.Add(2*time.Hour), 7)
	if err != nil {
		t.Fatal(err)
	}
	// 0.25 + 0.30 + 0.30
	if got := r.Value["EUR"].FloatString(2); got != "0.85" || r.Priced != 3 {
		t.Fatalf("value %s priced %d", got, r.Priced)
	}
}

func TestUnpricedUsesAreNotZero(t *testing.T) {
	s := open(t)
	logAt(t, s, base)
	r, err := s.Report(base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Value) != 0 || r.Priced != 0 || r.Uses != 1 {
		t.Fatalf("got %+v", r)
	}
}

func TestNicotineSnapshotSurvivesChange(t *testing.T) {
	s := open(t)
	logAt(t, s, base.Add(-24*time.Hour)) // a cigarette at the built-in estimate
	iqos := func(at time.Time) {
		t.Helper()
		if _, err := s.Log(Use{Product: "iqos", At: at, Now: at, Source: "cli"}); err != nil {
			t.Fatal(err)
		}
	}
	iqos(base)
	iqos(base.Add(time.Hour))
	if err := s.SetNicotine("iqos", 500); err != nil {
		t.Fatal(err)
	}
	iqos(base.Add(2 * time.Hour))
	if err := s.SetNicotine("vape", 500); !errors.Is(err, ErrUnknownProduct) {
		t.Fatalf("got %v", err)
	}

	r, err := s.Report(base.Add(3*time.Hour), 7)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Dose{{"iqos", 600, 2}, {"cigarette", 1000, 1}, {"iqos", 500, 1}}; !slices.Equal(r.Nicotine, want) {
		t.Fatalf("got %+v, want %+v", r.Nicotine, want)
	}
}

// Uses logged before nicotine estimates existed get their product's estimate.
func TestMigrationBackfillsNicotine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ciguse.db")
	s, err := Open(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Log(Use{Product: "pouch", At: base, Now: base, Source: "cli"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE events DROP COLUMN nicotine_ug; ALTER TABLE products DROP COLUMN nicotine_ug;
		PRAGMA user_version = 4`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.Report(base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Dose{{"pouch", 800, 1}}; !slices.Equal(r.Nicotine, want) {
		t.Fatalf("got %+v", r.Nicotine)
	}
}

func TestDayStartMovesLateNightsToThePreviousDay(t *testing.T) {
	s := open(t) // default day start 04:00
	logAt(t, s, time.Date(2026, 10, 28, 2, 0, 0, 0, cet))
	logAt(t, s, time.Date(2026, 10, 28, 5, 0, 0, 0, cet))

	r, err := s.Report(time.Date(2026, 10, 28, 10, 0, 0, 0, cet), 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Days[0].Date.Day() != 27 || r.Days[0].Uses != 1 || r.Days[1].Uses != 1 {
		t.Fatalf("got %+v", r.Days)
	}
}

func TestEventsKeepTheirOwnUTCOffset(t *testing.T) {
	s := open(t)
	if err := s.SetDayStart(0); err != nil {
		t.Fatal(err)
	}
	// 00:30 on the 28th in CET is still the 27th in New York.
	logAt(t, s, time.Date(2026, 10, 28, 0, 30, 0, 0, cet))

	ny := time.FixedZone("EST", -5*3600)
	r, err := s.Report(time.Date(2026, 10, 28, 10, 0, 0, 0, ny), 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Days[1].Date.Day() != 28 || r.Days[1].Uses != 1 || r.Days[0].Uses != 0 {
		t.Fatalf("event should count on the 28th where it was logged, got %+v", r.Days)
	}
}

func TestUndo(t *testing.T) {
	s := open(t)
	logAt(t, s, base)
	s.Log(Use{Product: "iqos", At: base.Add(2 * time.Minute), Now: base.Add(2 * time.Minute), Source: "cli"})

	u, err := s.Undo(base.Add(3 * time.Minute))
	if err != nil || !u.OK || u.Product != "iqos" || u.Today != 1 {
		t.Fatalf("got %+v %v", u, err)
	}
	u, err = s.Undo(base.Add(3 * time.Minute))
	if err != nil || !u.OK || u.Product != "cigarette" || u.Today != 0 {
		t.Fatalf("got %+v %v", u, err)
	}
	u, err = s.Undo(base.Add(3 * time.Minute))
	if err != nil || u.OK {
		t.Fatalf("nothing left to undo, got %+v %v", u, err)
	}
}

func TestUndoIgnoresLogsOlderThanADay(t *testing.T) {
	s := open(t)
	logAt(t, s, base)
	u, err := s.Undo(base.Add(25 * time.Hour))
	if err != nil || u.OK {
		t.Fatalf("got %+v %v", u, err)
	}
}

func TestReportActiveDaysStartAtFirstUse(t *testing.T) {
	s := open(t)
	logAt(t, s, base.Add(-3*24*time.Hour))
	logAt(t, s, base)

	r, err := s.Report(base, 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Days) != 14 || r.Active != 4 || r.Uses != 2 || r.Days[13].Uses != 1 {
		t.Fatalf("got active %d uses %d days %+v", r.Active, r.Uses, r.Days)
	}
}

func TestConcurrentLogsFromSeparateProcessesStoreOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ciguse.db")
	const n = 8
	var wg sync.WaitGroup
	results := make(chan Logged, n)
	errs := make(chan error, n)
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s, err := Open(path, 2*time.Second) // fresh database: migrations race too
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			l, err := s.Log(Use{At: base, Now: base, Source: "hotkey"})
			if err != nil {
				errs <- err
				return
			}
			results <- l
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	stored := 0
	for l := range results {
		if !l.Duplicate {
			stored++
		}
	}
	if stored != 1 {
		t.Fatalf("stored %d, want 1", stored)
	}
}
