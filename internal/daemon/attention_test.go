package daemon_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
)

type attentionItem struct {
	Kind        string    `json:"kind"`
	Key         string    `json:"key"`
	Count       int       `json:"count"`
	Since       time.Time `json:"since"`
	Summary     string    `json:"summary"`
	Run         string    `json:"run"`
	Ref         string    `json:"ref"`
	File        string    `json:"file"`
	Line        int       `json:"line"`
	Fingerprint string    `json:"fingerprint"`
}

type attention struct {
	Daemon struct {
		Alive bool `json:"alive"`
	} `json:"daemon"`
	Items  []attentionItem `json:"items"`
	Recent []struct {
		Run     string `json:"run"`
		Summary string `json:"summary"`
		Ref     string `json:"ref"`
	} `json:"recent"`
}

func readAttention(t *testing.T, w *World) attention {
	t.Helper()
	res := w.CLI("attention", "--json")
	if res.Exit != 0 || !res.OK {
		t.Fatalf("ags attention must succeed whatever waits, got %d %+v", res.Exit, res.Error)
	}
	var at attention
	res.Decode(t, &at)
	return at
}

func itemsOf(at attention, kind string) []attentionItem {
	var out []attentionItem
	for _, i := range at.Items {
		if i.Kind == kind {
			out = append(out, i)
		}
	}
	return out
}

func runNextDay(t *testing.T, w *World, previous Run) Run {
	t.Helper()
	d := w.Daemon()
	w.TickEvery(d, At(20, 8, 59, 0), At(20, 9, 1, 0), 30*time.Second)
	latest := w.Runs()[0]
	if latest.ID == previous.ID || latest.Status != "succeeded" {
		t.Fatalf("expected a second hand-off the next day, got %v", Statuses(w.Runs()))
	}
	return latest
}

func TestTwentyCrashesOfAPrecheckAreOneItemSayingSinceWhenUntilItWorksAgain(t *testing.T) {
	w := New(t)
	w.SharedFile(watchJob("*/5 * * * *", `if [ -f broken ]; then echo boom >&2; exit 1; fi; echo nothing new; exit 1`))
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 8, 59, 0), At(19, 8, 59, 0), time.Minute)
	w.Write("broken", "")
	w.TickEvery(d, At(19, 9, 0, 0), At(19, 10, 39, 0), time.Minute)

	errors := itemsOf(readAttention(t, w), "precheck_error")
	if len(errors) != 1 || errors[0].Key != "proj/watch" || errors[0].Count != 20 || !errors[0].Since.Equal(At(19, 9, 0, 0)) || errors[0].Summary != "precheck exit 1: boom" {
		t.Fatalf("expected one item for the 20 crashes since 09:00, got %+v", errors)
	}

	os.Remove(filepath.Join(w.ProjectDir, "broken"))
	w.TickEvery(d, At(19, 10, 40, 0), At(19, 10, 45, 0), time.Minute)
	if errors := itemsOf(readAttention(t, w), "precheck_error"); len(errors) != 0 {
		t.Fatalf("a precheck that skips normally again needs nothing, got %+v", errors)
	}
}

func TestABlockedReportWaitsUntilALaterRunOfTheJobReportsDone(t *testing.T) {
	w, run := runCVEWatch(t)
	w.Now = At(19, 9, 10, 0)
	w.MustCLI("report", run.ID, "--status", "blocked", "--summary", "Needs a token to bump Chrome.", "--ref", "https://example.com/pr/1")

	blocked := itemsOf(readAttention(t, w), "blocked")
	if len(blocked) != 1 || blocked[0].Run != run.ID || blocked[0].Ref != "https://example.com/pr/1" || blocked[0].Summary != "Needs a token to bump Chrome." {
		t.Fatalf("expected the blocked report with its ref, got %+v", blocked)
	}

	latest := runNextDay(t, w, run)
	if blocked := itemsOf(readAttention(t, w), "blocked"); len(blocked) != 1 {
		t.Fatalf("a hand-off still awaiting its report settles nothing, got %+v", blocked)
	}

	w.Now = At(20, 9, 30, 0)
	w.MustCLI("report", latest.ID, "--status", "done", "--summary", "Chrome bumped.", "--ref", "a1b2c3d")
	at := readAttention(t, w)
	if blocked := itemsOf(at, "blocked"); len(blocked) != 0 {
		t.Fatalf("a later done report settles the blocked one, got %+v", blocked)
	}
	if len(at.Recent) != 1 || at.Recent[0].Run != latest.ID || at.Recent[0].Ref != "a1b2c3d" {
		t.Fatalf("expected the done report among the recent ones, got %+v", at.Recent)
	}

	w.Now = At(21, 9, 31, 0)
	if at := readAttention(t, w); len(at.Recent) != 0 {
		t.Fatalf("a done report older than 24 h is no longer recent, got %+v", at.Recent)
	}
}

func TestAHandOffWithoutReportWaitsUntilALaterRunOfTheJobReports(t *testing.T) {
	w, run := runCVEWatch(t)
	w.Now = At(20, 9, 1, 0)
	missing := itemsOf(readAttention(t, w), "no_report")
	if len(missing) != 1 || missing[0].Run != run.ID {
		t.Fatalf("expected the hand-off past its delay, got %+v", missing)
	}

	latest := runNextDay(t, w, run)
	w.MustCLI("report", latest.ID, "--status", "nothing", "--summary", "No new CVE.")
	if missing := itemsOf(readAttention(t, w), "no_report"); len(missing) != 0 {
		t.Fatalf("a later reported run supersedes the silent one, got %+v", missing)
	}
}

func TestAnUnapprovedJobCarriesTheFingerprintTheApprovalExpects(t *testing.T) {
	w := New(t)
	w.SharedFile(cveWatch)
	w.Register()
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  cve-watch:
    schedule: "0 9 * * *"
    command: agent
    precheck: echo CVE-2026-76024
    prompt: "Handle {precheck.output} and open an issue."
`)

	unapproved := itemsOf(readAttention(t, w), "unapproved")
	if len(unapproved) != 1 || unapproved[0].Key != "proj/cve-watch" || unapproved[0].Fingerprint == "" || unapproved[0].Line == 0 {
		t.Fatalf("expected the changed job with its fingerprint and line, got %+v", unapproved)
	}
	w.MustCLI("approve", "proj/cve-watch", "--yes", "--fingerprints", unapproved[0].Fingerprint)
	if unapproved := itemsOf(readAttention(t, w), "unapproved"); len(unapproved) != 0 {
		t.Fatalf("an approved job waits no more, got %+v", unapproved)
	}
}

func TestAnInvalidProjectFileShowsWithItsFileAndLine(t *testing.T) {
	w := New(t)
	w.SharedFile(cveWatch)
	w.Register()
	w.SharedFile("version: 1\njobs:\n  cve-watch:\n    schedule: \"0 9 * * *\"\n    prompt: Watch.\n    colour: blue\n")

	at := readAttention(t, w)
	invalid := itemsOf(at, "config")
	if len(invalid) != 1 || filepath.Base(invalid[0].File) != ".ags.yml" || invalid[0].Line != 6 {
		t.Fatalf("expected the unknown key with its file and line, got %+v", invalid)
	}
	if daemon := itemsOf(at, "daemon"); len(daemon) != 1 || at.Daemon.Alive {
		t.Fatalf("a daemon that never ticked waits for the user, got %+v", daemon)
	}
}

func TestACatchUpRunThatEndsBlockedAfterAManualDoneRunStillWaits(t *testing.T) {
	w := New(t)
	w.SharedFile(cveWatch)
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 8, 59, 40))

	w.Now = At(19, 10, 0, 0)
	w.MustCLI("run", "proj/cve-watch")
	manual := w.Runs()[0]
	w.MustCLI("report", manual.ID, "--status", "done", "--summary", "Handled by hand.")

	w.TickEvery(d, At(19, 11, 0, 0), At(19, 11, 2, 0), 30*time.Second)
	var catchUp Run
	for _, r := range w.Runs() {
		if r.Trigger == "catch_up" {
			catchUp = r
		}
	}
	if catchUp.ID == "" {
		t.Fatalf("expected the 09:00 occurrence caught up at 11:00, got %v", Statuses(w.Runs()))
	}
	w.Now = At(19, 11, 5, 0)
	w.MustCLI("report", catchUp.ID, "--status", "blocked", "--summary", "Needs a token.")

	if blocked := itemsOf(readAttention(t, w), "blocked"); len(blocked) != 1 || blocked[0].Run != catchUp.ID {
		t.Fatalf("the catch-up ran after the manual run: its blocked report waits, got %+v", blocked)
	}
}

func TestOccurrencesMissedWhileTheMachineSleptWaitUntilTheJobRunsAgain(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  audit:
    schedule: "0 9 * * *"
    prompt: Audit.
`)
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 8, 59, 40))
	w.TickEvery(d, At(20, 22, 0, 0), At(20, 22, 2, 0), 30*time.Second)

	missed := itemsOf(readAttention(t, w), "missed")
	if len(missed) != 1 || missed[0].Key != "proj/audit" || missed[0].Count != 2 || !missed[0].Since.Equal(At(19, 9, 0, 0)) {
		t.Fatalf("expected the two occurrences past catch_up as one item since the 19th, got %+v", missed)
	}

	w.TickEvery(d, At(21, 8, 59, 0), At(21, 9, 1, 0), 30*time.Second)
	if missed := itemsOf(readAttention(t, w), "missed"); len(missed) != 0 {
		t.Fatalf("a run after the missed occurrences settles them, got %+v", missed)
	}
}
