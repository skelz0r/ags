package config

import (
	"fmt"
	"strings"
)

type Issue struct {
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

func (i Issue) String() string {
	if i.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", i.File, i.Line, i.Message)
	}
	return fmt.Sprintf("%s: %s", i.File, i.Message)
}

type Issues []Issue

func (is Issues) Error() string {
	lines := make([]string, len(is))
	for i, issue := range is {
		lines[i] = issue.String()
	}
	return strings.Join(lines, "\n")
}

func (is *Issues) add(file string, line int, format string, args ...any) {
	*is = append(*is, Issue{File: file, Line: line, Message: fmt.Sprintf(format, args...)})
}
