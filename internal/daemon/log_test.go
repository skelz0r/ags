package daemon_test

import (
	"os"
	"strings"
	"testing"

	. "github.com/skelz0r/ags/internal/agstest"
)

func daemonLog(t *testing.T, w *World) string {
	t.Helper()
	data, err := os.ReadFile(w.Paths.DaemonLog())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEveryRunLogsWhenItStartsBeforeItsPrecheck(t *testing.T) {
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

	runs := w.Runs()
	if len(runs) != 1 || !strings.Contains(daemonLog(t, w), "proj/watch: run "+runs[0].ID+" started (schedule") {
		t.Fatalf("expected a started line for the run its precheck skipped, got:\n%s", daemonLog(t, w))
	}
}
