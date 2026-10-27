package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
	"github.com/skelz0r/ags/internal/state"
)

func watchJob(schedule, precheck string) string {
	return `version: 1
timezone: Europe/Paris
jobs:
  watch:
    schedule: "` + schedule + `"
    prompt: Watch.
    precheck: '` + precheck + `'
`
}

const skipUnlessGo = `if [ -f go ]; then rm go; else echo nothing new; exit 1; fi`

func TestThreeHundredPrecheckSkipsInARowAreOneEntryAndTheRunAfterThemSurvivesThePrune(t *testing.T) {
	w := New(t)
	w.SharedFile(watchJob("*/5 * * * *", skipUnlessGo))
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 7, 59, 0))
	w.TickEvery(d, At(19, 8, 0, 0), At(20, 8, 55, 0), time.Minute)
	w.Write("go", "")
	w.TickEvery(d, At(20, 8, 56, 0), At(20, 9, 0, 0), time.Minute)

	runs := w.Runs()
	if got := Statuses(runs); strings.Join(got, ",") != "skipped (precheck) x300,succeeded" {
		t.Fatalf("expected one entry for the 300 skips then the run, got %v", got)
	}
	series := runs[1]
	if !series.ScheduledFor.Equal(At(19, 8, 0, 0)) || !series.SkippedLast.Equal(At(20, 8, 55, 0)) {
		t.Fatalf("expected the series from 08:00 to the next day 08:55, got %s to %s", series.ScheduledFor, series.SkippedLast)
	}

	store, err := state.Open(w.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Prune(state.KeepPerJob); err != nil {
		t.Fatal(err)
	}
	if got := Statuses(w.Runs()); strings.Join(got, ",") != "skipped (precheck) x300,succeeded" {
		t.Fatalf("the prune must keep the run, got %v", got)
	}
	dirs, _ := os.ReadDir(filepath.Join(w.Paths.RunsDir(), "proj", "watch"))
	if len(dirs) != 2 {
		t.Fatalf("a folded skip leaves no directory behind, got %d directories", len(dirs))
	}
}

func TestAChangeOfThePrecheckReasonStartsANewEntry(t *testing.T) {
	w := New(t)
	w.SharedFile(watchJob("* * * * *", `cat reason; exit 1`))
	w.Write("reason", "3 items pending\n")
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 2, 0), 30*time.Second)
	w.Write("reason", "4 items pending\n")
	w.TickEvery(d, At(19, 10, 2, 30), At(19, 10, 4, 0), 30*time.Second)

	runs := w.Runs()
	if got := Statuses(runs); strings.Join(got, ",") != "skipped (precheck) x3,skipped (precheck) x2" {
		t.Fatalf("expected two entries, one per reason, got %v", got)
	}
	if runs[1].Reason != "precheck: 3 items pending" || runs[0].Reason != "precheck: 4 items pending" {
		t.Fatalf("each entry keeps its own reason, got %q then %q", runs[1].Reason, runs[0].Reason)
	}
}

func TestARunBetweenTwoSeriesOfSkipsKeepsThemApartInChronologicalOrder(t *testing.T) {
	w := New(t)
	w.SharedFile(watchJob("* * * * *", skipUnlessGo))
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 2, 0), 30*time.Second)
	w.Write("go", "")
	w.TickEvery(d, At(19, 10, 2, 30), At(19, 10, 5, 0), 30*time.Second)

	runs := w.Runs()
	if got := Statuses(runs); strings.Join(got, ",") != "skipped (precheck) x3,succeeded,skipped (precheck) x2" {
		t.Fatalf("expected a series, the run, then a new series, got %v", got)
	}
	if !runs[0].ScheduledFor.Equal(At(19, 10, 4, 0)) || !runs[1].ScheduledFor.Equal(At(19, 10, 3, 0)) {
		t.Fatalf("expected the run at 10:03 and the new series from 10:04, got %s and %s", runs[1].ScheduledFor, runs[0].ScheduledFor)
	}
}

func TestApprovingAJobEndsItsSeriesOfUnapprovedSkips(t *testing.T) {
	w := New(t)
	w.SharedFile(checkJob("0 * * * *", "Check.", "true"))
	w.MustCLI("add", w.ProjectDir, "--name", "proj")
	d := w.Daemon()
	w.Tick(d, At(19, 7, 59, 0))
	w.TickEvery(d, At(19, 8, 0, 0), At(19, 10, 30, 0), time.Minute)
	w.MustCLI("approve", "proj/check", "--yes")
	w.TickEvery(d, At(19, 10, 31, 0), At(19, 11, 1, 0), time.Minute)

	runs := w.Runs()
	if got := Statuses(runs); strings.Join(got, ",") != "skipped (unapproved) x3,succeeded" {
		t.Fatalf("expected the hourly skips folded, then the approved run, got %v", got)
	}
	if !runs[1].SkippedLast.Equal(At(19, 10, 0, 0)) {
		t.Fatalf("expected the series to end at 10:00, got %s", runs[1].SkippedLast)
	}
}
