package schedule

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

const maxScan = 100000

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

var allowedDescriptors = map[string]bool{
	"@yearly": true, "@annually": true, "@monthly": true, "@weekly": true,
	"@daily": true, "@midnight": true, "@hourly": true,
}

type Schedule struct {
	Expr     string
	Location *time.Location
	spec     cron.Schedule
}

func Parse(expr string, loc *time.Location) (*Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("empty schedule")
	}
	if strings.HasPrefix(expr, "@") && !allowedDescriptors[expr] {
		return nil, fmt.Errorf("unsupported schedule %q: use a 5-field cron or @hourly, @daily, @weekly, @monthly", expr)
	}
	if strings.HasPrefix(expr, "CRON_TZ=") || strings.HasPrefix(expr, "TZ=") {
		return nil, fmt.Errorf("set the timezone with the timezone key, not in the schedule")
	}
	if loc == nil {
		loc = time.Local
	}
	spec, err := parser.Parse("CRON_TZ=" + loc.String() + " " + expr)
	if err != nil {
		return nil, fmt.Errorf("invalid schedule %q: %v", expr, err)
	}
	return &Schedule{Expr: expr, Location: loc, spec: spec}, nil
}

func (s *Schedule) Next(t time.Time) time.Time {
	for i := 0; i < maxScan; i++ {
		t = s.spec.Next(t)
		if t.IsZero() || !s.isRepeatedWallClock(t) {
			return t
		}
	}
	return time.Time{}
}

func (s *Schedule) isRepeatedWallClock(t time.Time) bool {
	local := t.In(s.Location)
	for _, shift := range []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour} {
		earlier := t.Add(-shift).In(s.Location)
		if wallClock(earlier) == wallClock(local) {
			return true
		}
	}
	return false
}

func wallClock(t time.Time) string {
	return t.Format("2006-01-02T15:04")
}

func (s *Schedule) NextN(after time.Time, n int) []time.Time {
	var out []time.Time
	t := after
	for len(out) < n {
		t = s.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return out
}

type Due struct {
	Latest       time.Time
	First        time.Time
	BeforeLatest time.Time
	Earlier      int
}

func (s *Schedule) Due(after, until time.Time) (Due, bool) {
	var d Due
	t := after
	for i := 0; i < maxScan; i++ {
		t = s.Next(t)
		if t.IsZero() || t.After(until) {
			return d, !d.Latest.IsZero()
		}
		d.add(t)
	}
	for window := time.Hour; ; window *= 2 {
		start := until.Add(-window)
		if !start.After(d.Latest) {
			start = d.Latest
		}
		var tail []time.Time
		for t := s.Next(start); !t.IsZero() && !t.After(until); t = s.Next(t) {
			tail = append(tail, t)
		}
		if len(tail) > 1 || start.Equal(d.Latest) {
			for _, t := range tail {
				d.add(t)
			}
			return d, true
		}
	}
}

func (d *Due) add(t time.Time) {
	if d.Latest.IsZero() {
		d.First = t
	} else {
		d.Earlier++
		d.BeforeLatest = d.Latest
	}
	d.Latest = t
}
