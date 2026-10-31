package daemon_test

import (
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
)

func TestAReportArrivingWhileTheFeedFollowsShowsOnceWithItsLink(t *testing.T) {
	w, run := runCVEWatch(t)
	w.MustCLI("report", run.ID, "--status", "blocked", "--summary", "Needs a reboot.")
	feed := w.Feed()
	feed.WaitFor("Needs a reboot.")

	w.MustCLI("report", run.ID, "--status", "done", "--summary", "CVE-2026-76024 patched, Chrome bumped.", "--ref", "https://github.com/acme/app/pull/12")
	feed.WaitFor("→ https://github.com/acme/app/pull/12")
	w.MustCLI("report", run.ID, "--status", "done", "--summary", "CVE-2026-76024 patched, Chrome bumped.", "--ref", "https://github.com/acme/app/pull/12")
	time.Sleep(100 * time.Millisecond)
	w.MustCLI("report", run.ID, "--status", "blocked", "--summary", "Chrome crashes on start.")

	out := feed.WaitFor("Chrome crashes on start.")
	if strings.Count(out, "CVE-2026-76024 patched") != 1 || strings.Count(out, "Needs a reboot.") != 1 || !strings.Contains(out, " done ") {
		t.Fatalf("expected each report once, the repeated one included, got:\n%s", out)
	}
	if out := w.MustCLI("feed", "--no-follow").Stdout; strings.Count(out, "proj/cve-watch") != 1 || !strings.Contains(out, "Chrome crashes on start.") {
		t.Fatalf("expected one entry on start, the latest report, got:\n%s", out)
	}
}

func TestAHistoryReplacedWhileFollowingDoesNotBringBackAReplacedReport(t *testing.T) {
	w, run := runCVEWatch(t)
	w.MustCLI("report", run.ID, "--status", "blocked", "--summary", "Needs a reboot.")
	w.MustCLI("report", run.ID, "--status", "done", "--summary", "Rebooted and patched.")
	feed := w.Feed()
	feed.WaitFor("Rebooted and patched.")

	history, err := os.ReadFile(w.Paths.History())
	if err != nil {
		t.Fatal(err)
	}
	replacement := w.Paths.History() + ".new"
	if err := os.WriteFile(replacement, history, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, w.Paths.History()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	w.MustCLI("report", run.ID, "--status", "done", "--summary", "Checked again after the reboot.")

	out := feed.WaitFor("Checked again after the reboot.")
	if strings.Contains(out, "Needs a reboot.") || strings.Count(out, "Rebooted and patched.") != 1 {
		t.Fatalf("expected the replayed history to show nothing already shown or replaced, got:\n%s", out)
	}
}

func TestAFoldedSeriesOfSkipsShowsOnceWithAll(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  tick:
    schedule: "* * * * *"
    prompt: Tick.
`)
	w.Register()
	w.MustCLI("pause", "proj/tick")
	d := w.Daemon()
	w.Tick(d, At(19, 8, 59, 40))
	w.Tick(d, At(19, 9, 0, 10))
	feed := w.Feed("--all")
	feed.WaitFor("skipped")

	w.TickEvery(d, At(19, 9, 1, 10), At(19, 9, 3, 10), time.Minute)
	w.MustCLI("resume", "proj/tick")
	w.TickEvery(d, At(19, 9, 4, 10), At(19, 9, 4, 10), time.Minute)

	out := feed.WaitFor("succeeded")
	if strings.Count(out, "skipped") != 1 {
		t.Fatalf("expected the folded series of skips once, got:\n%s", out)
	}
}

func TestNothingReportsAndSkipsShowOnlyWithAll(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  quiet:
    schedule: "0 9 * * *"
    precheck: exit 1
    prompt: Never runs.
  cve-watch:
    schedule: "0 9 * * *"
    command: agent
    prompt: Watch.
`)
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 8, 59, 40))
	w.Tick(d, At(19, 9, 0, 10))
	d.Wait()
	var watch Run
	for _, r := range w.Runs() {
		if r.Job == "cve-watch" {
			watch = r
		}
	}
	w.MustCLI("report", watch.ID, "--status", "nothing", "--summary", "No new CVE today.")

	if out := w.MustCLI("feed", "--no-follow").Stdout; out != "" {
		t.Fatalf("expected skips and nothing reports hidden by default, got:\n%s", out)
	}
	out := w.MustCLI("feed", "--no-follow", "--all").Stdout
	if !strings.Contains(out, "proj/quiet") || !strings.Contains(out, "skipped") || !strings.Contains(out, "No new CVE today.") {
		t.Fatalf("expected --all to show the skip and the nothing report, got:\n%s", out)
	}
}

func TestAPrecheckErrorShowsInTheFeed(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  broken:
    schedule: "0 9 * * *"
    precheck: 'echo "gh: not logged in" >&2; exit 4'
    prompt: Never runs.
`)
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 8, 59, 40))
	w.Tick(d, At(19, 9, 0, 10))
	d.Wait()

	out := w.MustCLI("feed", "--no-follow").Stdout
	if !strings.Contains(out, "proj/broken") || !strings.Contains(out, "ERROR") || !strings.Contains(out, "gh: not logged in") {
		t.Fatalf("expected the precheck error in the feed, got:\n%s", out)
	}
}

func TestARunStillWithoutReportPastItsDelayShowsAsNoReport(t *testing.T) {
	w, _ := runCVEWatch(t)

	w.Now = At(20, 9, 1, 0)
	out := w.MustCLI("feed", "--no-follow").Stdout
	if !strings.Contains(out, "proj/cve-watch") || !strings.Contains(out, "no report") {
		t.Fatalf("expected the unreported hand-off as no report, got:\n%s", out)
	}
}

func TestTheFeedStreamsJSONLines(t *testing.T) {
	w, run := runCVEWatch(t)
	w.MustCLI("report", run.ID, "--status", "done", "--summary", "Patched.", "--ref", "a1b2c3d")

	out := w.MustCLI("feed", "--no-follow", "--json").Stdout
	if want := `{"ok":true,"data":{"at":`; !strings.HasPrefix(out, want) || strings.Count(out, "\n") != 1 || !strings.Contains(out, `"source":"agent","status":"done","summary":"Patched.","ref":"a1b2c3d"`) {
		t.Fatalf("expected one JSON envelope per line, got:\n%s", out)
	}
}
