package problem

import (
	"sort"
	"strings"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/state"
)

const (
	PrecheckError = state.StatusPrecheckError
	Failed        = state.StatusFailed
	Blocked       = state.ReportBlocked
	AgentFailed   = "agent_failed"
	NoReport      = "no_report"
	Missed        = state.StatusMissed

	Window = state.UnreportedLookBack

	FailedAfter = 2
	FailedGrace = 26 * time.Hour
)

type Problem struct {
	Kind  string
	Runs  []*state.Run
	Since time.Time
	Last  time.Time
	Count int
	ToEnd bool
}

func (p Problem) Latest() *state.Run { return p.Runs[0] }

func (p Problem) Recent(now time.Time) bool { return !p.Last.Before(now.Add(-Window)) }

type Job struct {
	store   *state.Store
	machine *config.Machine
	history []*state.Run
	now     time.Time
}

func Of(store *state.Store, m *config.Machine, runs []*state.Run, now time.Time) Job {
	history := append([]*state.Run(nil), runs...)
	sort.SliceStable(history, func(i, j int) bool { return history[i].HappenedAt().After(history[j].HappenedAt()) })
	return Job{store: store, machine: m, history: history, now: now}
}

func (j Job) Runs() []*state.Run { return j.history }

func (j Job) Problems() []Problem {
	var found []Problem
	for _, derive := range []func() (Problem, bool){j.precheck, j.failed, j.report, j.noReport, j.missed} {
		if p, ok := derive(); ok {
			found = append(found, p)
		}
	}
	return found
}

func (j Job) precheck() (Problem, bool) {
	series, toEnd := j.series(state.StatusPrecheckError, func(r *state.Run) bool { return !j.store.PrecheckRan(r) })
	if len(series) == 0 {
		return Problem{}, false
	}
	return Problem{Kind: PrecheckError, Runs: series, Count: len(series), ToEnd: toEnd,
		Since: series[len(series)-1].PrecheckRanAt(), Last: series[0].PrecheckRanAt()}, true
}

func (j Job) failed() (Problem, bool) {
	series, toEnd := j.series(state.StatusFailed, func(r *state.Run) bool {
		return (!r.CommandStarted() && !NothingToDo(r)) || r.Status == state.StatusRunning
	})
	if len(series) == 0 || !confirmed(series, j.now) {
		return Problem{}, false
	}
	return Problem{Kind: Failed, Runs: series, Count: len(series), ToEnd: toEnd,
		Since: startedAt(series[len(series)-1]), Last: endedAt(series[0])}, true
}

func NothingToDo(r *state.Run) bool {
	return r.Status == state.StatusSkipped && strings.HasPrefix(r.Reason, state.ReasonPrecheck)
}

func confirmed(series []*state.Run, now time.Time) bool {
	return len(series) >= FailedAfter || !now.Before(endedAt(series[0]).Add(FailedGrace))
}

func (j Job) report() (Problem, bool) {
	r := j.settled()
	if r == nil || r.Report == nil {
		return Problem{}, false
	}
	kind := Blocked
	switch r.Report.Status {
	case state.ReportBlocked:
	case state.ReportFailed:
		kind = AgentFailed
	default:
		return Problem{}, false
	}
	return Problem{Kind: kind, Runs: []*state.Run{r}, Count: 1, Since: r.Report.At, Last: r.Report.At}, true
}

func (j Job) series(status string, ignored func(*state.Run) bool) ([]*state.Run, bool) {
	var series []*state.Run
	for _, r := range j.history {
		if ignored(r) {
			continue
		}
		if r.Status != status {
			return series, false
		}
		series = append(series, r)
	}
	return series, true
}

func (j Job) Unreported() []*state.Run {
	var overdue []*state.Run
	for _, r := range j.history {
		if r.Unreported(j.machine.ReportWithin(r.Command), j.now) {
			overdue = append(overdue, r)
		}
	}
	sort.SliceStable(overdue, func(a, b int) bool { return overdue[a].StartedAt.After(*overdue[b].StartedAt) })
	return overdue
}

func (j Job) noReport() (Problem, bool) {
	var settled time.Time
	if r := j.settled(); r != nil {
		settled = startedAt(r)
	}
	var open []*state.Run
	for _, r := range j.Unreported() {
		if r.StartedAt.After(settled) {
			open = append(open, r)
		}
	}
	if len(open) == 0 {
		return Problem{}, false
	}
	return Problem{Kind: NoReport, Runs: open, Count: len(open),
		Since: *open[len(open)-1].StartedAt, Last: *open[0].StartedAt}, true
}

func (j Job) missed() (Problem, bool) {
	series, toEnd := j.series(state.StatusMissed, func(*state.Run) bool { return false })
	if len(series) == 0 {
		return Problem{}, false
	}
	count := 0
	for _, r := range series {
		count += max(1, r.MissedCount)
	}
	return Problem{Kind: Missed, Runs: series, Count: count, ToEnd: toEnd,
		Since: series[len(series)-1].ScheduledFor, Last: missedLast(series[0])}, true
}

func (j Job) settled() *state.Run {
	for _, r := range j.history {
		if r.CommandStarted() && settles(r) {
			return r
		}
	}
	return nil
}

func settles(r *state.Run) bool {
	if r.Report != nil {
		return true
	}
	return r.Status == state.StatusSucceeded && !r.AwaitsReport
}

func missedLast(r *state.Run) time.Time {
	if r.MissedLast != nil {
		return *r.MissedLast
	}
	return r.ScheduledFor
}

func startedAt(r *state.Run) time.Time {
	if r.StartedAt != nil {
		return *r.StartedAt
	}
	return r.ScheduledFor
}

func endedAt(r *state.Run) time.Time {
	if r.EndedAt != nil {
		return *r.EndedAt
	}
	return r.ScheduledFor
}

const (
	CauseNotStarted  = "not_started"
	CauseNoAgent     = "no_agent"
	CauseAgent       = "agent"
	CauseCommand     = "command"
	CauseTimeout     = "timeout"
	CauseInterrupted = "interrupted"

	LaunchWindow = time.Minute
)

func Cause(r *state.Run) string {
	switch {
	case !r.CommandStarted() || strings.HasPrefix(r.Reason, "start: "):
		return CauseNotStarted
	case strings.HasPrefix(r.Reason, state.ReasonInterrupted):
		return CauseInterrupted
	case strings.HasPrefix(r.Reason, state.ReasonTimeout):
		return CauseTimeout
	case !r.AwaitsReport:
		return CauseCommand
	case r.Report == nil && Duration(r) < LaunchWindow:
		return CauseNoAgent
	}
	return CauseAgent
}

func Duration(r *state.Run) time.Duration {
	start := r.CommandAt
	if start == nil {
		start = r.StartedAt
	}
	if start == nil || r.EndedAt == nil {
		return 0
	}
	return r.EndedAt.Sub(*start)
}
