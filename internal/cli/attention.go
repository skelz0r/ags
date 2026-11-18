package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/paths"
	"github.com/skelz0r/ags/internal/state"
)

const (
	AttentionDaemon        = "daemon"
	AttentionConfig        = "config"
	AttentionPrecheckError = state.StatusPrecheckError
	AttentionFailed        = state.StatusFailed
	AttentionBlocked       = state.ReportBlocked
	AttentionAgentFailed   = "agent_failed"
	AttentionNoReport      = FeedNoReport
	AttentionMissed        = state.StatusMissed
	AttentionUnapproved    = "unapproved"

	attentionWindow = 7 * 24 * time.Hour
	recentWindow    = 24 * time.Hour
)

var attentionOrder = []string{AttentionDaemon, AttentionConfig, AttentionPrecheckError, AttentionFailed, AttentionBlocked, AttentionAgentFailed, AttentionNoReport, AttentionMissed, AttentionUnapproved}

type AttentionItem struct {
	Kind        string     `json:"kind"`
	Key         string     `json:"key,omitempty"`
	Project     string     `json:"project,omitempty"`
	Job         string     `json:"job,omitempty"`
	Count       int        `json:"count,omitempty"`
	Since       *time.Time `json:"since,omitempty"`
	Last        *time.Time `json:"last,omitempty"`
	Summary     string     `json:"summary"`
	Run         string     `json:"run,omitempty"`
	Ref         string     `json:"ref,omitempty"`
	File        string     `json:"file,omitempty"`
	Line        int        `json:"line,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
}

type DaemonView struct {
	Alive    bool       `json:"alive"`
	Detail   string     `json:"detail"`
	LastTick *time.Time `json:"last_tick,omitempty"`
}

type Attention struct {
	Daemon DaemonView      `json:"daemon"`
	Items  []AttentionItem `json:"items"`
	Recent []FeedEntry     `json:"recent"`
}

func (a *App) attention(args []string) (result, error) {
	f := newFlags("attention")
	if err := f.parse(args, 0, 0); err != nil {
		return result{}, err
	}
	at, err := a.collectAttention()
	if err != nil {
		return result{}, err
	}
	return result{data: at, text: attentionText(at, a.Now())}, nil
}

func (a *App) collectAttention() (Attention, error) {
	now := a.Now()
	store, err := a.store()
	if err != nil {
		return Attention{}, err
	}
	st, err := store.Read()
	if err != nil {
		return Attention{}, err
	}
	at := Attention{Items: []AttentionItem{}, Recent: []FeedEntry{}}
	alive, detail := DaemonAlive(st, now)
	at.Daemon = DaemonView{Alive: alive, Detail: detail}
	if !st.Daemon.LastTick.IsZero() {
		tick := st.Daemon.LastTick
		at.Daemon.LastTick = &tick
	}
	if !alive {
		at.Items = append(at.Items, AttentionItem{Kind: AttentionDaemon, Summary: "daemon down: " + detail})
	}

	m, machineIssues := a.loadMachine()
	home := a.Getenv("HOME")
	for _, issue := range machineIssues {
		at.Items = append(at.Items, configItem("", issue, home))
	}
	known := map[string]bool{}
	invalid := map[string]bool{}
	reg, err := a.registry()
	if err != nil {
		return Attention{}, err
	}
	for _, entry := range reg.Projects {
		p, issues := config.Loader{Machine: m, SkipGit: true}.Load(entry.Name, entry.Path)
		if len(issues) > 0 {
			invalid[entry.Name] = true
			for _, issue := range issues {
				at.Items = append(at.Items, configItem(entry.Name, issue, home))
			}
			continue
		}
		for _, j := range p.Jobs {
			known[j.Key()] = true
			if st.Approved(j) || !j.Enabled {
				continue
			}
			summary := "changed since its approval: review the diff"
			if _, seen := st.Approvals[j.Key()]; !seen {
				summary = "never approved"
			}
			at.Items = append(at.Items, AttentionItem{Kind: AttentionUnapproved, Key: j.Key(), Project: j.Project, Job: j.Name, Summary: summary, Fingerprint: j.Fingerprint, File: paths.Abbreviate(j.File, home), Line: j.Line})
		}
	}

	runs, err := store.ListRuns(state.Filter{})
	if err != nil {
		return Attention{}, err
	}
	var kept []*state.Run
	for _, r := range runs {
		if known[r.Key()] || invalid[r.Project] {
			kept = append(kept, r)
		}
	}
	at.Items = append(at.Items, runItems(store, m, kept, now)...)
	at.Recent = recentReports(kept, now)

	rank := map[string]int{}
	for i, k := range attentionOrder {
		rank[k] = i
	}
	sort.SliceStable(at.Items, func(i, j int) bool {
		x, y := at.Items[i], at.Items[j]
		if rank[x.Kind] != rank[y.Kind] {
			return rank[x.Kind] < rank[y.Kind]
		}
		if x.Last != nil && y.Last != nil && !x.Last.Equal(*y.Last) {
			return x.Last.After(*y.Last)
		}
		return x.Key < y.Key
	})
	return at, nil
}

func configItem(project string, issue config.Issue, home string) AttentionItem {
	return AttentionItem{Kind: AttentionConfig, Project: project, Summary: issue.Message, File: paths.Abbreviate(issue.File, home), Line: issue.Line}
}

func runItems(store *state.Store, m *config.Machine, runs []*state.Run, now time.Time) []AttentionItem {
	byJob := map[string][]*state.Run{}
	var keys []string
	for _, r := range runs {
		if byJob[r.Key()] == nil {
			keys = append(keys, r.Key())
		}
		byJob[r.Key()] = append(byJob[r.Key()], r)
	}
	for _, history := range byJob {
		sort.SliceStable(history, func(i, j int) bool { return happenedAt(history[i]).After(happenedAt(history[j])) })
	}
	var items []AttentionItem
	for _, r := range brokenPrechecks(store, runs, now) {
		items = append(items, precheckItem(store, r, byJob[r.Key()]))
	}
	unreported := map[string][]*state.Run{}
	for _, r := range unreportedRuns(runs, m, now) {
		unreported[r.Key()] = append(unreported[r.Key()], r)
	}
	since := now.Add(-attentionWindow)
	for _, key := range keys {
		history := byJob[key]
		if item, ok := failedItem(history, since); ok {
			items = append(items, item)
		}
		if item, ok := reportItem(history, since); ok {
			items = append(items, item)
		}
		if item, ok := noReportItem(history, unreported[key], m); ok {
			items = append(items, item)
		}
		if item, ok := missedItem(history, since); ok {
			items = append(items, item)
		}
	}
	return items
}

func jobItem(kind string, r *state.Run) AttentionItem {
	return AttentionItem{Kind: kind, Key: r.Key(), Project: r.Project, Job: r.Job, Run: r.ID, Count: 1}
}

func (i *AttentionItem) span(first, last time.Time) {
	i.Since, i.Last = &first, &last
}

func precheckItem(store *state.Store, latest *state.Run, history []*state.Run) AttentionItem {
	item := jobItem(AttentionPrecheckError, latest)
	item.Count = 0
	first := precheckRanAt(latest)
	for _, r := range history {
		if !precheckRan(store, r) || precheckRanAt(r).After(precheckRanAt(latest)) {
			continue
		}
		if r.Status != state.StatusPrecheckError {
			break
		}
		item.Count++
		first = precheckRanAt(r)
	}
	item.span(first, precheckRanAt(latest))
	item.Summary = oneLine(latest.Reason)
	return item
}

func failedItem(history []*state.Run, since time.Time) (AttentionItem, bool) {
	var series []*state.Run
	for _, r := range history {
		if !r.CommandStarted() || r.Status == state.StatusRunning {
			continue
		}
		if r.Status != state.StatusFailed {
			break
		}
		series = append(series, r)
	}
	if len(series) == 0 || endedAt(series[0]).Before(since) {
		return AttentionItem{}, false
	}
	item := jobItem(AttentionFailed, series[0])
	item.Count = len(series)
	item.span(startedAt(series[len(series)-1]), endedAt(series[0]))
	item.Summary = oneLine(series[0].Reason)
	if item.Summary == "" && series[0].ExitCode != nil {
		item.Summary = fmt.Sprintf("exit %d", *series[0].ExitCode)
	}
	return item, true
}

func settles(r *state.Run) bool {
	if r.Report != nil {
		return true
	}
	return r.Status == state.StatusSucceeded && !r.AwaitsReport
}

func reportItem(history []*state.Run, since time.Time) (AttentionItem, bool) {
	for _, r := range history {
		if !r.CommandStarted() || !settles(r) {
			continue
		}
		if r.Report == nil || r.Report.At.Before(since) {
			return AttentionItem{}, false
		}
		kind := AttentionBlocked
		switch r.Report.Status {
		case state.ReportBlocked:
		case state.ReportFailed:
			kind = AttentionAgentFailed
		default:
			return AttentionItem{}, false
		}
		item := jobItem(kind, r)
		item.span(r.Report.At, r.Report.At)
		item.Summary, item.Ref = oneLine(r.Report.Summary), r.Report.Ref
		return item, true
	}
	return AttentionItem{}, false
}

func noReportItem(history, unreported []*state.Run, m *config.Machine) (AttentionItem, bool) {
	var settled time.Time
	for _, r := range history {
		if r.CommandStarted() && settles(r) {
			settled = startedAt(r)
			break
		}
	}
	var open []*state.Run
	for _, r := range unreported {
		if r.StartedAt.After(settled) {
			open = append(open, r)
		}
	}
	if len(open) == 0 {
		return AttentionItem{}, false
	}
	sort.Slice(open, func(i, j int) bool { return open[i].StartedAt.After(*open[j].StartedAt) })
	item := jobItem(AttentionNoReport, open[0])
	item.Count = len(open)
	item.span(*open[len(open)-1].StartedAt, *open[0].StartedAt)
	item.Summary = fmt.Sprintf("handed off, no ags report within %s", config.FormatDuration(m.ReportWithin(open[0].Command)))
	return item, true
}

func missedItem(history []*state.Run, since time.Time) (AttentionItem, bool) {
	var series []*state.Run
	for _, r := range history {
		if r.Status != state.StatusMissed {
			break
		}
		series = append(series, r)
	}
	if len(series) == 0 || missedLast(series[0]).Before(since) {
		return AttentionItem{}, false
	}
	item := jobItem(AttentionMissed, series[0])
	item.Count = 0
	for _, r := range series {
		item.Count += max(1, r.MissedCount)
	}
	item.span(series[len(series)-1].ScheduledFor, missedLast(series[0]))
	item.Summary = oneLine(series[0].Reason)
	return item, true
}

func happenedAt(r *state.Run) time.Time {
	switch {
	case r.SkippedLast != nil:
		return *r.SkippedLast
	case r.MissedLast != nil:
		return *r.MissedLast
	case r.StartedAt != nil:
		return *r.StartedAt
	}
	return r.ScheduledFor
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

func recentReports(runs []*state.Run, now time.Time) []FeedEntry {
	from := now.Add(-recentWindow)
	recent := []FeedEntry{}
	for _, r := range runs {
		if r.Report == nil || r.Report.Status != state.ReportDone || r.Report.At.Before(from) {
			continue
		}
		recent = append(recent, FeedEntry{At: r.Report.At, Run: r.ID, Project: r.Project, Job: r.Job, Source: FeedAgent, Status: r.Report.Status, Summary: oneLine(r.Report.Summary), Ref: r.Report.Ref})
	}
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].At.After(recent[j].At) })
	return recent
}

func AttentionLabel(kind string) string {
	switch kind {
	case AttentionPrecheckError:
		return "ERROR"
	case AttentionAgentFailed:
		return "agent failed"
	case AttentionNoReport:
		return "no report"
	case AttentionBlocked, AttentionUnapproved:
		return kind
	}
	return strings.ToUpper(kind)
}

func (i AttentionItem) Where() string {
	switch {
	case i.Key != "":
		return i.Key
	case i.Project != "":
		return i.Project
	}
	return "ags"
}

func (i AttentionItem) Headline(now time.Time) string {
	var parts []string
	switch {
	case i.Kind == AttentionPrecheckError || i.Kind == AttentionFailed:
		parts = append(parts, "failing since "+ago(now, *i.Since))
	case i.Kind == AttentionNoReport || i.Kind == AttentionMissed:
		parts = append(parts, "since "+ago(now, *i.Since))
	case i.Last != nil:
		parts = append(parts, ago(now, *i.Last)+" ago")
	}
	if i.Count > 1 {
		parts = append(parts, fmt.Sprintf("x%d", i.Count))
	}
	return strings.Join(parts, ", ")
}

func (i AttentionItem) Detail() string {
	text := i.Summary
	if i.Kind == AttentionConfig && i.File != "" {
		where := i.File
		if i.Line > 0 {
			where = fmt.Sprintf("%s:%d", i.File, i.Line)
		}
		text = where + ": " + text
	}
	return text
}

func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func attentionText(at Attention, now time.Time) string {
	var b strings.Builder
	if at.Daemon.Alive {
		fmt.Fprintf(&b, "daemon ok: %s\n", at.Daemon.Detail)
	}
	if len(at.Items) == 0 {
		b.WriteString("\nnothing waits for you\n")
	} else {
		fmt.Fprintf(&b, "\nATTENTION (%d)\n", len(at.Items))
	}
	for _, i := range at.Items {
		fmt.Fprintf(&b, "  %-32s %-12s %-24s %s\n", i.Where(), AttentionLabel(i.Kind), i.Headline(now), i.Detail())
		switch {
		case i.Ref != "":
			fmt.Fprintf(&b, "  %-32s → %s\n", "", i.Ref)
		case i.Kind == AttentionUnapproved:
			fmt.Fprintf(&b, "  %-32s → ags approve %s\n", "", i.Key)
		case i.Run != "":
			fmt.Fprintf(&b, "  %-32s → ags show %s\n", "", i.Run)
		}
	}
	if len(at.Recent) > 0 {
		b.WriteString("\nDONE (24h)\n")
		for _, e := range at.Recent {
			b.WriteString(FeedLine(e, 0, false))
		}
	}
	return b.String()
}
