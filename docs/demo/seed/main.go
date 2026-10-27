// Seed writes two weeks of sample Claude Code sessions and uses into $CIGUSE_HOME, so the
// report in docs/demo.gif has something to show. Run it after `ciguse init`, only on a
// throwaway folder: go run ./docs/demo/seed
package main

import (
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/demetriusk/ciguse/internal/store"
)

func main() {
	dir := os.Getenv("CIGUSE_HOME")
	if dir == "" {
		log.Fatal("set CIGUSE_HOME to a throwaway folder")
	}
	s, err := store.Open(filepath.Join(dir, "ciguse.db"), 2*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	r := rand.New(rand.NewPCG(1, 2))
	now := time.Now().Add(-10 * time.Minute)
	mins := func(lo, hi int) time.Duration { return time.Duration(lo+r.IntN(hi-lo+1)) * time.Minute }
	// uses logs a use at random through [from, to) at perHour on average.
	uses := func(from, to time.Time, perHour float64) {
		for t := from; ; {
			t = t.Add(time.Duration(r.ExpFloat64() / perHour * float64(time.Hour)))
			if !t.Before(to) || t.After(now) {
				return
			}
			if _, err := s.Log(store.Use{At: t, Now: t, Source: "cli", Force: true}); err != nil {
				log.Fatal(err)
			}
		}
	}
	agent := func(session, project string, at time.Time, state string) {
		if at.After(now) {
			return
		}
		err := s.RecordAgent(store.AgentEvent{Session: session, Project: project, Alias: project, At: at, State: state, Cause: "seed"})
		if err != nil {
			log.Fatal(err)
		}
	}

	y, m, d := now.AddDate(0, 0, -13).Date()
	for day := range 14 {
		t := time.Date(y, m, d+day, 9, 0, 0, 0, time.Local).Add(mins(0, 40))
		end := t.Add(9 * time.Hour)
		for i := 0; t.Before(end) && t.Before(now); i++ {
			session, project := fmt.Sprintf("demo-%d-%d", day, i), []string{"api-server", "web-app"}[r.IntN(2)]
			stop := t.Add(mins(40, 100))
			for t.Before(stop) {
				work, wait := mins(4, 16), mins(1, 9)
				agent(session, project, t, store.Working)
				uses(t, t.Add(work), 0.9)
				t = t.Add(work)
				agent(session, project, t, store.WaitingOnYou)
				uses(t, t.Add(wait), 1.8)
				t = t.Add(wait)
			}
			agent(session, project, t, store.Ended)
			gap := mins(10, 40)
			uses(t, t.Add(gap), 0.8)
			t = t.Add(gap)
		}
	}
}
