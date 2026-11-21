package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/skelz0r/ags/internal/agstest"
)

const alertWatch = `version: 1
timezone: Europe/Paris
jobs:
  watch:
    schedule: "0 * * * *"
    command: dispatch
    precheck: sh watch.sh
    prompt: |
      Handle these alerts:
      {precheck.output}
`

const watchScript = `seen="$AGS_JOB_STATE_DIR/seen"
new=""
while read -r alert; do
  grep -qxF "$alert" "$seen" 2>/dev/null || new="$new$alert
"
done < alerts
if [ -z "$new" ]; then echo "nothing new"; exit 1; fi
{ cat "$seen" 2>/dev/null; printf '%s' "$new"; } > "$AGS_STAGED_STATE_DIR/seen"
printf '%s' "$new"
`

func alertWorld(t *testing.T) *World {
	w := New(t)
	w.SharedFile(alertWatch)
	w.Write(filepath.Join(w.ProjectDir, "watch.sh"), watchScript)
	w.Write(filepath.Join(w.ProjectDir, "alerts"), "nginx CVE-2026-1\nlinux CVE-2026-2\n")
	return w
}

func promptOf(w *World, run Run) string {
	prompt, _ := w.Show(run.ID)["prompt"].(string)
	return prompt
}

func TestAFailedRunLeavesWhatItsPrecheckFoundToTheNextRun(t *testing.T) {
	w := alertWorld(t)
	w.Register()
	d := w.Daemon()
	fail := filepath.Join(w.ProjectDir, "fail")
	w.Write(fail, "")
	w.TickEvery(d, At(19, 8, 59, 30), At(19, 9, 0, 30), 30*time.Second)
	os.Remove(fail)
	w.TickEvery(d, At(19, 9, 59, 30), At(19, 10, 0, 30), 30*time.Second)

	runs := w.Runs()
	if got := Statuses(runs); strings.Join(got, ",") != "failed (exit 1),succeeded" {
		t.Fatalf("expected a failed run then a succeeded one, got %v", got)
	}
	for _, run := range runs {
		if prompt := promptOf(w, run); !strings.Contains(prompt, "nginx CVE-2026-1") || !strings.Contains(prompt, "linux CVE-2026-2") {
			t.Fatalf("both runs should be handed the alerts the failed one could not send, run %s got %q", run.Status, prompt)
		}
	}
}

func TestASucceededRunCommitsWhatItsPrecheckFoundSoTheNextRunDoesNotSeeItAgain(t *testing.T) {
	w := alertWorld(t)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 8, 59, 30), At(19, 10, 0, 30), 30*time.Second)
	w.Write(filepath.Join(w.ProjectDir, "alerts"), "nginx CVE-2026-1\nlinux CVE-2026-2\nopenssl CVE-2026-3\n")
	w.TickEvery(d, At(19, 10, 59, 30), At(19, 11, 0, 30), 30*time.Second)

	runs := w.Runs()
	if got := Statuses(runs); strings.Join(got, ",") != "succeeded,skipped (precheck),succeeded" {
		t.Fatalf("expected the alerts handled once, nothing new, then the new one, got %v", got)
	}
	if prompt := promptOf(w, runs[0]); strings.Contains(prompt, "nginx") || !strings.Contains(prompt, "openssl CVE-2026-3") {
		t.Fatalf("the last run should be handed only the new alert, got %q", prompt)
	}
}

func TestAPrecheckThatFindsNothingStillMovesItsCursorOnAcrossFoldedSkips(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
jobs:
  watch:
    schedule: "0 * * * *"
    prompt: Watch.
    precheck: echo $(( $(cat "$AGS_JOB_STATE_DIR/checks" 2>/dev/null || echo 0) + 1 )) > "$AGS_STAGED_STATE_DIR/checks"; echo nothing new; exit 1
`)
	w.Register()
	d := w.Daemon()
	w.TickEvery(d, At(19, 8, 59, 30), At(19, 11, 0, 30), 30*time.Second)

	if got := Statuses(w.Runs()); strings.Join(got, ",") != "skipped (precheck) x3" {
		t.Fatalf("expected three folded skips, got %v", got)
	}
	checks, err := os.ReadFile(filepath.Join(w.Paths.StateDir, "jobs", "proj", "watch", "checks"))
	if err != nil || string(checks) != "3\n" {
		t.Fatalf("each quiet check should move the cursor on, got %q %v", checks, err)
	}
}
