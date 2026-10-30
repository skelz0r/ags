package daemon_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
)

const cveWatch = `version: 1
timezone: Europe/Paris
jobs:
  cve-watch:
    schedule: "0 9 * * *"
    command: agent
    precheck: echo CVE-2026-76024
    prompt: "Handle {precheck.output}."
`

func runCVEWatch(t *testing.T) (*World, Run) {
	t.Helper()
	w := New(t)
	w.SharedFile(cveWatch)
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 8, 59, 40))
	w.Tick(d, At(19, 9, 0, 10))
	d.Wait()
	runs := w.Runs()
	if len(runs) != 1 || runs[0].Status != "succeeded" {
		t.Fatalf("expected one succeeded run, got %v", Statuses(runs))
	}
	return w, runs[0]
}

func TestTheAgentIsToldHowToReportItsRun(t *testing.T) {
	w, run := runCVEWatch(t)

	prompt := w.Show(run.ID)["prompt"].(string)
	if !strings.Contains(prompt, "Handle CVE-2026-76024.") || !strings.Contains(prompt, "AGS_HOME="+w.Env["AGS_HOME"]+" ags report "+run.ID+" --status") {
		t.Fatalf("expected the prompt to end with the ags report instruction for run %s, got:\n%s", run.ID, prompt)
	}
}

func TestAnAgentReportingACommitShowsInRunsShowAndTheDaemonLog(t *testing.T) {
	w, run := runCVEWatch(t)
	if table := w.MustCLI("runs").Stdout; !strings.Contains(table, "awaited") {
		t.Fatalf("expected the run to await its report, got:\n%s", table)
	}

	w.Now = At(19, 9, 20, 0)
	w.MustCLI("report", run.ID, "--status", "done", "--summary", "Chrome bumped to 142.0.7444.59.", "--ref", "a1b2c3d")

	if table := w.MustCLI("runs").Stdout; !strings.Contains(table, " done ") {
		t.Fatalf("expected ags runs to show the report, got:\n%s", table)
	}
	report := w.Show(run.ID)["report"].(map[string]any)
	if report["status"] != "done" || report["summary"] != "Chrome bumped to 142.0.7444.59." || report["ref"] != "a1b2c3d" {
		t.Fatalf("expected the report in ags show, got %v", report)
	}
	if !strings.Contains(daemonLog(t, w), "proj/cve-watch: run "+run.ID+" reported done: Chrome bumped to 142.0.7444.59. (a1b2c3d)") {
		t.Fatalf("expected a reported line in daemon.log, got:\n%s", daemonLog(t, w))
	}
}

func TestAnAgentWithNothingToDoReportsWithoutRef(t *testing.T) {
	w, run := runCVEWatch(t)

	w.MustCLI("report", run.ID, "--status", "nothing", "--summary", "Chrome is not installed on this machine.")

	report := w.Show(run.ID)["report"].(map[string]any)
	if report["status"] != "nothing" || report["ref"] != nil {
		t.Fatalf("expected a report with no ref, got %v", report)
	}
}

func TestTheLastReportOfARunWins(t *testing.T) {
	w, run := runCVEWatch(t)

	w.MustCLI("report", run.ID, "--status", "blocked", "--summary", "Needs a reboot.")
	w.MustCLI("report", run.ID, "--status", "done", "--summary", "Rebooted and patched.")

	if report := w.Show(run.ID)["report"].(map[string]any); report["status"] != "done" {
		t.Fatalf("expected the last report to win, got %v", report)
	}
}

func TestDoctorFlagsARunWithoutReportPastTheDelay(t *testing.T) {
	w, run := runCVEWatch(t)

	w.Now = At(20, 8, 0, 0)
	if res := w.CLI("doctor"); strings.Contains(res.Stdout, "report proj/cve-watch") {
		t.Fatalf("a run 23 h old is still within its delay, got:\n%s", res.Stdout)
	}
	w.Now = At(20, 9, 1, 0)
	if res := w.CLI("doctor"); !strings.Contains(res.Stdout, "FAIL report proj/cve-watch: run "+run.ID) {
		t.Fatalf("expected doctor to flag the unreported run, got:\n%s", res.Stdout)
	}
	w.MustCLI("report", run.ID, "--status", "nothing", "--summary", "Not concerned.")
	if res := w.CLI("doctor"); strings.Contains(res.Stdout, "report proj/cve-watch") {
		t.Fatalf("a reported run is no longer flagged, got:\n%s", res.Stdout)
	}
}

func TestAReportOnARunThatNeverStartedItsCommandIsRefused(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  watch:
    schedule: "0 9 * * *"
    precheck: "exit 1"
    prompt: Watch.
`)
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 8, 59, 40))
	w.Tick(d, At(19, 9, 0, 10))
	d.Wait()

	res := w.CLI("report", w.Runs()[0].ID, "--status", "done", "--summary", "Done.", "--json")
	if res.Exit != 1 || res.Error.Code != "not_reportable" {
		t.Fatalf("expected not_reportable, got %d %+v", res.Exit, res.Error)
	}
	if res := w.CLI("report", "20261019T000000Z-000000", "--status", "done", "--summary", "Done.", "--json"); res.Error == nil || res.Error.Code != "not_found" {
		t.Fatalf("expected not_found for an unknown run, got %+v", res.Error)
	}
}

func TestAReportSentWhileTheCommandStillRunsSurvivesItsEnd(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  audit:
    schedule: "0 9 * * *"
    command: waiting-agent
    prompt: Audit.
`)
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 8, 59, 40))
	w.Tick(d, At(19, 9, 0, 10))
	var running []Run
	for i := 0; i < 100 && len(running) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		running = w.Runs("--status", "running")
	}
	if len(running) != 1 {
		t.Fatal("expected the run to be running")
	}
	for i := 0; i < 100 && w.Show(running[0].ID)["argv"] == nil; i++ {
		time.Sleep(20 * time.Millisecond)
	}

	w.MustCLI("report", running[0].ID, "--status", "done", "--summary", "Audited.")
	w.Write(filepath.Join(w.Paths.JobsDir(), "proj", "audit", "go"), "")
	d.Wait()

	detail := w.Show(running[0].ID)
	if detail["status"] != "succeeded" || detail["report"] == nil {
		t.Fatalf("expected the succeeded run to keep its report, got status %v report %v", detail["status"], detail["report"])
	}
}
