package daemon

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/notify"
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
	eventNoReport      = "no_report"
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
	runs := d.store.RecentRuns(p.Name, j.Name, navetteLookBack)
	if c, ok := d.streakCondition(p, item, runs, prev, posted, now, on); ok {
		return c, true
	}
	if !on(eventNoReport) {
		return navetteCondition{}, false
	}
	return d.noReportCondition(p, item, runs, prev, now)
}

func (d *Daemon) streakCondition(p *config.Project, item notify.NavetteItem, runs []*state.Run, prev state.NavetteItem, posted bool, now time.Time, on func(string) bool) (navetteCondition, bool) {
	streak := decisiveStreak(runs)
	if len(streak) == 0 {
		return navetteCondition{}, false
	}
	last, first := streak[0], streak[len(streak)-1]
	event := last.Status
	if event != state.StatusPrecheckError && event != state.StatusFailed && event != state.StatusMissed || !on(event) {
		return navetteCondition{}, false
	}
	sameEvent := posted && strings.HasPrefix(prev.Signature, event+":")
	if !sameEvent && now.Sub(state.OccurredAt(last)) > state.UnreportedLookBack {
		return navetteCondition{}, false
	}
	since, sig, count := first.ScheduledFor, event+":"+first.ID, ""
	truncated := first == runs[len(runs)-1] && len(runs) == navetteLookBack
	if truncated && sameEvent && !prev.Since.IsZero() && prev.Since.Before(since) {
		since, sig = prev.Since, prev.Signature
	}
	at := func(t time.Time) string { return t.In(p.Location).Format(timeLayout) }
	key := strings.TrimPrefix(item.Key, "ags:")
	key = strings.Replace(key, ":", "/", 1)
	c := navetteCondition{event: event, sig: sig, since: since}
	if event == state.StatusMissed {
		missed := 0
		for _, r := range streak {
			missed += max(r.MissedCount, 1)
		}
		count = plural(missed, "occurrence")
		if truncated {
			count = fmt.Sprintf("%d+ occurrences", missed)
		}
		lastMissed := last.ScheduledFor
		if last.MissedLast != nil {
			lastMissed = *last.MissedLast
		}
		item.Title, item.Status = key+" missed occurrences", navetteInfo
		item.Description = fmt.Sprintf("%s missed since %s, the last at %s: the Mac was asleep or off, or the daemon stopped.",
			count, at(since), at(lastMissed))
		c.item = item
		return c, true
	}
	count = plural(len(streak), "run")
	if truncated {
		count = fmt.Sprintf("%d+ runs", len(streak))
	}
	what := "Failing"
	item.Title = key + " failed"
	if event == state.StatusPrecheckError {
		what, item.Title = "Precheck failing", key+": precheck fails"
	}
	item.Status = navetteWaiting
	item.Description = fmt.Sprintf("%s since %s (%s). Last: %s.\n\nDetails: `ags show %s`.",
		what, at(since), count, shortReason(last), last.ID)
	c.item = item
	return c, true
}

func (d *Daemon) noReportCondition(p *config.Project, item notify.NavetteItem, runs []*state.Run, prev state.NavetteItem, now time.Time) (navetteCondition, bool) {
	tracked, open := strings.CutPrefix(prev.Signature, eventNoReport+":")
	if open {
		if r, err := d.store.FindRun(tracked); err == nil {
			runs = append([]*state.Run{r}, runs...)
		}
	}
	for _, r := range runs {
		within := d.machine.ReportWithin(r.Command)
		overdue := r.Unreported(within, now)
		if open && r.ID == tracked && r.StartedAt != nil {
			overdue = r.Unreported(within, r.StartedAt.Add(within+time.Second))
		}
		if !overdue {
			continue
		}
		key := strings.Replace(strings.TrimPrefix(item.Key, "ags:"), ":", "/", 1)
		item.Title, item.Status = key+": no report", navetteWaiting
		item.Description = fmt.Sprintf("Run handed off at %s, no `ags report` within %s: look at the agent it was handed to.\n\nDetails: `ags show %s`.",
			r.StartedAt.In(p.Location).Format(timeLayout), config.FormatDuration(within), r.ID)
		return navetteCondition{event: eventNoReport, item: item, sig: eventNoReport + ":" + r.ID}, true
	}
	return navetteCondition{}, false
}

func decisiveStreak(runs []*state.Run) []*state.Run {
	var streak []*state.Run
	for _, r := range runs {
		if r.Status == state.StatusRunning || r.Status == state.StatusSkipped && !strings.HasPrefix(r.Reason, state.ReasonPrecheck) {
			continue
		}
		if len(streak) > 0 && r.Status == state.StatusMissed && streak[0].Status != state.StatusMissed {
			continue
		}
		if len(streak) > 0 && (r.Status != streak[0].Status || r.Status == state.StatusSucceeded) {
			break
		}
		streak = append(streak, r)
	}
	return streak
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
		return "The last run did not fail."
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
