package diff

import (
	"fmt"
	"strings"
)

type line struct {
	prefix string
	text   string
}

func Lines(before, after string) string {
	var out strings.Builder
	for _, l := range compare(before, after) {
		out.WriteString(l.prefix + l.text + "\n")
	}
	return out.String()
}

func Hunks(before, after string, context int) string {
	lines := compare(before, after)
	keep := make([]bool, len(lines))
	for i, l := range lines {
		if l.prefix == unchanged {
			continue
		}
		for k := max(0, i-context); k <= min(len(lines)-1, i+context); k++ {
			keep[k] = true
		}
	}
	var out strings.Builder
	oldLine, newLine := 1, 1
	for i := 0; i < len(lines); {
		if !keep[i] {
			oldLine, newLine = advance(lines[i], oldLine, newLine)
			i++
			continue
		}
		end := i
		for end < len(lines) && keep[end] {
			end++
		}
		oldCount, newCount := 0, 0
		for _, l := range lines[i:end] {
			oldCount, newCount = advance(l, oldCount, newCount)
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", oldLine, oldCount, newLine, newCount)
		for _, l := range lines[i:end] {
			out.WriteString(l.prefix + l.text + "\n")
			oldLine, newLine = advance(l, oldLine, newLine)
		}
		i = end
	}
	return out.String()
}

const (
	unchanged = "  "
	added     = "+ "
	removed   = "- "
)

func advance(l line, oldLine, newLine int) (int, int) {
	switch l.prefix {
	case added:
		return oldLine, newLine + 1
	case removed:
		return oldLine + 1, newLine
	}
	return oldLine + 1, newLine + 1
}

func compare(before, after string) []line {
	a := split(before)
	b := split(after)
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []line
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			out = append(out, line{unchanged, a[i]})
			i++
			j++
		case j < len(b) && (i == len(a) || lcs[i][j+1] > lcs[i+1][j]):
			out = append(out, line{added, b[j]})
			j++
		default:
			out = append(out, line{removed, a[i]})
			i++
		}
	}
	return out
}

func split(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func Style(lines string, added, removed func(string) string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(lines, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "+ "):
			l = added(l)
		case strings.HasPrefix(l, "- "):
			l = removed(l)
		}
		out = append(out, l)
	}
	return out
}
