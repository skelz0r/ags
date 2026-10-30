package cli

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/paths"
	"github.com/skelz0r/ags/internal/state"
)

const (
	completeCommand = "__complete"
	completeFiles   = ":files"
	recentRuns      = 30
)

//go:embed completion
var completionScripts embed.FS

type usage struct {
	flags  map[string]bool
	maxPos int
}

func (f *flags) usage(maxPos int) *usage {
	u := &usage{flags: map[string]bool{}, maxPos: maxPos}
	f.VisitAll(func(fl *flag.Flag) {
		boolean, _ := fl.Value.(interface{ IsBoolFlag() bool })
		u.flags[fl.Name] = boolean == nil || !boolean.IsBoolFlag()
	})
	return u
}

func (a *App) completion(args []string) (result, error) {
	f := newFlags("completion")
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	shell := f.positional[0]
	script, err := completionScripts.ReadFile("completion/ags." + shell)
	if err != nil {
		return result{}, usageError("completion: unknown shell %q, expected zsh", shell)
	}
	return result{data: string(script), text: string(script)}, nil
}

type candidate struct {
	value, description string
}

func (a *App) complete(words []string) int {
	if len(words) == 0 {
		words = []string{""}
	}
	for _, c := range a.candidates(words[:len(words)-1], words[len(words)-1]) {
		fmt.Fprintf(a.Stdout, "%s\t%s\n", oneLine(c.value), oneLine(c.description))
	}
	return ExitOK
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (a *App) candidates(before []string, current string) []candidate {
	var args []string
	for _, w := range before {
		if w != "--json" {
			args = append(args, w)
		}
	}
	if len(args) == 0 {
		if strings.HasPrefix(current, "-") {
			return []candidate{{"--json", "print a JSON envelope"}, {"--help", "list the commands"}}
		}
		out := []candidate{{"help", "list the commands"}}
		for _, name := range commandOrder {
			out = append(out, candidate{name, commands[name].summary})
		}
		return out
	}
	cmd, ok := commands[args[0]]
	if !ok {
		return nil
	}
	u := probeUsage(a, cmd)
	if u == nil {
		return nil
	}
	positional, pending, literal := 0, "", false
	for _, w := range args[1:] {
		switch {
		case pending != "":
			pending = ""
		case literal || w == "-" || !strings.HasPrefix(w, "-"):
			literal = false
			positional++
		case w == "--":
			literal = true
		default:
			name, _, inline := strings.Cut(strings.TrimLeft(w, "-"), "=")
			if u.flags[name] && !inline {
				pending = name
			}
		}
	}
	if pending != "" {
		return a.flagValues(args[0], pending)
	}
	if !literal && strings.HasPrefix(current, "-") {
		if flag, _, inline := strings.Cut(current, "="); inline {
			if !u.flags[strings.TrimLeft(flag, "-")] {
				return nil
			}
			out := a.flagValues(args[0], strings.TrimLeft(flag, "-"))
			for i := range out {
				if out[i].value != completeFiles {
					out[i].value = flag + "=" + out[i].value
				}
			}
			return out
		}
		out := []candidate{{"--json", ""}}
		for _, name := range slices.Sorted(maps.Keys(u.flags)) {
			out = append(out, candidate{flagSpelling(name), ""})
		}
		return out
	}
	if positional >= u.maxPos {
		return nil
	}
	if args[0] == "new" {
		return a.projectPrefixes()
	}
	return a.positionalValues(cmd.summary, positional)
}

func probeUsage(a *App, cmd command) *usage {
	_, err := cmd.run(a, []string{"-h"})
	var e *Error
	if !errors.As(err, &e) {
		return nil
	}
	return e.usage
}

func flagSpelling(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

func (a *App) flagValues(command, name string) []candidate {
	switch name {
	case "project":
		return a.projectCandidates()
	case "job":
		return a.jobCandidates()
	case "status":
		var out []candidate
		if command == "report" {
			for _, s := range state.ReportStatuses {
				out = append(out, candidate{s, ""})
			}
			return out
		}
		for _, s := range []string{state.StatusRunning, state.StatusSucceeded, state.StatusFailed, state.StatusSkipped, state.StatusPrecheckError, state.StatusMissed} {
			out = append(out, candidate{s, ""})
		}
		return out
	case "prompt-file":
		return []candidate{{completeFiles, ""}}
	}
	return nil
}

func (a *App) positionalValues(summary string, index int) []candidate {
	var placeholders []string
	for _, field := range strings.Fields(summary)[1:] {
		if strings.HasPrefix(field, "[-") || strings.HasPrefix(field, "--") {
			break
		}
		placeholders = append(placeholders, field)
	}
	if index >= len(placeholders) {
		return nil
	}
	placeholder := strings.Trim(placeholders[index], "<>[]")
	switch placeholder {
	case "project":
		return a.projectCandidates()
	case "project/job":
		return a.jobCandidates()
	case "project[/job":
		return append(a.projectCandidates(), a.jobCandidates()...)
	case "run":
		return a.runCandidates()
	case "path":
		return []candidate{{completeFiles, ""}}
	}
	if placeholder == placeholders[index] {
		return []candidate{{placeholder, ""}}
	}
	return nil
}

func (a *App) projectCandidates() []candidate {
	reg, err := a.registry()
	if err != nil {
		return nil
	}
	var out []candidate
	for _, e := range reg.Projects {
		out = append(out, candidate{e.Name, paths.Abbreviate(e.Path, a.Getenv("HOME"))})
	}
	return out
}

func (a *App) projectPrefixes() []candidate {
	out := a.projectCandidates()
	for i := range out {
		out[i].value += "/"
	}
	return out
}

func (a *App) jobCandidates() []candidate {
	reg, err := a.registry()
	if err != nil {
		return nil
	}
	m, _ := a.loadMachine()
	var out []candidate
	for _, e := range reg.Projects {
		p, _ := config.Loader{Machine: m, SkipGit: true}.Load(e.Name, e.Path)
		if p == nil {
			continue
		}
		for _, j := range p.Jobs {
			out = append(out, candidate{j.Project + "/" + j.Name, j.ScheduleExpr})
		}
	}
	return out
}

func (a *App) runCandidates() []candidate {
	store := &state.Store{Paths: a.paths}
	runs, err := store.ListRuns(state.Filter{Limit: recentRuns})
	if err != nil {
		return nil
	}
	var out []candidate
	for _, r := range runs {
		out = append(out, candidate{r.ID, fmt.Sprintf("%s %s %s", r.Key(), StatusLabel(r.Status, r.Reason), r.ScheduledFor.Local().Format("2006-01-02 15:04"))})
	}
	return out
}
