package daemon

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/notify"
	"github.com/skelz0r/ags/internal/problem"
	"github.com/skelz0r/ags/internal/state"
)

const (
	NavetteRepostEvery = time.Hour
	NavetteRetryAfter  = 5 * time.Minute
	navetteLookBack    = 30
	navetteWaiting     = "waiting"
	navetteInfo        = "info"
	navetteDone        = "done"
	eventInvalid       = "invalid"
	eventUnapproved    = "unapproved"
	eventNoReport      = problem.NoReport
	timeLayout         = "Mon 02 Jan 15:04"
)

type NavettePoster interface {
	Post(ctx context.Context, item notify.NavetteItem) error
}

type navetteCondition struct {
	event string
	item  notify.NavetteItem
	sig   string
	since time.Time
}

type navettePost struct {
	item   notify.NavetteItem
	record *state.NavetteItem
}

func (d *Daemon) reconcileNavette(now time.Time) {
	if d.opts.Navette == nil || d.machine == nil || !d.machine.HasChannel(config.ChannelNavette) || now.Before(d.holdUntil) {
		return
	}
	d.mu.Lock()
	idle := !d.navetteBusy && !now.Before(d.navetteRetryAt)
	if idle {
		d.navetteBusy = true
	}
	d.mu.Unlock()
	if !idle {
		return
	}
	posts := d.navettePosts(now)
	if len(posts) == 0 {
		d.mu.Lock()
		d.navetteBusy = false
		d.mu.Unlock()
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			d.navetteBusy = false
			d.mu.Unlock()
		}()
		d.sendNavette(posts, now)
	}()
}

func (d *Daemon) sendNavette(posts []navettePost, now time.Time) {
	for _, p := range posts {
		if err := d.opts.Navette.Post(d.ctx, p.item); err != nil {
			d.mu.Lock()
			d.navetteRetryAt = now.Add(NavetteRetryAfter)
			first := !d.navetteFailing
			d.navetteFailing = true
			d.mu.Unlock()
			if first {
				d.logf("navette: %s not posted, retrying every %s: %v", p.item.Key, config.FormatDuration(NavetteRetryAfter), err)
			}
			return
		}
		d.mu.Lock()
		recovered := d.navetteFailing
		d.navetteFailing = false
		d.mu.Unlock()
		if recovered {
			d.logf("navette: posting again")
		}
		d.store.Update(func(st *state.State) error {
			if p.record != nil {
				st.Navette[p.item.Key] = *p.record
			} else {
				delete(st.Navette, p.item.Key)
			}
			return nil
		})
		d.logf("navette: %s posted %s", p.item.Key, p.item.Status)
	}
}

func (d *Daemon) navettePosts(now time.Time) []navettePost {
	st, err := d.store.Read()
	if err != nil {
		return nil
	}
	open, kept := d.navetteConditions(st, now)
	var posts []navettePost
	for key, c := range open {
		prev, posted := st.Navette[key]
		if posted && prev.Signature == c.sig && (prev.Description == c.item.Description || now.Sub(prev.PostedAt) < NavetteRepostEvery) {
			continue
		}
		posts = append(posts, navettePost{item: c.item, record: &state.NavetteItem{
			Title: c.item.Title, Dir: c.item.Dir, Signature: c.sig, Since: c.since, Description: c.item.Description, PostedAt: now,
		}})
	}
	for key, prev := range st.Navette {
		if _, ok := open[key]; ok || kept[key] {
			continue
		}
		posts = append(posts, navettePost{item: notify.NavetteItem{
			Key: key, Dir: prev.Dir, Title: prev.Title, Status: navetteDone, Description: d.resolution(key, prev),
		}})
	}
	sort.Slice(posts, func(i, j int) bool { return posts[i].item.Key < posts[j].item.Key })
	return posts
}

func (d *Daemon) navetteConditions(st *state.State, now time.Time) (open map[string]navetteCondition, kept map[string]bool) {
	open, kept = map[string]navetteCondition{}, map[string]bool{}
	on := func(event string) bool { return d.machine.Notifies(config.ChannelNavette, event) }
	if on(eventInvalid) {
		for source, e := range st.ConfigErrors {
			c := d.configCondition(source, e)
			open[c.item.Key] = c
		}
	}
	if _, broken := st.ConfigErrors[registryKey]; broken {
		for key := range st.Navette {
			kept[key] = key != navetteConfigKey(machineKey) && key != navetteConfigKey(registryKey)
		}
	}
	for _, name := range d.projectNames() {
		lp := d.projects[name]
		if lp.project == nil {
			for key := range st.Navette {
				if strings.HasPrefix(key, navettePrefix(name)) {
					kept[key] = true
				}
			}
			continue
		}
		for _, j := range lp.project.Jobs {
			if c, ok := d.jobCondition(st, lp.project, j, now, on); ok {
				open[c.item.Key] = c
			}
		}
	}
	return open, kept
}

func navettePrefix(project string) string { return "ags:" + project + ":" }

func navetteConfigKey(source string) string {
	switch source {
	case machineKey:
		return "ags:machine:@config"
	case registryKey:
		return "ags:registry:@config"
	}
	return navettePrefix(source) + "@config"
}

func (d *Daemon) configCondition(source string, e state.ConfigError) navetteCondition {
	dir := ""
	label := strings.TrimPrefix(source, "@")
	if lp := d.projects[source]; lp != nil {
		dir = lp.entry.Path
	}
	return navetteCondition{
		event: eventInvalid,
		sig:   eventInvalid + ":" + e.Message,
		item: notify.NavetteItem{
			Key:    navetteConfigKey(source),
			Dir:    dir,
			Title:  "ags: invalid config for " + label,
			Status: navetteWaiting,
			Description: fmt.Sprintf("%s\n\nSince %s. Fix the file: `ags validate` shows every error, the daemon keeps the last valid version meanwhile.",
				firstLine(e.Message), e.Since.Local().Format(timeLayout)),
		},
	}
}

func (d *Daemon) jobCondition(st *state.State, p *config.Project, j *config.Job, now time.Time, on func(string) bool) (navetteCondition, bool) {
	if !j.Enabled {
		return navetteCondition{}, false
	}
	key := j.Key()
	item := notify.NavetteItem{Key: navettePrefix(p.Name) + j.Name, Dir: p.Dir}
	if !st.Approved(j) {
		if !on(eventUnapproved) {
			return navetteCondition{}, false
		}
		item.Title, item.Status = key+" needs approval", navetteWaiting
		item.Description = "Its prompt, precheck or command changed since you approved it, it does not run meanwhile: `ags approve " + key + "` shows the diff."
		return navetteCondition{event: eventUnapproved, item: item, sig: eventUnapproved + ":" + j.Fingerprint}, true
	}
	prev, posted := st.Navette[item.Key]
	job := problem.Of(d.store, d.machine, d.store.RecentRuns(p.Name, j.Name, navetteLookBack), now)
	full := len(job.Runs()) == navetteLookBack
	byKind := map[string]problem.Problem{}
	for _, pb := range job.Problems() {
		byKind[pb.Kind] = pb
	}
	for _, kind := range []string{problem.PrecheckError, problem.Failed, problem.NoReport, problem.Missed} {
		if !on(kind) {
			continue
		}
		if kind == problem.NoReport {
			if c, ok := d.noReportCondition(p, item, job, prev); ok {
				return c, true
			}
			continue
		}
		pb, found := byKind[kind]
		if !found {
			continue
		}
		sameEvent := posted && strings.HasPrefix(prev.Signature, kind+":")
		if !sameEvent && !pb.Recent(now) {
			continue
		}
		return seriesCondition(p, item, pb, pb.ToEnd && full, prev, sameEvent), true
	}
	return navetteCondition{}, false
}

func seriesCondition(p *config.Project, item notify.NavetteItem, pb problem.Problem, truncated bool, prev state.NavetteItem, sameEvent bool) navetteCondition {
	last, first := pb.Latest(), pb.Runs[len(pb.Runs)-1]
	since, sig := first.ScheduledFor, pb.Kind+":"+first.ID
	if truncated && sameEvent && !prev.Since.IsZero() && !prev.Since.After(first.HappenedAt()) {
		since, sig = prev.Since, prev.Signature
	}
	at := func(t time.Time) string { return t.In(p.Location).Format(timeLayout) }
	key := strings.Replace(strings.TrimPrefix(item.Key, "ags:"), ":", "/", 1)
	unit := "run"
	if pb.Kind == problem.Missed {
		unit = "occurrence"
	}
	count := plural(pb.Count, unit)
	if truncated {
		count = fmt.Sprintf("%d+ %ss", pb.Count, unit)
	}
	switch pb.Kind {
	case problem.Missed:
		item.Title, item.Status = key+" missed occurrences", navetteInfo
		item.Description = fmt.Sprintf("%s missed since %s, the last at %s: the Mac was asleep or off, or the daemon stopped.",
			count, at(since), at(pb.Last))
	case problem.Failed:
		item.Title, item.Status = key+" en échec", navetteWaiting
		item.Description = failedDescription(key, last, at(since), pb.Count, truncated)
	default:
		item.Title, item.Status = key+": precheck fails", navetteWaiting
		item.Description = fmt.Sprintf("Precheck failing since %s (%s). Last: %s.\n\nDetails: `ags show %s`.",
			at(since), count, shortReason(last), last.ID)
	}
	return navetteCondition{event: pb.Kind, item: item, sig: sig, since: since}
}

func failedDescription(key string, last *state.Run, since string, count int, truncated bool) string {
	runs := fmt.Sprintf("%d runs", count)
	switch {
	case truncated:
		runs = fmt.Sprintf("%d+ runs", count)
	case count == 1:
		runs = "1 run"
	}
	exit := describeExit(last)
	var b strings.Builder
	fmt.Fprintf(&b, "En échec depuis %s (%s). Dernier run : %s.", since, runs, failureCause(last, exit))
	switch {
	case last.StagedState == state.StagedDiscarded && last.PrecheckFound:
		b.WriteString("\n\nLe precheck avait trouvé du travail : son état n'a pas été validé, le prochain run le retrouvera.")
	case last.StagedState == state.StagedDiscarded:
		b.WriteString("\n\nL'état préparé par le precheck n'a pas été validé : le prochain run repart du même point.")
	case last.PrecheckFound:
		b.WriteString("\n\nLe precheck avait trouvé du travail, peut-être perdu : s'il a avancé son curseur dans `AGS_JOB_STATE_DIR`, le prochain run ne le reverra pas (`AGS_STAGED_STATE_DIR` évite cette perte).")
	}
	if last.StderrTail != "" {
		fence := codeFence(last.StderrTail)
		fmt.Fprintf(&b, "\n\nFin du stderr :\n\n%s\n%s\n%s", fence, last.StderrTail, fence)
	}
	fmt.Fprintf(&b, "\n\nRelancer : `ags run %s`. Détails : `ags show %s`.", key, last.ID)
	return b.String()
}

func failureCause(r *state.Run, exit string) string {
	took := formatTook(problem.Duration(r))
	switch problem.Cause(r) {
	case problem.CauseNotStarted:
		return "la commande n'a pas pu démarrer (" + strings.TrimPrefix(r.Reason, "start: ") + ")"
	case problem.CauseInterrupted:
		return "interrompu, le daemon s'est arrêté pendant le run"
	case problem.CauseTimeout:
		return "l'agent a dépassé le timeout de sa commande (" + r.Reason + ")"
	case problem.CauseCommand:
		return fmt.Sprintf("la commande a échoué (%s après %s)", exit, took)
	case problem.CauseNoAgent:
		return fmt.Sprintf("la commande n'a probablement pas lancé l'agent (%s après %s, sans rapport)", exit, took)
	}
	return fmt.Sprintf("l'agent a échoué (%s après %s)", exit, took)
}

func describeExit(r *state.Run) string {
	switch {
	case r.ExitCode == nil:
		return shortReason(r)
	case *r.ExitCode < 0:
		return "tué par un signal"
	}
	return fmt.Sprintf("exit %d", *r.ExitCode)
}

func formatTook(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Round(time.Second)/time.Second))
	}
	return config.FormatDuration(d)
}

func codeFence(text string) string {
	longest, run := 0, 0
	for _, c := range text {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func (d *Daemon) noReportCondition(p *config.Project, item notify.NavetteItem, job problem.Job, prev state.NavetteItem) (navetteCondition, bool) {
	runs := job.Unreported()
	tracked, open := strings.CutPrefix(prev.Signature, eventNoReport+":")
	if open {
		if r, err := d.store.FindRun(tracked); err == nil && r.StartedAt != nil {
			within := d.machine.ReportWithin(r.Command)
			if r.Unreported(within, r.StartedAt.Add(within+time.Second)) {
				runs = append([]*state.Run{r}, runs...)
			}
		}
	}
	if len(runs) == 0 {
		return navetteCondition{}, false
	}
	r := runs[0]
	key := strings.Replace(strings.TrimPrefix(item.Key, "ags:"), ":", "/", 1)
	item.Title, item.Status = key+": no report", navetteWaiting
	item.Description = fmt.Sprintf("Run handed off at %s, no `ags report` within %s: look at the agent it was handed to.\n\nDetails: `ags show %s`.",
		r.StartedAt.In(p.Location).Format(timeLayout), config.FormatDuration(d.machine.ReportWithin(r.Command)), r.ID)
	return navetteCondition{event: eventNoReport, item: item, sig: eventNoReport + ":" + r.ID}, true
}

func (d *Daemon) resolution(key string, prev state.NavetteItem) string {
	event, _, _ := strings.Cut(prev.Signature, ":")
	if event == eventInvalid {
		return "Config fixed."
	}
	j := d.configuredJob(key)
	switch {
	case j == nil:
		return "No longer configured in ags."
	case !j.Enabled:
		return "Job paused."
	}
	switch event {
	case eventUnapproved:
		return "Approved."
	case eventNoReport:
		return "No longer awaiting a report."
	case state.StatusPrecheckError:
		return "The precheck passes again."
	case state.StatusFailed:
		return "Le dernier run n'a pas échoué."
	case state.StatusMissed:
		return "Ran again."
	}
	return "Resolved."
}

func (d *Daemon) configuredJob(key string) *config.Job {
	for name, lp := range d.projects {
		if lp.project == nil {
			continue
		}
		for _, j := range lp.project.Jobs {
			if key == navettePrefix(name)+j.Name {
				return j
			}
		}
	}
	return nil
}

func shortReason(r *state.Run) string {
	reason, _, _ := strings.Cut(r.Reason, ": ")
	if reason == "" {
		return r.Status
	}
	return reason
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
