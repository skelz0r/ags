package state_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/skelz0r/ags/internal/paths"
	"github.com/skelz0r/ags/internal/state"
)

func openStore(t *testing.T) *state.Store {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(paths.Resolve(func(key string) string {
		return map[string]string{"AGS_HOME": root, "HOME": root}[key]
	}))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func saveEntry(t *testing.T, store *state.Store, at time.Time, status, reason string) *state.Run {
	t.Helper()
	r := &state.Run{ID: state.NewRunID(at), Project: "proj", Job: "watch", Trigger: state.TriggerSchedule, Status: status, Reason: reason, ScheduledFor: at}
	if err := store.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPruneKeepsTheLastHundredRunsOfAJobHoweverManySkipsCameAfterThem(t *testing.T) {
	store := openStore(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var succeeded []string
	for i := 0; i < 150; i++ {
		succeeded = append(succeeded, saveEntry(t, store, start.Add(time.Duration(i)*time.Hour), state.StatusSucceeded, "").ID)
	}
	skipsFrom := start.Add(200 * time.Hour)
	for i := 0; i < 2000; i++ {
		saveEntry(t, store, skipsFrom.Add(time.Duration(i)*time.Minute), state.StatusSkipped, fmt.Sprintf("precheck: item %d", i))
	}

	if err := store.Prune(state.KeepPerJob); err != nil {
		t.Fatal(err)
	}

	runs, err := store.ListRuns(state.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	skipped := 0
	for _, r := range runs {
		kept[r.ID] = true
		if r.Status == state.StatusSkipped {
			skipped++
		}
	}
	for i, id := range succeeded {
		if want := i >= 50; kept[id] != want {
			t.Fatalf("succeeded run %d of 150: kept=%v, expected the last 100 kept", i+1, kept[id])
		}
	}
	if skipped != state.KeepPerJob {
		t.Fatalf("expected the last %d skipped entries kept apart from the runs, got %d", state.KeepPerJob, skipped)
	}
}
