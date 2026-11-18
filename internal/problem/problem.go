package problem

import (
	"sort"
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
)

type Problem struct {
	Kind  string
	Runs  []*state.Run
	Since time.Time
	Last  time.Time
	Count int
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
	var series []*state.Run
	for _, r := range j.history {
		if !j.store.PrecheckRan(r) {
			continue
		}
		if r.Status != state.StatusPrecheckError {
			break
		}
		series = append(series, r)
	}
	if len(series) == 0 {
		return Problem{}, false
	}
	return Problem{Kind: PrecheckError, Runs: series, Count: len(series),
		Since: series[len(series)-1].PrecheckRanAt(), Last: series[0].PrecheckRanAt()}, true
}

func (j Job) failed() (Problem, bool) {
	var series []*state.Run
	for _, r := range j.history {
		if !r.CommandStarted() || r.Status == state.StatusRunning {
			continue
		}
		if r.Status != state.StatusFailed {
			break
		}
		series = append(series, r)
	}
	if len(series) == 0 {
		return Problem{}, false
	}
	return Problem{Kind: Failed, Runs: series, Count: len(series),
		Since: startedAt(series[len(series)-1]), Last: endedAt(series[0])}, true
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

func (j Job) noReport() (Problem, bool) {
	var settled time.Time
	if r := j.settled(); r != nil {
		settled = startedAt(r)
	}
	var open []*state.Run
	for _, r := range j.history {
		if r.Unreported(j.machine.ReportWithin(r.Command), j.now) && r.StartedAt.After(settled) {
			open = append(open, r)
		}
	}
	if len(open) == 0 {
		return Problem{}, false
	}
	sort.SliceStable(open, func(a, b int) bool { return open[a].StartedAt.After(*open[b].StartedAt) })
	return Problem{Kind: NoReport, Runs: open, Count: len(open),
		Since: *open[len(open)-1].StartedAt, Last: *open[0].StartedAt}, true
}

func (j Job) missed() (Problem, bool) {
	var series []*state.Run
	count := 0
	for _, r := range j.history {
		if r.Status != state.StatusMissed {
			break
		}
		series = append(series, r)
		count += max(1, r.MissedCount)
	}
	if len(series) == 0 {
		return Problem{}, false
	}
	return Problem{Kind: Missed, Runs: series, Count: count,
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
