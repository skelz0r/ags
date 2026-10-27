package daemon_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
)

func weekly(schedule, timezone string) string {
	return `version: 1
timezone: ` + timezone + `
jobs:
  audit:
    schedule: "` + schedule + `"
    prompt: Audit.
`
}

func TestAScheduleMovedLaterInTheDayAfterTheRunIsNotMissedAndRunsAtItsNewTime(t *testing.T) {
	w := New(t)
	w.SharedFile(weekly("30 9 * * 1", "Europe/Paris"))
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 29, 30), At(19, 9, 31, 0), 30*time.Second)
	w.TickEvery(d, At(20, 8, 35, 0), At(20, 8, 40, 0), 30*time.Second)

	w.SharedFile(weekly("30 16 * * 1", "Europe/Paris"))
	w.TickEvery(d, At(20, 8, 40, 30), At(20, 8, 43, 0), 30*time.Second)

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "succeeded" {
		t.Fatalf("Monday 16:30 was not an occurrence when Monday passed, expected only the 09:30 run, got %v", got)
	}
	if w.Notes.Count("missed") != 0 {
		t.Fatalf("nothing was missed, got %d missed notification(s)", w.Notes.Count("missed"))
	}

	w.TickEvery(d, At(26, 16, 28, 0), At(26, 16, 31, 0), 30*time.Second)
	runs := w.Runs()
	if len(runs) != 2 || !runs[0].ScheduledFor.Equal(At(26, 16, 30, 0)) || runs[0].Trigger != "schedule" || runs[0].Status != "succeeded" {
		t.Fatalf("expected the job to run on time next Monday at 16:30, got %+v", runs)
	}
}

func TestAScheduleMovedToAMomentAlreadyPassedSinceTheRunDoesNotRunAgain(t *testing.T) {
	w := New(t)
	w.SharedFile(weekly("30 9 * * 1", "Europe/Paris"))
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 29, 30), At(19, 10, 0, 0), 30*time.Second)

	w.SharedFile(weekly("45 9 * * 1", "Europe/Paris"))
	w.TickEvery(d, At(19, 10, 0, 30), At(19, 10, 5, 0), 30*time.Second)

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "succeeded" {
		t.Fatalf("the job already ran this Monday, expected no catch up nor missed entry, got %v", got)
	}
}

func TestATimezoneChangeAloneIsTreatedLikeAScheduleChange(t *testing.T) {
	w := New(t)
	w.SharedFile(weekly("30 9 * * 1", "Europe/Paris"))
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 29, 30), At(19, 10, 45, 0), 30*time.Second)

	w.SharedFile(weekly("30 9 * * 1", "Europe/London"))
	w.TickEvery(d, At(19, 10, 45, 30), At(19, 10, 50, 0), 30*time.Second)

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "succeeded" {
		t.Fatalf("09:30 in London (10:30 in Paris) had passed when the timezone changed, expected no catch up, got %v", got)
	}
}

func TestACursorSavedByAPreviousVersionKeepsItsCatchUp(t *testing.T) {
	w := New(t)
	w.SharedFile(weekly("30 9 * * 1", "Europe/Paris"))
	w.Register()
	w.Tick(w.Daemon(), At(19, 8, 0, 0))
	forgetCursorSchedules(t, w)

	upgraded := w.Daemon()
	w.Tick(upgraded, At(19, 18, 0, 0))
	w.TickEvery(upgraded, At(19, 18, 0, 30), At(19, 18, 2, 0), 30*time.Second)

	runs := w.Runs()
	if len(runs) != 1 || runs[0].Trigger != "catch_up" || runs[0].Status != "succeeded" || !runs[0].ScheduledFor.Equal(At(19, 9, 30, 0)) {
		t.Fatalf("a cursor without its schedule must keep the occurrence it was catching up, got %+v", runs)
	}
}

func forgetCursorSchedules(t *testing.T, w *World) {
	t.Helper()
	data, err := os.ReadFile(w.Paths.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	delete(st, "cursor_schedules")
	if data, err = json.Marshal(st); err != nil {
		t.Fatal(err)
	}
	w.Write(w.Paths.StateFile(), string(data))
}
