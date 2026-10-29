package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
)

func TestAnInvalidProjectFileKeepsItsLastValidVersionInService(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  check:
    schedule: "* * * * *"
    prompt: Check.
`)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 0), 30*time.Second)

	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  check:
    schedule: "* * * * *"
    prompt: Check.
    shedule: typo
`)
	w.TickEvery(d, At(19, 10, 0, 30), At(19, 10, 2, 0), 30*time.Second)
	d.Wait()

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "succeeded,succeeded,succeeded" {
		t.Fatalf("expected the last valid version to keep running, got %v", got)
	}
	if w.Notes.Count("invalid") != 1 {
		t.Fatalf("expected the error notified once, not per tick, got %d", w.Notes.Count("invalid"))
	}
	var doctor []struct {
		Name   string `json:"name"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	w.CLI("doctor", "--json").Decode(t, &doctor)
	found := false
	for _, c := range doctor {
		if c.Name == "daemon sees proj" && strings.Contains(c.Detail, ".ags.yml:7: job \"check\": unknown key \"shedule\"") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the error is shown with file and line until fixed, got %+v", doctor)
	}
}

func TestAProjectInvalidFromTheStartRunsNothing(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\njobs:\n  check:\n    schedule: \"* * * * *\"\n    prompt: Check.\n")
	w.Register()
	w.SharedFile("version: 1\njobs:\n  check:\n    schedule: \"* * * * *\"\n")
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 1, 0), 30*time.Second)
	if runs := w.Runs(); len(runs) != 0 {
		t.Fatalf("expected nothing to run, got %v", Statuses(runs))
	}
}

func TestAJobKeptFromALastValidVersionRunsWithTheCommandItWasValidatedWith(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\ntimezone: Europe/Paris\njobs:\n  check:\n    schedule: \"* * * * *\"\n    prompt: Check.\n    command: failing\n")
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 0), 30*time.Second)

	w.Write(w.Paths.MachineConfig(), strings.Replace(MachineConfig, "  failing:\n", "  renamed:\n", 1))
	w.TickEvery(d, At(19, 10, 0, 30), At(19, 10, 1, 0), 30*time.Second)

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "failed (exit 3),failed (exit 3)" {
		t.Fatalf("expected the kept version to run its validated command, got %v", got)
	}
}

func TestAProjectFixedByCreatingAMissingPromptFileIsReloaded(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\ntimezone: Europe/Paris\njobs:\n  check:\n    schedule: \"* * * * *\"\n    prompt_file: .ags/check.md\n")
	w.Write(".ags/check.md", "Check.\n")
	w.Register()
	removeFile(t, w, ".ags/check.md")
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 0), 30*time.Second)

	w.Write(".ags/check.md", "Check.\n")
	w.TickEvery(d, At(19, 10, 0, 30), At(19, 10, 1, 0), 30*time.Second)

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "succeeded" {
		t.Fatalf("expected the project to come back once its prompt file exists, got %v", got)
	}
}

func removeFile(t *testing.T, w *World, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(w.ProjectDir, rel)); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorReportsAJobWhosePrecheckCrashesUntilItWorksAgain(t *testing.T) {
	w := New(t)
	w.SharedFile(watchJob("*/5 * * * *", `if [ -f broken ]; then echo boom >&2; exit 1; fi; echo nothing new; exit 1`))
	w.Write("broken", "")
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 7, 59, 0))
	w.TickEvery(d, At(19, 8, 0, 0), At(19, 8, 1, 0), time.Minute)

	precheckCheck := func() string {
		var doctor []struct {
			Name   string `json:"name"`
			OK     bool   `json:"ok"`
			Detail string `json:"detail"`
		}
		w.CLI("doctor", "--json").Decode(t, &doctor)
		for _, c := range doctor {
			if c.Name == "precheck proj/watch" && !c.OK {
				return c.Detail
			}
		}
		return ""
	}
	if detail := precheckCheck(); !strings.Contains(detail, "precheck_error") || !strings.Contains(detail, "precheck exit 1: boom") {
		t.Fatalf("expected doctor to report the crashing precheck, got %q", detail)
	}

	w.MustCLI("run", "proj/watch", "--skip-precheck")
	if detail := precheckCheck(); detail == "" {
		t.Fatalf("a run that skipped the precheck says nothing about it: doctor must still report it")
	}

	os.Remove(filepath.Join(w.ProjectDir, "broken"))
	w.TickEvery(d, At(19, 8, 2, 0), At(19, 8, 6, 0), time.Minute)
	if detail := precheckCheck(); detail != "" {
		t.Fatalf("expected doctor quiet once the precheck skips normally, got %q", detail)
	}
}
