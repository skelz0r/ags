package daemon_test

import (
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
)

func nightly(at string) string {
	return `version: 1
timezone: Europe/Paris
jobs:
  nightly:
    schedule: "` + at + `"
    prompt: Night.
`
}

func TestATimeThatDoesNotExistOnTheSpringForwardNightIsSkipped(t *testing.T) {
	w := New(t)
	w.SharedFile(nightly("30 2 * * *"))
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, time.Date(2026, 3, 28, 23, 0, 0, 0, Paris), time.Date(2026, 3, 30, 3, 0, 0, 0, Paris), 30*time.Second)

	runs := w.Runs()
	if len(runs) != 1 || !runs[0].ScheduledFor.Equal(time.Date(2026, 3, 30, 2, 30, 0, 0, Paris)) || runs[0].Trigger != "schedule" {
		t.Fatalf("expected only the 30 March run, got %+v", runs)
	}
}

func TestAnHourRepeatedOnTheFallBackNightFiresOnce(t *testing.T) {
	w := New(t)
	w.SharedFile(nightly("30 2 * * *"))
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, time.Date(2026, 10, 25, 1, 0, 0, 0, Paris), time.Date(2026, 10, 25, 4, 0, 0, 0, Paris), 30*time.Second)

	runs := w.Runs()
	if len(runs) != 1 || runs[0].Trigger != "schedule" {
		t.Fatalf("expected a single run, got %+v", runs)
	}
	if offset := runs[0].ScheduledFor.Format("-07:00"); offset != "+02:00" {
		t.Fatalf("expected the first 02:30 (summer time), got %s", runs[0].ScheduledFor)
	}
}

func TestAnHourlyJobFiresOnceForTheRepeatedHour(t *testing.T) {
	w := New(t)
	w.SharedFile(nightly("0 * * * *"))
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, time.Date(2026, 10, 25, 0, 30, 0, 0, Paris), time.Date(2026, 10, 25, 3, 30, 0, 0, Paris), 30*time.Second)

	if got := len(w.Runs()); got != 3 {
		t.Fatalf("expected 01:00, 02:00 and 03:00 once each, got %d runs", got)
	}
}
