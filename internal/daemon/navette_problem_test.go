package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
)

func withFlakyCommand(t *testing.T, w *World) {
	t.Helper()
	machine, _ := os.ReadFile(w.Paths.MachineConfig())
	w.Write(w.Paths.MachineConfig(), strings.Replace(string(machine), "commands:\n",
		"commands:\n  flaky:\n    argv: [\"sh\", \"-c\", \"cat > /dev/null; test ! -e broken\"]\n    timeout: 10s\n    report_within: never\n", 1))
}

const dailyFlaky = `version: 1
timezone: Europe/Paris
jobs:
  daily:
    schedule: "0 9 * * *"
    command: flaky
    prompt: Daily.
    catch_up: never
`

func TestAFailingJobStaysAFailureInTheInboxWhenLaterOccurrencesAreMissed(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	withFlakyCommand(t, w)
	w.Write("broken", "")
	w.SharedFile(dailyFlaky)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 8, 59, 30), At(19, 9, 0, 30), 30*time.Second)
	w.TickEvery(d, At(20, 8, 59, 30), At(20, 9, 1, 0), 30*time.Second)
	onlyPost(t, w.NavettePostsFor("ags:proj:daily"), "waiting")

	w.TickEvery(d, At(22, 9, 30, 0), At(22, 9, 31, 0), 30*time.Second)
	posts := w.NavettePostsFor("ags:proj:daily")
	if last := posts[len(posts)-1]; last.Flags["--status"] != "waiting" || last.Flags["--title"] != "proj/daily en échec" {
		t.Fatalf("expected the failure to stay what waits for the user, got %+v", posts)
	}
}

func TestACatchUpThatSucceedsAfterAFailedManualRunLeavesNothingWaiting(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	withFlakyCommand(t, w)
	w.SharedFile(strings.Replace(dailyFlaky, "never", "12h", 1))
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 8, 59, 40))

	w.Write("broken", "")
	w.Now = At(19, 10, 0, 0)
	w.CLI("run", "proj/daily")
	os.Remove(filepath.Join(w.ProjectDir, "broken"))
	w.TickEvery(d, At(19, 11, 0, 0), At(19, 11, 2, 0), 30*time.Second)

	if got := Statuses(w.Runs()); len(got) != 2 || got[0] != "succeeded" {
		t.Fatalf("expected the 09:00 occurrence caught up after the manual run, got %v", got)
	}
	if posts := w.NavettePostsFor("ags:proj:daily"); len(posts) > 0 && posts[len(posts)-1].Flags["--status"] != "done" {
		t.Fatalf("the catch-up ran after the failed manual run and succeeded: nothing waits, got %+v", posts)
	}
}

func TestALongFailureStartedByAManualRunThenACatchUpIsNotRepostedWhenTheManualRunLeavesTheLookBack(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	withFlakyCommand(t, w)
	w.Write("broken", "")
	w.SharedFile(strings.Replace(strings.Replace(dailyFlaky, "0 9 * * *", "*/5 * * * *", 1), "never", "12h", 1))
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 9, 59, 40))
	w.Now = At(19, 10, 0, 20)
	w.CLI("run", "proj/daily")
	w.TickEvery(d, At(19, 10, 0, 50), At(19, 12, 40, 0), 30*time.Second)

	posts := w.NavettePostsFor("ags:proj:daily")
	if len(posts) > 3 {
		t.Fatalf("expected a post at the start then at most one an hour while the series grows, got %d: %+v", len(posts), posts)
	}
}
