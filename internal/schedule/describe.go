package schedule

import (
	"fmt"
	"strconv"
	"strings"
)

var weekdays = []string{"Sundays", "Mondays", "Tuesdays", "Wednesdays", "Thursdays", "Fridays", "Saturdays"}
var months = []string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

func Describe(expr string) string {
	switch expr {
	case "@hourly":
		return "every hour"
	case "@daily", "@midnight":
		return "every day 00:00"
	case "@weekly":
		return "Sundays 00:00"
	case "@monthly":
		return "1st of the month 00:00"
	case "@yearly", "@annually":
		return "January 1st 00:00"
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return expr
	}
	minute, hour, dom, month, dow := fields[0], fields[1], fields[2], fields[3], fields[4]
	m, errM := strconv.Atoi(minute)
	if errM != nil {
		return expr
	}
	if hour == "*" && dom == "*" && month == "*" && dow == "*" {
		return fmt.Sprintf("every hour at :%02d", m)
	}
	h, errH := strconv.Atoi(hour)
	if errH != nil {
		return expr
	}
	at := fmt.Sprintf("%02d:%02d", h, m)
	days, ok := describeDays(dom, month, dow)
	if !ok {
		return expr
	}
	return days + " " + at
}

func describeDays(dom, month, dow string) (string, bool) {
	monthPart := ""
	if month != "*" {
		n, err := strconv.Atoi(month)
		if err != nil || n < 1 || n > 12 {
			return "", false
		}
		monthPart = " of " + months[n]
	}
	switch {
	case dom == "*" && dow == "*":
		if monthPart != "" {
			return "every day" + monthPart, true
		}
		return "every day", true
	case dom == "*":
		names, ok := listOf(dow, func(n int) (string, bool) {
			if n == 7 {
				n = 0
			}
			if n < 0 || n > 6 {
				return "", false
			}
			return weekdays[n], true
		})
		if dow == "1-5" {
			names, ok = "weekdays", true
		}
		return names + monthPart, ok
	case dow == "*":
		names, ok := listOf(dom, func(n int) (string, bool) {
			if n < 1 || n > 31 {
				return "", false
			}
			return ordinal(n), true
		})
		if monthPart == "" {
			monthPart = " of the month"
		}
		return names + monthPart, ok
	}
	return "", false
}

func listOf(field string, name func(int) (string, bool)) (string, bool) {
	var names []string
	for _, part := range strings.Split(field, ",") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return "", false
		}
		s, ok := name(n)
		if !ok {
			return "", false
		}
		names = append(names, s)
	}
	switch len(names) {
	case 1:
		return names[0], true
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1], true
	}
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}
