package state_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

func TestASeriesExtendedByAPrecheckEndingAfterALaterSkipKeepsItsFinalCountInHistory(t *testing.T) {
	store := openStore(t)
	at := func(minute int) time.Time { return time.Date(2026, 10, 19, 10, minute, 0, 0, time.UTC) }
	record := func(minute int, reason string) *state.Run {
		r := &state.Run{ID: state.NewRunID(at(minute).Add(10 * time.Second)), Project: "proj", Job: "watch", Trigger: state.TriggerSchedule, Status: state.StatusSkipped, Reason: reason, ScheduledFor: at(minute)}
		entry, err := store.Record(r)
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	series := record(0, "precheck: nothing new")
	record(1, "precheck: nothing new")
	late := &state.Run{ID: state.NewRunID(at(2).Add(10 * time.Second)), Project: "proj", Job: "watch", Trigger: state.TriggerSchedule, Status: state.StatusRunning, ScheduledFor: at(2)}
	store.SaveRun(late)
	record(3, state.ReasonOverlap)
	late.Status, late.Reason = state.StatusSkipped, "precheck: nothing new"
	if entry, _ := store.Record(late); entry.ID != series.ID || entry.SkippedCount != 3 {
		t.Fatalf("expected the 10:02 skip folded into the series, got %+v", entry)
	}
	saveEntry(t, store, at(4), state.StatusSucceeded, "")

	data, _ := os.ReadFile(store.Paths.History())
	var last state.Run
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r state.Run
		json.Unmarshal([]byte(line), &r)
		if r.ID == series.ID {
			last = r
		}
	}
	if last.SkippedCount != 3 {
		t.Fatalf("the last history line of the series should count 3 occurrences, got %d", last.SkippedCount)
	}
}
