package schedule_test

import (
	"testing"
	"time"

	"github.com/skelz0r/ags/internal/schedule"
)

func TestAfterAVeryLongGapTheCandidateIsStillTheLatestOccurrence(t *testing.T) {
	s, err := schedule.Parse("* * * * *", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := after.Add(100002 * time.Minute).Add(10 * time.Second)
	due, ok := s.Due(after, until)
	if !ok || !due.Latest.Equal(until.Truncate(time.Minute)) {
		t.Fatalf("expected the latest occurrence %s, got %s", until.Truncate(time.Minute), due.Latest)
	}
	if due.Earlier < 100000 {
		t.Fatalf("expected at least 100000 earlier occurrences counted, got %d", due.Earlier)
	}
}

func TestPastTheScanCapThePreviousOccurrenceIsTheRealOne(t *testing.T) {
	s, err := schedule.Parse("* * * * 1-5", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	due, _ := s.Due(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 13, 0, 0, 10, 0, time.UTC))
	if !due.Latest.Equal(time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC)) || !due.BeforeLatest.Equal(time.Date(2026, 4, 10, 23, 59, 0, 0, time.UTC)) {
		t.Fatalf("expected Monday 00:00 after Friday 23:59, got %s after %s", due.Latest, due.BeforeLatest)
	}
}
