package daemon_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
)

func checkJob(schedule, prompt, precheck string) string {
	return fmt.Sprintf(`version: 1
timezone: Europe/Paris
jobs:
  check:
    schedule: "%s"
    prompt: %s
    precheck: %s
`, schedule, prompt, precheck)
}

func TestASharedJobNeverApprovedDoesNotRun(t *testing.T) {
	w := New(t)
	w.SharedFile(checkJob("* * * * *", "Check.", "true"))
	w.MustCLI("add", w.ProjectDir, "--name", "proj")
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 2, 0), 30*time.Second)
	d.Wait()

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "skipped (unapproved) x3" {
		t.Fatalf("expected every occurrence skipped (unapproved), folded into one entry, got %v", got)
	}
	if w.Notes.Count("unapproved") != 1 {
		t.Fatalf("expected one notification for the change, got %d", w.Notes.Count("unapproved"))
	}

	w.MustCLI("approve", "proj/check", "--yes")
	w.TickEvery(d, At(19, 10, 2, 30), At(19, 10, 3, 0), 30*time.Second)
	d.Wait()
	if runs := w.Runs(); runs[0].Status != "succeeded" {
		t.Fatalf("once approved the job runs, got %v", Statuses(runs))
	}
}

func TestAChangedPromptOrPrecheckNeedsANewApproval(t *testing.T) {
	for name, changed := range map[string]string{
		"prompt":   checkJob("* * * * *", "Check something else.", "true"),
		"precheck": checkJob("* * * * *", "Check.", "test -d ."),
	} {
		t.Run(name, func(t *testing.T) {
			w := New(t)
			w.SharedFile(checkJob("* * * * *", "Check.", "true"))
			w.Register()
			d := w.Daemon()
			w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 0), 30*time.Second)
			d.Wait()

			w.SharedFile(changed)
			w.TickEvery(d, At(19, 10, 0, 30), At(19, 10, 1, 0), 30*time.Second)
			d.Wait()

			if got := Statuses(w.Runs()); strings.Join(got, ",") != "succeeded,skipped (unapproved)" {
				t.Fatalf("expected the changed job to stop until approved, got %v", got)
			}
			var approval struct {
				Pending []struct {
					Diff string `json:"diff"`
				} `json:"pending"`
			}
			w.MustCLI("approve", "proj", "--yes", "--json").Decode(t, &approval)
			if len(approval.Pending) != 1 || !strings.Contains(approval.Pending[0].Diff, "+ ") {
				t.Fatalf("approving shows the diff against the approved version, got %+v", approval)
			}
		})
	}
}

func TestAScheduleChangeAloneNeedsNoApproval(t *testing.T) {
	w := New(t)
	w.SharedFile(checkJob("0 9 * * *", "Check.", "true"))
	w.Register()
	w.SharedFile(checkJob("* * * * *", "Check.", "true"))
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 0), 30*time.Second)
	d.Wait()

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "succeeded" {
		t.Fatalf("expected the rescheduled job to run, got %v", got)
	}
}

func TestALocalJobNeverNeedsApproval(t *testing.T) {
	w := New(t)
	w.LocalFile(`jobs:
  mine:
    schedule: "* * * * *"
    prompt: Mine.
`)
	w.MustCLI("add", w.ProjectDir, "--name", "proj")
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 0), 30*time.Second)
	d.Wait()

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "succeeded" {
		t.Fatalf("expected the local job to run, got %v", got)
	}
}

func TestALocalOverrideOfASharedJobKeepsItsApproval(t *testing.T) {
	w := New(t)
	w.SharedFile(checkJob("0 9 * * *", "Check.", "true"))
	w.Register()
	w.LocalFile(`jobs:
  check:
    schedule: "* * * * *"
    precheck: "false"
`)
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 0), 30*time.Second)
	d.Wait()

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "skipped (precheck)" {
		t.Fatalf("expected the local precheck to run on the approved job, got %v", got)
	}
}
