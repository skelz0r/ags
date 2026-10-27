package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/paths"
	"github.com/skelz0r/ags/internal/runner"
	"github.com/skelz0r/ags/internal/state"
)

const (
	TickInterval  = 30 * time.Second
	OnTime        = 2 * TickInterval
	WakeDelay     = 60 * time.Second
	MaxConcurrent = 4
	machineKey    = "@machine"
)

var ErrAlreadyRunning = errors.New("another ags daemon is running")

type Options struct {
	Paths    paths.Paths
	Notifier runner.Notifier
	Log      io.Writer
	Now      func() time.Time
}

type loadedProject struct {
	entry     config.Entry
	project   *config.Project
	signature string
	sources   []string
	err       string
	machine   *config.Machine
}

type Daemon struct {
	opts          Options
	store         *state.Store
	machine       *config.Machine
	machineSig    string
	projects      map[string]*loadedProject
	holdUntil     time.Time
	machineError  string
	pendingErrors []loadError

	mu      sync.Mutex
	running map[string]*state.Run
	sem     chan struct{}
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

func New(opts Options) (*Daemon, error) {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	store, err := state.Open(opts.Paths)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{
		opts:     opts,
		store:    store,
		projects: map[string]*loadedProject{},
		running:  map[string]*state.Run{},
		sem:      make(chan struct{}, MaxConcurrent),
		ctx:      ctx,
		cancel:   cancel,
	}
	return d, d.recoverInterrupted()
}

func (d *Daemon) logf(format string, args ...any) {
	fmt.Fprintf(d.opts.Log, "%s %s\n", d.opts.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func (d *Daemon) recoverInterrupted() error {
	runs, err := d.store.ListRuns(state.Filter{Status: state.StatusRunning})
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.OwnerPID != os.Getpid() && state.ProcessAlive(r.OwnerPID) {
			continue
		}
		now := d.opts.Now()
		r.Status, r.Reason, r.EndedAt = state.StatusFailed, state.ReasonInterrupted, &now
		d.store.SaveRun(r)
		d.logf("run %s of %s marked failed (interrupted)", r.ID, r.Key())
	}
	return nil
}

func (d *Daemon) Run(ctx context.Context) error {
	lock, err := os.OpenFile(d.opts.Paths.DaemonLock(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return ErrAlreadyRunning
	}
	started := d.opts.Now()
	d.store.Update(func(st *state.State) error {
		st.Daemon.PID, st.Daemon.StartedAt = os.Getpid(), started
		return nil
	})
	d.logf("daemon started, pid %d", os.Getpid())
	next := started
	for {
		now := d.opts.Now().Round(0)
		if !now.Before(next) {
			if err := d.Tick(now); err != nil {
				d.logf("tick: %v", err)
			}
			next = now.Truncate(TickInterval).Add(TickInterval)
		}
		select {
		case <-ctx.Done():
			d.logf("daemon stopping")
			d.Stop()
			return nil
		case <-time.After(time.Second):
		}
	}
}

func (d *Daemon) Stop() {
	d.cancel()
	d.wg.Wait()
}

func (d *Daemon) Wait() {
	d.wg.Wait()
}

type decision struct {
	machine *config.Machine
	project *config.Project
	job     *config.Job
	records []*state.Run
	start   *state.Run
	notify  []note
}

type note struct{ event, title, message string }

func (d *Daemon) Tick(now time.Time) error {
	d.reload()
	var reloadNotes []note
	var decisions []*decision
	err := d.store.Update(func(st *state.State) error {
		last := st.Daemon.LastTick
		st.Daemon.LastTick = now
		if st.Daemon.PID == 0 {
			st.Daemon.PID = os.Getpid()
		}
		d.recordConfigErrors(st, now, &reloadNotes)
		if !last.IsZero() && now.Sub(last) > OnTime {
			d.holdUntil = now.Add(WakeDelay)
			d.logf("wake after %s, firing waits until %s", now.Sub(last).Round(time.Second), d.holdUntil.Format(time.RFC3339))
		}
		if now.Before(d.holdUntil) {
			return nil
		}
		decisions = d.decide(st, now)
		d.reserve(decisions)
		if now.Sub(st.LastPrune) > 24*time.Hour {
			st.LastPrune = now
			go d.store.Prune(state.KeepPerJob)
		}
		return nil
	})
	if err != nil {
		d.release(decisions, err)
		return err
	}
	for _, n := range reloadNotes {
		d.notify(n)
	}
	for _, dec := range decisions {
		for _, r := range dec.records {
			if entry, _ := d.store.Record(r); entry == r {
				d.logf("%s %s: %s %s", r.Key(), r.ScheduledFor.Format(time.RFC3339), r.Status, r.Reason)
			}
		}
		for _, n := range dec.notify {
			d.notify(n)
		}
		if dec.start != nil {
			d.start(dec)
		}
	}
	return nil
}

func (d *Daemon) notify(n note) {
	if d.opts.Notifier != nil {
		d.opts.Notifier.Notify(n.event, n.title, n.message)
	}
}

func (d *Daemon) decide(st *state.State, now time.Time) []*decision {
	var out []*decision
	known := map[string]bool{}
	for _, name := range d.projectNames() {
		lp := d.projects[name]
		if lp.project == nil {
			for key := range st.Cursors {
				if strings.HasPrefix(key, name+"/") {
					known[key] = true
				}
			}
			continue
		}
		for _, job := range lp.project.Jobs {
			known[job.Key()] = true
			if dec := d.decideJob(st, lp.project, job, now); dec != nil {
				dec.machine = lp.machine
				out = append(out, dec)
			}
		}
	}
	for key := range st.Cursors {
		if !known[key] {
			delete(st.Cursors, key)
			delete(st.CursorSchedules, key)
		}
	}
	return out
}

func (d *Daemon) decideJob(st *state.State, p *config.Project, j *config.Job, now time.Time) *decision {
	key := j.Key()
	cursor, ok := st.Cursors[key]
	previous, recorded := st.CursorSchedules[key]
	st.CursorSchedules[key] = j.Schedule.String()
	if !ok {
		st.Cursors[key] = now
		d.logf("%s: new job, first occurrence %s", key, j.Schedule.Next(now).Format(time.RFC3339))
		return nil
	}
	if recorded && previous != j.Schedule.String() {
		st.Cursors[key] = now
		d.logf("%s: schedule changed from %q to %q, first occurrence %s", key, previous, j.Schedule.String(), j.Schedule.Next(now).Format(time.RFC3339))
		return nil
	}
	due, ok := j.Schedule.Due(cursor, now)
	if !ok {
		return nil
	}
	st.Cursors[key] = due.Latest
	dec := &decision{project: p, job: j}
	record := func(status, reason string, scheduled time.Time) *state.Run {
		r := runner.NewRun(p, j, "", scheduled, now)
		r.Status, r.Reason, r.StartedAt, r.EndedAt = status, reason, nil, nil
		r.Command = ""
		dec.records = append(dec.records, r)
		return r
	}
	if !j.Enabled {
		record(state.StatusSkipped, state.ReasonPaused, due.Latest)
		return dec
	}
	late := now.Sub(due.Latest)
	latestMissed := late > OnTime && (j.CatchUpNever || late > j.CatchUp)
	missedCount := due.Earlier
	if latestMissed {
		missedCount++
	}
	if missedCount > 0 {
		r := record(state.StatusMissed, "", due.First)
		r.MissedCount = missedCount
		r.LateBySeconds = int64(now.Sub(due.First) / time.Second)
		last := due.BeforeLatest
		if latestMissed {
			last = due.Latest
		}
		r.MissedLast = &last
		dec.notify = append(dec.notify, note{state.StatusMissed, "ags: " + key + " missed",
			fmt.Sprintf("%d occurrence(s) missed, the last at %s", missedCount, last.In(p.Location).Format("Mon 02 Jan 15:04"))})
	}
	if latestMissed {
		return dec
	}
	trigger := state.TriggerSchedule
	if late > OnTime {
		trigger = state.TriggerCatchUp
	}
	if !st.Approved(j) {
		r := record(state.StatusSkipped, state.ReasonUnapproved, due.Latest)
		r.Trigger = trigger
		if st.Notified["unapproved:"+key] != j.Fingerprint {
			st.Notified["unapproved:"+key] = j.Fingerprint
			dec.notify = append(dec.notify, note{"unapproved", "ags: " + key + " needs approval",
				"its prompt, precheck or command changed: run ags approve " + key})
		}
		return dec
	}
	if d.isRunning(j) {
		r := record(state.StatusSkipped, state.ReasonOverlap, due.Latest)
		r.Trigger = trigger
		return dec
	}
	dec.start = runner.NewRun(p, j, trigger, due.Latest, now)
	return dec
}

func (d *Daemon) isRunning(j *config.Job) bool {
	d.mu.Lock()
	_, mine := d.running[j.Key()]
	d.mu.Unlock()
	if mine {
		return true
	}
	runs, _ := d.store.ListRuns(state.Filter{Project: j.Project, Job: j.Name, Status: state.StatusRunning})
	for _, r := range runs {
		if r.OwnerPID != os.Getpid() && state.ProcessAlive(r.OwnerPID) {
			return true
		}
	}
	return false
}

func (d *Daemon) reserve(decisions []*decision) {
	for _, dec := range decisions {
		if dec.start == nil {
			continue
		}
		if err := d.store.SaveRun(dec.start); err != nil {
			d.logf("%s: cannot record run: %v", dec.start.Key(), err)
			dec.start = nil
			continue
		}
		d.mu.Lock()
		d.running[dec.start.Key()] = dec.start
		d.mu.Unlock()
	}
}

func (d *Daemon) release(decisions []*decision, cause error) {
	for _, dec := range decisions {
		if dec.start == nil {
			continue
		}
		d.mu.Lock()
		delete(d.running, dec.start.Key())
		d.mu.Unlock()
		now := d.opts.Now()
		dec.start.Status, dec.start.Reason, dec.start.EndedAt = state.StatusFailed, "state not saved: "+cause.Error(), &now
		d.store.SaveRun(dec.start)
	}
}

func (d *Daemon) start(dec *decision) {
	run := dec.start
	rn := &runner.Runner{Store: d.store, Machine: dec.machine, Notifier: d.opts.Notifier, Now: d.opts.Now}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			delete(d.running, run.Key())
			d.mu.Unlock()
		}()
		select {
		case d.sem <- struct{}{}:
		case <-d.ctx.Done():
			now := d.opts.Now()
			run.Status, run.Reason, run.EndedAt = state.StatusFailed, state.ReasonInterrupted, &now
			d.store.SaveRun(run)
			return
		}
		defer func() { <-d.sem }()
		started := func() {
			d.logf("%s %s: started run %s (%s, late by %s)", run.Key(), run.ScheduledFor.Format(time.RFC3339), run.ID, run.Trigger, config.FormatDuration(run.LateBy()))
		}
		result := rn.Execute(d.ctx, runner.Request{Project: dec.project, Job: dec.job, Run: run, OnStart: started})
		if result == run {
			d.logf("%s: run %s %s %s", run.Key(), run.ID, result.Status, result.Reason)
		}
	}()
}

func (d *Daemon) projectNames() []string {
	names := make([]string, 0, len(d.projects))
	for name := range d.projects {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
