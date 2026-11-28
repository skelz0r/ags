package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
	"github.com/skelz0r/ags/internal/daemon"
)

const brokenPrecheck = `version: 1
timezone: Europe/Paris
jobs:
  check:
    schedule: "* * * * *"
    precheck: "test -e fixed || { echo token=hunter2 >&2; exit 2; }"
    prompt: Check.
`

func onlyPost(t *testing.T, posts []NavettePost, status string) NavettePost {
	t.Helper()
	if len(posts) != 1 {
		t.Fatalf("expected one post, got %d: %+v", len(posts), posts)
	}
	if posts[0].Flags["--status"] != status {
		t.Fatalf("expected status %s, got %+v", status, posts[0].Flags)
	}
	return posts[0]
}

func TestAPrecheckFailingRepeatedlyIsOneInboxItemClosedOnceItPasses(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(brokenPrecheck)
	w.Register()
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("CLAUDE_CODE_BRIDGE_SESSION_ID", "session-of-the-shell")
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 3, 0), 30*time.Second)

	if w.Notes.Count("precheck_error") != 4 {
		t.Fatalf("desktop keeps notifying each precheck error, got %d", w.Notes.Count("precheck_error"))
	}
	post := onlyPost(t, w.NavettePostsFor("ags:proj:check"), "waiting")
	if post.Flags["--type"] != "ags" || post.Flags["--title"] != "proj/check: precheck fails" {
		t.Fatalf("expected an ags item titled after the job, got %+v", post.Flags)
	}
	if desc := post.Flags["--description"]; !strings.Contains(desc, "Last: precheck exit 2.") || strings.Contains(desc, "hunter2") {
		t.Fatalf("expected the short reason without the precheck's stderr, got %q", desc)
	}
	for _, arg := range post.Args {
		if arg == "--option" || arg == "--notify" {
			t.Fatalf("the daemon has no pane to send a click back to, got %v", post.Args)
		}
	}
	if post.Bridge != "unset" || post.Pane != "unset" {
		t.Fatalf("the item must not be tied to the pane the daemon was started from, got bridge %q pane %q", post.Bridge, post.Pane)
	}
	if resolved, _ := filepath.EvalSymlinks(w.ProjectDir); post.Cwd != resolved && post.Cwd != w.ProjectDir {
		t.Fatalf("navette runs from the project so the item gets its tag, got %q", post.Cwd)
	}

	w.Write("fixed", "")
	w.TickEvery(d, At(19, 10, 3, 30), At(19, 10, 4, 30), 30*time.Second)
	posts := w.NavettePostsFor("ags:proj:check")
	if len(posts) != 2 || posts[1].Flags["--status"] != "done" || posts[1].Flags["--description"] != "The precheck passes again." {
		t.Fatalf("expected the item closed once the precheck passes, got %+v", posts)
	}
	w.TickEvery(d, At(19, 10, 5, 0), At(19, 10, 5, 30), 30*time.Second)
	if len(w.NavettePosts()) != 2 {
		t.Fatalf("nothing more to post once resolved, got %+v", w.NavettePosts())
	}
}

func TestAJobWaitingForApprovalIsAnInboxItemClosedByTheApproval(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(checkJob("* * * * *", "Check.", "true"))
	w.MustCLI("add", w.ProjectDir, "--name", "proj")
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 2, 0), 30*time.Second)

	post := onlyPost(t, w.NavettePostsFor("ags:proj:check"), "waiting")
	if !strings.Contains(post.Flags["--description"], "`ags approve proj/check`") {
		t.Fatalf("expected the item to say how to approve, got %q", post.Flags["--description"])
	}
	if desc := post.Flags["--description"]; !strings.Contains(desc, "```diff\n@@ -1,0 +1,") || !strings.Contains(desc, "\n+   Check.\n") {
		t.Fatalf("a job never approved shows all it would run as added lines, got %q", desc)
	}

	w.MustCLI("approve", "proj/check", "--yes")
	w.TickEvery(d, At(19, 10, 2, 30), At(19, 10, 3, 0), 30*time.Second)
	posts := w.NavettePostsFor("ags:proj:check")
	if len(posts) != 2 || posts[1].Flags["--status"] != "done" || posts[1].Flags["--description"] != "Approved." {
		t.Fatalf("expected the approval to close the item, got %+v", posts)
	}

	w.SharedFile(checkJob("* * * * *", "Check twice.", "true"))
	w.TickEvery(d, At(19, 10, 3, 30), At(19, 10, 4, 0), 30*time.Second)
	changed := onlyPost(t, w.NavettePostsFor("ags:proj:check")[2:], "waiting")
	desc := changed.Flags["--description"]
	if !strings.Contains(desc, "\n-   Check.\n+   Check twice.\n") || strings.Contains(desc, "Never approved") {
		t.Fatalf("a changed job shows what changed since the approval, got %q", desc)
	}
	if strings.Contains(desc, "  command: ") {
		t.Fatalf("lines far from the change stay out of the item, got %q", desc)
	}
}

func TestAHandOffWithoutReportWaitsInTheInboxUntilTheAgentReports(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(strings.Replace(cveWatch, `"0 9 * * *"`, `"0 9 19 10 *"`, 1))
	w.Register()
	d := w.Daemon()
	w.Tick(d, At(19, 8, 59, 40))
	w.Tick(d, At(19, 9, 0, 10))
	d.Wait()
	w.TickEvery(d, At(20, 8, 59, 0), At(20, 9, 0, 0), 30*time.Second)
	if posts := w.NavettePosts(); len(posts) != 0 {
		t.Fatalf("a hand-off within report_within is not a problem, got %+v", posts)
	}

	w.TickEvery(d, At(20, 9, 1, 0), At(20, 9, 1, 30), 30*time.Second)
	post := onlyPost(t, w.NavettePostsFor("ags:proj:cve-watch"), "waiting")
	if !strings.Contains(post.Flags["--description"], "no `ags report` within 1d") {
		t.Fatalf("expected the item to name the missing report, got %q", post.Flags["--description"])
	}

	w.MustCLI("report", w.Runs()[0].ID, "--status", "done", "--summary", "Patched.")
	w.TickEvery(d, At(20, 9, 2, 0), At(20, 9, 2, 0), 30*time.Second)
	posts := w.NavettePostsFor("ags:proj:cve-watch")
	if len(posts) != 2 || posts[1].Flags["--status"] != "done" {
		t.Fatalf("expected the report to close the item, got %+v", posts)
	}
}

func TestAnInvalidProjectFileIsAnInboxItemUntilFixed(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(checkJob("0 9 * * *", "Check.", "true"))
	w.Register()
	d := w.Daemon()
	w.SharedFile("version: 1\njobs: [oops\n")
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 1, 0), 30*time.Second)

	post := onlyPost(t, w.NavettePostsFor("ags:proj:@config"), "waiting")
	if post.Flags["--title"] != "ags: invalid config for proj" {
		t.Fatalf("expected the item to name the project, got %+v", post.Flags)
	}

	w.SharedFile(checkJob("0 9 * * *", "Check.", "true"))
	w.TickEvery(d, At(19, 10, 1, 30), At(19, 10, 1, 30), 30*time.Second)
	posts := w.NavettePostsFor("ags:proj:@config")
	if len(posts) != 2 || posts[1].Flags["--status"] != "done" || posts[1].Flags["--description"] != "Config fixed." {
		t.Fatalf("expected the fix to close the item, got %+v", posts)
	}
}

func TestAnUnreachableNavetteNeverStopsTheDaemonAndIsRetriedLater(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(brokenPrecheck)
	w.Register()
	w.NavetteDown(true)
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 2, 0), 30*time.Second)

	if got := Statuses(w.Runs()); len(got) != 3 || got[2] != "precheck_error (precheck exit 2)" {
		t.Fatalf("expected the job to keep running, got %v", got)
	}
	if w.Notes.Count("precheck_error") != 3 {
		t.Fatalf("desktop still notifies, got %d", w.Notes.Count("precheck_error"))
	}
	if attempts := len(w.NavettePosts()); attempts != 1 {
		t.Fatalf("expected one attempt, then a pause before retrying, got %d", attempts)
	}
	log, _ := os.ReadFile(w.Paths.DaemonLog())
	if strings.Count(string(log), "not posted") != 1 || !strings.Contains(string(log), "server unreachable") {
		t.Fatalf("expected the failure logged once, got:\n%s", log)
	}

	w.NavetteDown(false)
	w.TickEvery(d, At(19, 10, 6, 0), At(19, 10, 8, 0), 30*time.Second)
	posts := w.NavettePostsFor("ags:proj:check")
	if len(posts) == 0 || posts[len(posts)-1].Flags["--status"] != "waiting" {
		t.Fatalf("expected the item posted once navette is back, got %+v", posts)
	}
}

func TestADailyHandOffWithoutReportStaysInTheInboxPastTheNextOccurrence(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(cveWatch)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 8, 59, 30), At(19, 9, 0, 0), 30*time.Second)
	w.TickEvery(d, At(20, 8, 59, 0), At(20, 9, 2, 0), 30*time.Second)

	posts := w.NavettePostsFor("ags:proj:cve-watch")
	if len(posts) != 1 || posts[0].Flags["--status"] != "waiting" || !strings.Contains(posts[0].Flags["--description"], "Mon 19 Oct 09:00") {
		t.Fatalf("expected the first day's missing report to stay open after the second run, got %+v", posts)
	}
}

func TestAStreakLongerThanTheLookBackIsStillOnePost(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(brokenPrecheck)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 35, 0), 30*time.Second)
	onlyPost(t, w.NavettePostsFor("ags:proj:check"), "waiting")

	w.TickEvery(d, At(19, 10, 35, 30), At(19, 11, 1, 0), 30*time.Second)
	posts := w.NavettePostsFor("ags:proj:check")
	if len(posts) != 2 || !strings.Contains(posts[1].Flags["--description"], "since Mon 19 Oct 10:00 (30+ runs)") {
		t.Fatalf("expected one hourly repost keeping the streak's first failure, got %+v", posts)
	}
}

func TestAFailingWeeklyJobStaysOpenPastSevenDays(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(strings.Replace(brokenPrecheck, `"* * * * *"`, `"0 10 1 * *"`, 1))
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(1, 9, 59, 30), At(1, 10, 0, 30), 30*time.Second)
	onlyPost(t, w.NavettePostsFor("ags:proj:check"), "waiting")

	w.TickEvery(d, At(10, 10, 0, 0), At(10, 10, 1, 30), 30*time.Second)
	onlyPost(t, w.NavettePostsFor("ags:proj:check"), "waiting")
}

func TestAMissingNavetteBinaryNeverStopsTheDaemon(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.Env["PATH"] = "/usr/bin:/bin"
	w.SharedFile(brokenPrecheck)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 1, 0), 30*time.Second)

	if got := w.Runs(); len(got) != 2 || w.Notes.Count("precheck_error") != 2 {
		t.Fatalf("expected the job to keep running and notifying on desktop, got %v", Statuses(got))
	}
	if log, _ := os.ReadFile(w.Paths.DaemonLog()); !strings.Contains(string(log), "navette not found in PATH") {
		t.Fatalf("expected the missing binary logged, got:\n%s", log)
	}
}

func TestABrokenRegistryDoesNotCloseAProjectConfigItem(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(checkJob("0 9 * * *", "Check.", "true"))
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 0), At(19, 9, 59, 0), 30*time.Second)
	w.SharedFile("version: 1\njobs: [oops\n")
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 0), 30*time.Second)
	onlyPost(t, w.NavettePostsFor("ags:proj:@config"), "waiting")

	w.Write(w.Paths.Registry(), "projects: [oops\n")
	w.TickEvery(d, At(19, 10, 0, 30), At(19, 10, 1, 0), 30*time.Second)
	onlyPost(t, w.NavettePostsFor("ags:registry:@config"), "waiting")
	onlyPost(t, w.NavettePostsFor("ags:proj:@config"), "waiting")
}

func TestANewStreakAfterAnUnpostedRecoveryIsPostedWithItsOwnStart(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(brokenPrecheck)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 30), 30*time.Second)
	onlyPost(t, w.NavettePostsFor("ags:proj:check"), "waiting")

	w.NavetteDown(true)
	w.Write("fixed", "")
	w.TickEvery(d, At(19, 10, 0, 30), At(19, 10, 1, 30), 30*time.Second)
	os.Remove(filepath.Join(w.ProjectDir, "fixed"))
	w.TickEvery(d, At(19, 10, 2, 0), At(19, 10, 5, 30), 30*time.Second)
	w.NavetteDown(false)
	w.TickEvery(d, At(19, 10, 6, 0), At(19, 10, 7, 0), 30*time.Second)

	posts := w.NavettePostsFor("ags:proj:check")
	last := posts[len(posts)-1]
	if len(posts) != 2 || last.Flags["--status"] != "waiting" || !strings.Contains(last.Flags["--description"], "since Mon 19 Oct 10:02") {
		t.Fatalf("expected the new streak posted with its own start, got %+v", posts)
	}
}

func TestOccurrencesMissedDuringALongAbsenceOpenAnItem(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  daily:
    schedule: "0 9 * * *"
    prompt: Daily.
    catch_up: never
`)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(1, 8, 59, 30), At(1, 9, 0, 0), 30*time.Second)
	w.TickEvery(d, At(10, 9, 30, 0), At(10, 9, 31, 30), 30*time.Second)

	post := onlyPost(t, w.NavettePostsFor("ags:proj:daily"), "info")
	if !strings.Contains(post.Flags["--description"], "the last at Sat 10 Oct 09:00") {
		t.Fatalf("expected the missed occurrences up to today, got %q", post.Flags["--description"])
	}
}

func TestAMissingReportStaysOpenWhileLaterRunsReport(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	machine, _ := os.ReadFile(w.Paths.MachineConfig())
	w.Write(w.Paths.MachineConfig(), strings.Replace(string(machine),
		"  agent:\n    argv: [\"sh\", \"-c\", \"cat > /dev/null; echo handed-off\"]\n    timeout: 10s\n",
		"  agent:\n    argv: [\"sh\", \"-c\", \"cat > /dev/null; echo handed-off\"]\n    timeout: 10s\n    report_within: 2m\n", 1))
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  handoff:
    schedule: "* * * * *"
    command: agent
    prompt: Hand off.
`)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 30), 30*time.Second)
	forgotten := w.Runs()[0].ID
	for at := At(19, 10, 1, 0); at.Before(At(19, 10, 40, 0)); at = at.Add(time.Minute) {
		w.TickEvery(d, at, at.Add(30*time.Second), 30*time.Second)
		if latest := w.Runs()[0]; latest.ID != forgotten && latest.Status == "succeeded" {
			w.CLI("report", latest.ID, "--status", "done", "--summary", "Done.")
		}
	}

	posts := w.NavettePostsFor("ags:proj:handoff")
	if len(posts) == 0 || posts[len(posts)-1].Flags["--status"] != "waiting" {
		t.Fatalf("expected the forgotten report to keep the item open, got %+v", posts)
	}
}

func TestTheForgottenReportKeepsTheItemWhenALaterRunMissesItsReportToo(t *testing.T) {
	w := New(t)
	w.EnableNavette()
	machine, _ := os.ReadFile(w.Paths.MachineConfig())
	w.Write(w.Paths.MachineConfig(), strings.Replace(string(machine),
		"  agent:\n    argv: [\"sh\", \"-c\", \"cat > /dev/null; echo handed-off\"]\n    timeout: 10s\n",
		"  agent:\n    argv: [\"sh\", \"-c\", \"cat > /dev/null; echo handed-off\"]\n    timeout: 10s\n    report_within: 2m\n", 1))
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  handoff:
    schedule: "* * * * *"
    command: agent
    prompt: Hand off.
`)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 30), 30*time.Second)
	forgotten := w.Runs()[0].ID
	for at := At(19, 10, 1, 0); at.Before(At(19, 10, 40, 0)); at = at.Add(time.Minute) {
		w.TickEvery(d, at, at.Add(30*time.Second), 30*time.Second)
		if latest := w.Runs()[0]; latest.ID != forgotten && latest.Status == "succeeded" && at.Before(At(19, 10, 35, 0)) {
			w.CLI("report", latest.ID, "--status", "done", "--summary", "Done.")
		}
	}
	for _, r := range w.Runs() {
		if r.ID != forgotten && r.Status == "succeeded" {
			w.CLI("report", r.ID, "--status", "done", "--summary", "Done.")
		}
	}
	w.TickEvery(d, At(19, 10, 40, 0), At(19, 10, 40, 30), 30*time.Second)

	posts := w.NavettePostsFor("ags:proj:handoff")
	if last := posts[len(posts)-1]; last.Flags["--status"] != "waiting" || !strings.Contains(last.Flags["--description"], forgotten) {
		t.Fatalf("expected the item to stay on the forgotten run once the later ones report, got %+v", posts)
	}
}

func failTwice(w *World, d *daemon.Daemon) {
	w.Write(filepath.Join(w.ProjectDir, "fail"), "")
	w.TickEvery(d, At(19, 8, 59, 30), At(19, 9, 0, 30), 30*time.Second)
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 1, 0), 30*time.Second)
}

func TestAnIsolatedFailureFollowedByAPrecheckWithNothingToDoNeverReachesTheInbox(t *testing.T) {
	w := alertWorld(t)
	w.EnableNavette()
	w.Register()
	d := w.Daemon()
	w.Write(filepath.Join(w.ProjectDir, "fail"), "")
	w.TickEvery(d, At(19, 8, 59, 30), At(19, 9, 0, 30), 30*time.Second)
	w.Write(filepath.Join(w.ProjectDir, "alerts"), "")
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 13, 0, 30), 30*time.Second)
	w.TickEvery(d, At(21, 9, 0, 0), At(21, 9, 0, 30), 30*time.Second)

	if posts := w.NavettePostsFor("ags:proj:watch"); len(posts) != 0 {
		t.Fatalf("one failure then prechecks with nothing to do: the job is back to normal, nothing to post, got %+v", posts)
	}
}

func TestAJobFailingSeveralTimesInARowReachesTheInboxWithTheRealErrorAndWhatToDo(t *testing.T) {
	w := alertWorld(t)
	w.EnableNavette()
	w.Register()
	d := w.Daemon()
	w.Write(filepath.Join(w.ProjectDir, "fail"), "")
	w.TickEvery(d, At(19, 8, 59, 30), At(19, 9, 0, 30), 30*time.Second)
	if posts := w.NavettePostsFor("ags:proj:watch"); len(posts) != 0 {
		t.Fatalf("a single failure waits for the next run before reaching the inbox, got %+v", posts)
	}
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 1, 0), 30*time.Second)

	post := onlyPost(t, w.NavettePostsFor("ags:proj:watch"), "waiting")
	desc := post.Flags["--description"]
	for _, want := range []string{
		"Le job a échoué 2 fois de suite depuis Mon 19 Oct 09:00.",
		"**Ce qui a planté** : la commande `dispatch` s'est arrêtée au bout de 0 s (exit 1) sans lancer l'agent. Erreur : `unreadable tab 1 of herdr workspace wW`",
		"**À faire** : l'agent n'a jamais démarré, relancer ne suffira pas : corrige la commande `dispatch` ou le script qu'elle appelle, puis relance avec `ags run proj/watch` pour vérifier.",
		"Le precheck avait trouvé du travail : son état n'a pas été validé, le prochain run le retrouvera.",
		"Fin du stderr :\n\n```\ndispatch: enqueue\nunreadable tab 1 of herdr workspace wW\n```",
		"Cet item se ferme tout seul dès qu'un run réussit ou qu'un precheck passe sans rien à faire.",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("expected the description to contain %q, got %q", want, desc)
		}
	}
	if post.Flags["--title"] != "proj/watch en échec" {
		t.Fatalf("expected the item titled after the job, got %q", post.Flags["--title"])
	}
}

func TestAFailureItemClosesOnceAPrecheckFindsNothingToDo(t *testing.T) {
	w := alertWorld(t)
	w.EnableNavette()
	w.Register()
	d := w.Daemon()
	failTwice(w, d)
	w.Write(filepath.Join(w.ProjectDir, "alerts"), "")
	w.TickEvery(d, At(19, 10, 59, 30), At(19, 13, 0, 30), 30*time.Second)

	posts := w.NavettePostsFor("ags:proj:watch")
	last := posts[len(posts)-1]
	if len(posts) != 2 || last.Flags["--status"] != "done" ||
		last.Flags["--description"] != "Le job est revenu à la normale : le precheck du Mon 19 Oct 11:00 est passé sans rien à faire." {
		t.Fatalf("expected the item closed once the precheck has nothing to do, got %+v", posts)
	}
}

func TestARareJobFailingOnceReachesTheInboxWhenNoRunGoesThroughWithinADay(t *testing.T) {
	w := alertWorld(t)
	w.SharedFile(strings.Replace(alertWatch, "0 * * * *", "0 9 * * 1", 1))
	w.EnableNavette()
	w.Register()
	d := w.Daemon()
	w.Write(filepath.Join(w.ProjectDir, "fail"), "")
	w.TickEvery(d, At(19, 8, 59, 30), At(19, 9, 0, 30), 30*time.Second)
	w.TickEvery(d, At(20, 10, 0, 0), At(20, 10, 3, 0), 30*time.Second)
	if posts := w.NavettePostsFor("ags:proj:watch"); len(posts) != 0 {
		t.Fatalf("a weekly job failing once is not reported within the first day, got %+v", posts)
	}
	w.TickEvery(d, At(20, 11, 1, 0), At(20, 11, 3, 0), 30*time.Second)

	post := onlyPost(t, w.NavettePostsFor("ags:proj:watch"), "waiting")
	if !strings.Contains(post.Flags["--description"], "Le job a échoué le Mon 19 Oct 09:00 et aucun run n'est passé depuis.") {
		t.Fatalf("expected the isolated failure reported past 26 hours, got %q", post.Flags["--description"])
	}
}

func TestAFrenchInboxItemReachesNavetteUnderAUTF8LocaleWhenTheDaemonHasNone(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "")
	w := alertWorld(t)
	w.EnableNavette()
	w.Register()
	d := w.Daemon()
	failTwice(w, d)

	post := onlyPost(t, w.NavettePostsFor("ags:proj:watch"), "waiting")
	if !strings.Contains(strings.ToUpper(post.Locale), "UTF-8") {
		t.Fatalf("launchd starts the daemon without a locale and navette then rejects accented text, expected a UTF-8 locale, got %q", post.Locale)
	}
}
