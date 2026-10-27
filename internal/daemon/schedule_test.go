package daemon_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
)

func weeklyAudit(extra string) string {
	return `version: 1
timezone: Europe/Paris
jobs:
  audit:
    schedule: "30 9 * * 1"
    prompt: "Audit, late by {run.late_by}."
` + extra
}

func TestAnOccurrenceMissedDuringSleepWithinCatchUpRunsOnceAsCatchUpKnowingItsLateness(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit(""))
	w.Register()
	d := w.Daemon()

	w.Tick(d, At(19, 8, 0, 0))
	w.Tick(d, At(19, 18, 0, 0))
	w.TickEvery(d, At(19, 18, 0, 30), At(19, 18, 5, 0), 30*time.Second)
	d.Wait()

	runs := w.Runs()
	if len(runs) != 1 {
		t.Fatalf("expected one run, got %v", Statuses(runs))
	}
	run := runs[0]
	if run.Trigger != "catch_up" || run.Status != "succeeded" {
		t.Fatalf("expected a succeeded catch_up run, got %s %s", run.Trigger, run.Status)
	}
	if !run.ScheduledFor.Equal(At(19, 9, 30, 0)) {
		t.Fatalf("expected the run to be for Monday 09:30, got %s", run.ScheduledFor)
	}
	prompt := w.Show(run.ID)["prompt"].(string)
	if !strings.Contains(prompt, "Audit, late by 8h31m.") {
		t.Fatalf("the prompt should know it is 8h31m late:\n%s", prompt)
	}
}

func TestOlderOccurrencesThanTheLatestAreRecordedAsOneMissedEntryWithTheirCount(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  hourly:
    schedule: "0 * * * *"
    prompt: Check.
`)
	w.Register()
	d := w.Daemon()

	w.Tick(d, At(19, 8, 10, 0))
	w.Tick(d, At(19, 13, 20, 0))
	w.TickEvery(d, At(19, 13, 20, 30), At(19, 13, 22, 0), 30*time.Second)
	d.Wait()

	runs := w.Runs()
	if got := Statuses(runs); strings.Join(got, ",") != "missed,succeeded" {
		t.Fatalf("expected one missed entry then one run, got %v", got)
	}
	missed, caught := runs[1], runs[0]
	if missed.MissedCount != 4 || !missed.ScheduledFor.Equal(At(19, 9, 0, 0)) {
		t.Fatalf("expected 4 missed occurrences from 09:00, got %d from %s", missed.MissedCount, missed.ScheduledFor)
	}
	if caught.Trigger != "catch_up" || !caught.ScheduledFor.Equal(At(19, 13, 0, 0)) {
		t.Fatalf("expected the 13:00 occurrence caught up, got %s %s", caught.Trigger, caught.ScheduledFor)
	}
	if w.Notes.Count("missed") != 1 {
		t.Fatalf("expected one missed notification, got %d", w.Notes.Count("missed"))
	}
}

func TestBeyondCatchUpTheOccurrenceIsMissedWithoutRun(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit("    catch_up: 2h\n"))
	w.Register()
	d := w.Daemon()

	w.Tick(d, At(19, 8, 0, 0))
	w.Tick(d, At(19, 18, 0, 0))
	w.TickEvery(d, At(19, 18, 0, 30), At(19, 18, 3, 0), 30*time.Second)
	d.Wait()

	runs := w.Runs()
	if len(runs) != 1 || runs[0].Status != "missed" || runs[0].MissedCount != 1 {
		t.Fatalf("expected a single missed entry, got %+v", runs)
	}
}

func TestCatchUpNeverRecordsTheOccurrenceAsMissed(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit("    catch_up: never\n"))
	w.Register()
	d := w.Daemon()

	w.Tick(d, At(19, 8, 0, 0))
	w.Tick(d, At(19, 9, 45, 0))
	w.TickEvery(d, At(19, 9, 45, 30), At(19, 9, 48, 0), 30*time.Second)

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "missed" {
		t.Fatalf("expected missed, got %v", got)
	}
}

func TestUpToTwoTicksLateCountsAsOnTime(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit(""))
	w.Register()
	d := w.Daemon()

	w.Tick(d, At(19, 9, 29, 0))
	w.Tick(d, At(19, 9, 29, 59))
	w.Tick(d, At(19, 9, 30, 59))
	d.Wait()

	runs := w.Runs()
	if len(runs) != 1 || runs[0].Trigger != "schedule" || runs[0].LateBy != "0m" {
		t.Fatalf("expected one on-time run, got %+v", runs)
	}
}

func TestAfterAWakeFiringWaitsAMinute(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit(""))
	w.Register()
	d := w.Daemon()

	w.Tick(d, At(19, 8, 0, 0))
	w.Tick(d, At(19, 12, 0, 0))
	w.Tick(d, At(19, 12, 0, 30))
	if runs := w.Runs(); len(runs) != 0 {
		t.Fatalf("nothing should fire during the minute after a wake, got %v", Statuses(runs))
	}
	w.Tick(d, At(19, 12, 1, 0))
	d.Wait()
	if runs := w.Runs(); len(runs) != 1 || runs[0].Trigger != "catch_up" {
		t.Fatalf("expected the catch up a minute after the wake, got %+v", runs)
	}
}

func TestACrashAfterTheCursorIsPersistedMeansNotRunNeverRunTwice(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit("    command: slow\n"))
	w.Register()
	crashed := w.Daemon()
	w.Tick(crashed, At(19, 9, 29, 40))
	w.Tick(crashed, At(19, 9, 30, 10))
	runs := w.Runs()
	if len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("expected one running run before the crash, got %+v", runs)
	}
	waitForCommandStart(t, w, runs[0].ID)

	restarted := w.Daemon()
	for at := At(19, 9, 30, 40); at.Before(At(19, 9, 35, 0)); at = at.Add(30 * time.Second) {
		w.Tick(restarted, at)
	}

	runs = w.Runs()
	if len(runs) != 1 {
		t.Fatalf("the occurrence must not run a second time, got %v", Statuses(runs))
	}
	if runs[0].Status != "failed" || runs[0].Reason != "interrupted" {
		t.Fatalf("the run left running must be failed (interrupted), got %s (%s)", runs[0].Status, runs[0].Reason)
	}
}

func TestANewJobDoesNotRunForThePast(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  hourly:
    schedule: "0 * * * *"
    prompt: Check.
`)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 10, 30, 0), At(19, 10, 31, 0), 30*time.Second)

	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  hourly:
    schedule: "0 * * * *"
    prompt: Check.
  weekly:
    schedule: "30 9 * * 1"
    prompt: Audit.
`)
	w.MustCLI("approve", "proj", "--yes")
	w.TickEvery(d, At(19, 10, 31, 30), At(19, 10, 33, 0), 30*time.Second)
	d.Wait()

	if runs := w.Runs(); len(runs) != 0 {
		t.Fatalf("expected no run nor missed entry, got %v", Statuses(runs))
	}
}

func TestAnOccurrenceDueWhileTheJobStillRunsIsSkippedForOverlap(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  every-minute:
    schedule: "* * * * *"
    prompt: Check.
    command: slow
`)
	w.Register()
	d := w.Daemon()
	for _, at := range []time.Time{At(19, 9, 59, 30), At(19, 10, 0, 0), At(19, 10, 0, 30), At(19, 10, 1, 0)} {
		w.Tick(d, at)
	}

	got := Statuses(w.Runs())
	if strings.Join(got, ",") != "running,skipped (overlap)" {
		t.Fatalf("expected the second occurrence skipped for overlap, got %v", got)
	}
}

func TestACommandPastItsTimeoutIsKilledAndTheRunFails(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit("    command: short-timeout\n"))
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 9, 29, 40))
	started := time.Now()
	w.Tick(d, At(19, 9, 30, 10))
	d.Wait()

	if time.Since(started) > 10*time.Second {
		t.Fatalf("the command should have been killed after 1s")
	}
	runs := w.Runs()
	if len(runs) != 1 || runs[0].Status != "failed" || !strings.HasPrefix(runs[0].Reason, "timeout") {
		t.Fatalf("expected a failed run for timeout, got %+v", runs)
	}
	if w.Notes.Count("failed") != 1 {
		t.Fatalf("a failed run notifies once, got %d", w.Notes.Count("failed"))
	}
}

func TestAFailingCommandFailsTheRunWithItsExitCodeAndStderr(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit("    command: failing\n"))
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 9, 29, 40))
	w.Tick(d, At(19, 9, 30, 10))
	d.Wait()

	runs := w.Runs()
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].ExitCode == nil || *runs[0].ExitCode != 3 {
		t.Fatalf("expected a failed run with exit 3, got %+v", runs)
	}
	if stderr := w.Show(runs[0].ID)["stderr"]; stderr != "broken\n" {
		t.Fatalf("expected stderr kept, got %q", stderr)
	}
}

func TestPrecheckDecidesWhetherTheJobRuns(t *testing.T) {
	cases := []struct {
		name     string
		precheck string
		status   string
		reason   string
	}{
		{"exit 0 runs the job", "true", "succeeded", ""},
		{"exit 1 skips it with stdout as the reason", "echo nothing new; exit 1", "skipped", "precheck: nothing new"},
		{"another exit code is a precheck error", "exit 2", "precheck_error", "precheck exit 2"},
		{"a timeout is a precheck error", "sleep 5", "precheck_error", "precheck timeout after 1s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := New(t)
			w.SharedFile(weeklyAudit("    precheck: " + c.precheck + "\n"))
			w.Register()
			d := w.Daemon()
			w.Tick(d, At(19, 9, 29, 40))
			w.Tick(d, At(19, 9, 30, 10))
			d.Wait()

			runs := w.Runs()
			if len(runs) != 1 || runs[0].Status != c.status || runs[0].Reason != c.reason {
				t.Fatalf("expected %s (%s), got %+v", c.status, c.reason, runs)
			}
			if c.status == "precheck_error" && w.Notes.Count("precheck_error") != 1 {
				t.Fatalf("a precheck error notifies")
			}
			if c.status != "succeeded" {
				if stdout := w.Show(runs[0].ID)["stdout"]; stdout != "" {
					t.Fatalf("the command must not run, stdout %q", stdout)
				}
			}
		})
	}
}

func TestPrecheckRunsInTheProjectWithTheRunEnvironment(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit(`    precheck: 'echo "$AGS_PROJECT/$AGS_JOB $AGS_TRIGGER $(basename "$PWD")"; exit 1'` + "\n"))
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 9, 29, 40))
	w.Tick(d, At(19, 9, 30, 10))
	d.Wait()

	if runs := w.Runs(); runs[0].Reason != "precheck: proj/audit schedule proj" {
		t.Fatalf("unexpected precheck output %q", runs[0].Reason)
	}
}

func TestWhatThePrecheckPrintsFillsThePromptWhereItAsksForIt(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  audit:
    schedule: "30 9 * * 1"
    prompt: |
      New items:
      {precheck.output}
      Triage them.
    precheck: "printf 'item #12 {job.name}\\nitem #13\\n\\n'"
`)
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 9, 29, 40))
	w.Tick(d, At(19, 9, 30, 10))
	d.Wait()

	runs := w.Runs()
	if len(runs) != 1 || runs[0].Status != "succeeded" {
		t.Fatalf("expected one succeeded run, got %+v", runs)
	}
	want := "Machine preprompt.\n\nNew items:\nitem #12 {job.name}\nitem #13\nTriage them.\n"
	if prompt := w.Show(runs[0].ID)["prompt"]; prompt != want {
		t.Fatalf("the precheck output goes in as printed, never templated:\n%q", prompt)
	}
}

func TestAPrecheckOutputTooLongForThePromptStopsTheRunInsteadOfLosingItsEnd(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		status string
		reason string
	}{
		{"a prompt using the output is not run", `"{precheck.output}"`, "precheck_error", "precheck output over 64 KiB, the limit of {precheck.output}"},
		{"a prompt not using it runs", `"Audit."`, "succeeded", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := New(t)
			w.SharedFile(strings.Replace(weeklyAudit("    precheck: yes item | head -c 70000\n"), `"Audit, late by {run.late_by}."`, c.prompt, 1))
			w.Register()
			d := w.Daemon()
			w.Tick(d, At(19, 9, 29, 40))
			w.Tick(d, At(19, 9, 30, 10))
			d.Wait()

			runs := w.Runs()
			if len(runs) != 1 || runs[0].Status != c.status || runs[0].Reason != c.reason {
				t.Fatalf("expected %s (%s), got %+v", c.status, c.reason, runs)
			}
			if c.status == "precheck_error" && w.Notes.Count("precheck_error") != 1 {
				t.Fatalf("an output too long for the prompt notifies")
			}
		})
	}
}

func TestAJobKeepsADirectoryOfItsOwnAcrossRunsSharedByPrecheckAndCommand(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  watch:
    schedule: "0 * * * *"
    prompt: Watch.
    command: memory
    precheck: echo "$AGS_TRIGGER" >> "$AGS_JOB_STATE_DIR/seen"
`)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 8, 59, 30), At(19, 10, 0, 30), 30*time.Second)
	w.MustCLI("run", "proj/watch")

	runs := w.Runs()
	if got := Statuses(runs); strings.Join(got, ",") != "succeeded,succeeded,succeeded" {
		t.Fatalf("expected three succeeded runs, got %v", got)
	}
	if stdout := w.Show(runs[0].ID)["stdout"]; stdout != "schedule\nschedule\nmanual\n" {
		t.Fatalf("each run, manual ones included, should find what the previous ones left, got %q", stdout)
	}
	dir := filepath.Join(w.Paths.StateDir, "jobs", "proj", "watch")
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("expected %s readable by its owner only, got %v %v", dir, info, err)
	}
}

func TestAPausedJobRecordsItsOccurrencesAsSkipped(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit(""))
	w.Register()
	w.MustCLI("pause", "proj/audit")
	d := w.Daemon()
	w.Tick(d, At(19, 9, 29, 40))
	w.Tick(d, At(19, 9, 30, 10))

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "skipped (paused)" {
		t.Fatalf("expected skipped (paused), got %v", got)
	}
	local, _ := os.ReadFile(filepath.Join(w.ProjectDir, ".ags.local.yml"))
	if !strings.Contains(string(local), "enabled: false") {
		t.Fatalf("pausing writes to the local file, got %q", local)
	}
}

func TestATimeoutKillsTheWholeProcessGroupEvenAChildIgnoringSIGTERM(t *testing.T) {
	w := New(t)
	w.SharedFile(weeklyAudit("    command: stubborn\n"))
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 9, 29, 40))
	w.Tick(d, At(19, 9, 30, 10))
	d.Wait()

	data, err := os.ReadFile(filepath.Join(w.ProjectDir, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	time.Sleep(100 * time.Millisecond)
	if syscall.Kill(pid, 0) == nil {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("the child %d survived the timeout", pid)
	}
	if runs := w.Runs(); runs[0].Status != "failed" || !strings.HasPrefix(runs[0].Reason, "timeout") {
		t.Fatalf("expected failed (timeout), got %+v", runs[0])
	}
}

func waitForCommandStart(t *testing.T, w *World, id string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if argv, _ := w.Show(id)["argv"].([]any); len(argv) > 0 {
			if _, err := os.Stat(filepath.Join(w.Show(id)["dir"].(string), "stdout")); err == nil {
				return
			}
		}
	}
	t.Fatalf("the command of run %s never started", id)
}
