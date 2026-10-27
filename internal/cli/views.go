package cli

import (
	"fmt"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/paths"
	"github.com/skelz0r/ags/internal/schedule"
	"github.com/skelz0r/ags/internal/state"
)

type JobView struct {
	Key         string      `json:"key"`
	Project     string      `json:"project"`
	Name        string      `json:"name"`
	Origin      string      `json:"origin"`
	Schedule    string      `json:"schedule"`
	Description string      `json:"description"`
	Timezone    string      `json:"timezone"`
	Command     string      `json:"command"`
	Precheck    string      `json:"precheck,omitempty"`
	PromptFile  string      `json:"prompt_file,omitempty"`
	CatchUp     string      `json:"catch_up"`
	Enabled     bool        `json:"enabled"`
	Approved    bool        `json:"approved"`
	Overrides   []string    `json:"local_overrides,omitempty"`
	Next        []time.Time `json:"next"`
	LastRun     *RunView    `json:"last_run,omitempty"`
	File        string      `json:"file"`
	Line        int         `json:"line"`
}

type RunView struct {
	ID              string     `json:"id"`
	Project         string     `json:"project"`
	Job             string     `json:"job"`
	Trigger         string     `json:"trigger,omitempty"`
	Status          string     `json:"status"`
	Reason          string     `json:"reason,omitempty"`
	ScheduledFor    time.Time  `json:"scheduled_for"`
	LateBy          string     `json:"late_by"`
	MissedCount     int        `json:"missed_count,omitempty"`
	MissedLast      *time.Time `json:"missed_last,omitempty"`
	SkippedCount    int        `json:"skipped_count,omitempty"`
	SkippedLast     *time.Time `json:"skipped_last,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	DurationSeconds float64    `json:"duration_seconds"`
	Command         string     `json:"command,omitempty"`
	Argv            []string   `json:"argv,omitempty"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	Dir             string     `json:"dir"`
}

func jobView(j *config.Job, st *state.State, now time.Time, n int, home string) JobView {
	catchUp := config.FormatDuration(j.CatchUp)
	if j.CatchUpNever {
		catchUp = "never"
	}
	return JobView{
		Key:         j.Key(),
		Project:     j.Project,
		Name:        j.Name,
		Origin:      j.Origin,
		Schedule:    j.ScheduleExpr,
		Description: schedule.Describe(j.ScheduleExpr),
		Timezone:    j.Schedule.Location.String(),
		Command:     j.CommandName,
		Precheck:    j.Precheck,
		PromptFile:  j.PromptFile,
		CatchUp:     catchUp,
		Enabled:     j.Enabled,
		Approved:    st == nil || st.Approved(j),
		Overrides:   j.LocalOverrides,
		Next:        j.Schedule.NextN(now, n),
		File:        paths.Abbreviate(j.File, home),
		Line:        j.Line,
	}
}

func runView(store *state.Store, r *state.Run) RunView {
	return RunView{
		ID:              r.ID,
		Project:         r.Project,
		Job:             r.Job,
		Trigger:         r.Trigger,
		Status:          r.Status,
		Reason:          r.Reason,
		ScheduledFor:    r.ScheduledFor,
		LateBy:          config.FormatDuration(r.LateBy()),
		MissedCount:     r.MissedCount,
		MissedLast:      r.MissedLast,
		SkippedCount:    r.SkippedCount,
		SkippedLast:     r.SkippedLast,
		StartedAt:       r.StartedAt,
		EndedAt:         r.EndedAt,
		DurationSeconds: r.Duration().Seconds(),
		Command:         r.Command,
		Argv:            r.Argv,
		ExitCode:        r.ExitCode,
		Dir:             store.RunDir(r),
	}
}

func (v RunView) LastScheduled() time.Time {
	if v.SkippedLast != nil {
		return *v.SkippedLast
	}
	return v.ScheduledFor
}

func (v RunView) Label() string {
	switch {
	case v.MissedCount > 0:
		return fmt.Sprintf("missed x%d", v.MissedCount)
	case v.SkippedCount > 1:
		return fmt.Sprintf("%s x%d", StatusLabel(v.Status, v.Reason), v.SkippedCount)
	}
	return StatusLabel(v.Status, v.Reason)
}

func StatusLabel(status, reason string) string {
	if status == state.StatusSkipped && reason != "" {
		short := reason
		if i := len(state.ReasonPrecheck); len(reason) > i && reason[:i] == state.ReasonPrecheck {
			short = state.ReasonPrecheck
		}
		return status + " (" + short + ")"
	}
	return status
}
