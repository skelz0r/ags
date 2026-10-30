package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skelz0r/ags/internal/config"
)

const (
	StatusRunning       = "running"
	StatusSucceeded     = "succeeded"
	StatusFailed        = "failed"
	StatusSkipped       = "skipped"
	StatusPrecheckError = "precheck_error"
	StatusMissed        = "missed"

	ReasonPrecheck    = "precheck"
	ReasonOverlap     = "overlap"
	ReasonPaused      = "paused"
	ReasonUnapproved  = "unapproved"
	ReasonInterrupted = "interrupted"
	ReasonTimeout     = "timeout"

	TriggerSchedule = "schedule"
	TriggerCatchUp  = "catch_up"
	TriggerManual   = "manual"

	OutputLimit      = 64 * 1024
	KeepPerJob       = 100
	previousLookBack = 10
)

type Run struct {
	ID            string     `json:"id"`
	Project       string     `json:"project"`
	Job           string     `json:"job"`
	Trigger       string     `json:"trigger,omitempty"`
	Status        string     `json:"status"`
	Reason        string     `json:"reason,omitempty"`
	ScheduledFor  time.Time  `json:"scheduled_for"`
	LateBySeconds int64      `json:"late_by_seconds"`
	MissedCount   int        `json:"missed_count,omitempty"`
	MissedLast    *time.Time `json:"missed_last,omitempty"`
	SkippedCount  int        `json:"skipped_count,omitempty"`
	SkippedLast   *time.Time `json:"skipped_last,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	Command       string     `json:"command,omitempty"`
	Argv          []string   `json:"argv,omitempty"`
	ExitCode      *int       `json:"exit_code,omitempty"`
	OwnerPID      int        `json:"owner_pid,omitempty"`
}

func (r *Run) StartedLog() string {
	return fmt.Sprintf("%s: run %s started (%s, scheduled %s, late by %s)", r.Key(), r.ID, r.Trigger, r.ScheduledFor.Format(time.RFC3339), config.FormatDuration(r.LateBy()))
}

func (r *Run) EndedLog() string {
	return strings.TrimSpace(fmt.Sprintf("%s: run %s %s %s", r.Key(), r.ID, r.Status, r.Reason))
}

func (r *Run) LateBy() time.Duration { return time.Duration(r.LateBySeconds) * time.Second }

func (r *Run) Duration() time.Duration {
	if r.StartedAt == nil || r.EndedAt == nil {
		return 0
	}
	return r.EndedAt.Sub(*r.StartedAt)
}

func (r *Run) Terminal() bool { return r.Status != StatusRunning }

func (r *Run) Ran() bool { return r.Status != StatusSkipped && r.Status != StatusMissed }

func (r *Run) LastScheduled() time.Time {
	if r.SkippedLast != nil {
		return *r.SkippedLast
	}
	return r.ScheduledFor
}

func (r *Run) repeats(prev *Run) bool {
	return r.Status == StatusSkipped && prev.Status == r.Status && prev.Reason == r.Reason &&
		r.Trigger != TriggerManual && prev.Trigger != TriggerManual
}

func (r *Run) Key() string { return r.Project + "/" + r.Job }

var historyMu sync.Mutex

func NewRunID(now time.Time) string {
	b := make([]byte, 3)
	rand.Read(b)
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b)
}

func (s *Store) RunDir(r *Run) string {
	return filepath.Join(s.Paths.RunsDir(), r.Project, r.Job, r.ID)
}

func (s *Store) JobStateDir(r *Run) string {
	return filepath.Join(s.Paths.JobsDir(), r.Project, r.Job)
}

func (s *Store) SaveRun(r *Run) error {
	var prev *Run
	if r.Terminal() {
		prev, _ = s.previous(r)
	}
	return s.save(r, prev)
}

func (s *Store) Record(r *Run) (*Run, error) {
	prev, followed := s.previous(r)
	if prev == nil || !r.repeats(prev) {
		return r, s.save(r, prev)
	}
	if err := os.RemoveAll(s.RunDir(r)); err != nil {
		return r, s.save(r, prev)
	}
	prev.SkippedCount = max(prev.SkippedCount, 1) + 1
	last := r.ScheduledFor
	prev.SkippedLast = &last
	if err := s.writeRun(prev); err != nil {
		return r, s.save(r, prev)
	}
	if followed {
		return prev, s.appendHistory(prev)
	}
	return prev, nil
}

func (s *Store) previous(r *Run) (prev *Run, followed bool) {
	jobDir := filepath.Join(s.Paths.RunsDir(), r.Project, r.Job)
	entries, _ := os.ReadDir(jobDir)
	for i, seen := len(entries)-1, 0; i >= 0 && seen < previousLookBack; i-- {
		if !entries[i].IsDir() || entries[i].Name() == r.ID {
			continue
		}
		e, err := readRun(filepath.Join(jobDir, entries[i].Name(), "run.json"))
		if err != nil {
			continue
		}
		if !e.LastScheduled().Before(r.ScheduledFor) {
			followed = true
			continue
		}
		seen++
		if prev == nil || e.LastScheduled().After(prev.LastScheduled()) {
			prev = e
		}
	}
	return prev, followed
}

func (s *Store) save(r, prev *Run) error {
	if err := s.writeRun(r); err != nil {
		return err
	}
	if !r.Terminal() {
		return nil
	}
	if prev != nil && prev.SkippedCount > 0 {
		if err := s.appendHistory(prev); err != nil {
			return err
		}
	}
	return s.appendHistory(r)
}

func (s *Store) writeRun(r *Run) error {
	dir := s.RunDir(r)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(filepath.Join(dir, "run.json"), append(data, '\n'), 0o600)
}

func (s *Store) WriteRunFile(r *Run, name string, data []byte) error {
	dir := s.RunDir(r)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), data, 0o600)
}

func (s *Store) ReadRunFile(r *Run, name string) string {
	data, _ := os.ReadFile(filepath.Join(s.RunDir(r), name))
	return string(data)
}

func (s *Store) appendHistory(r *Run) error {
	historyMu.Lock()
	defer historyMu.Unlock()
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.Paths.History(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

type Filter struct {
	Project string
	Job     string
	Status  string
	Since   time.Time
	Limit   int
}

func (f Filter) match(r *Run) bool {
	switch {
	case f.Project != "" && r.Project != f.Project,
		f.Job != "" && r.Job != f.Job,
		f.Status != "" && r.Status != f.Status && r.Status+" ("+r.Reason+")" != f.Status,
		!f.Since.IsZero() && r.LastScheduled().Before(f.Since):
		return false
	}
	return true
}

func (s *Store) ListRuns(f Filter) ([]*Run, error) {
	pattern := filepath.Join(s.Paths.RunsDir(), "*", "*", "*", "run.json")
	if f.Project != "" {
		pattern = filepath.Join(s.Paths.RunsDir(), f.Project, "*", "*", "run.json")
		if f.Job != "" {
			pattern = filepath.Join(s.Paths.RunsDir(), f.Project, f.Job, "*", "run.json")
		}
	}
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var runs []*Run
	for _, file := range files {
		r, err := readRun(file)
		if err != nil {
			continue
		}
		if f.match(r) {
			runs = append(runs, r)
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if a, b := runs[i].LastScheduled(), runs[j].LastScheduled(); !a.Equal(b) {
			return a.After(b)
		}
		return runs[i].ID > runs[j].ID
	})
	if f.Limit > 0 && len(runs) > f.Limit {
		runs = runs[:f.Limit]
	}
	return runs, nil
}

func readRun(file string) (*Run, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	r := &Run{}
	return r, json.Unmarshal(data, r)
}

func (s *Store) FindRun(id string) (*Run, error) {
	files, _ := filepath.Glob(filepath.Join(s.Paths.RunsDir(), "*", "*", "*", "run.json"))
	var found []*Run
	for _, file := range files {
		name := filepath.Base(filepath.Dir(file))
		if name == id || strings.HasPrefix(name, id) {
			r, err := readRun(file)
			if err == nil {
				found = append(found, r)
			}
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("run %q not found", id)
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("run %q is ambiguous: %d runs match", id, len(found))
}

func (s *Store) Prune(keep int) error {
	jobs, err := filepath.Glob(filepath.Join(s.Paths.RunsDir(), "*", "*"))
	if err != nil {
		return err
	}
	for _, jobDir := range jobs {
		entries, err := os.ReadDir(jobDir)
		if err != nil {
			continue
		}
		var ids []string
		for _, e := range entries {
			if e.IsDir() {
				ids = append(ids, e.Name())
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(ids)))
		kept := map[bool]int{}
		for _, id := range ids {
			r, err := readRun(filepath.Join(jobDir, id, "run.json"))
			if err == nil && !r.Terminal() {
				continue
			}
			ran := err == nil && r.Ran()
			if kept[ran] < keep {
				kept[ran]++
				continue
			}
			os.RemoveAll(filepath.Join(jobDir, id))
		}
	}
	return nil
}

func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscallZero)
	return err == nil || errors.Is(err, fs.ErrPermission)
}
