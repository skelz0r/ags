package daemon

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/state"
)

func signature(files []string) string {
	var b strings.Builder
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", f, info.ModTime().UnixNano(), info.Size())
		} else {
			fmt.Fprintf(&b, "%s:-;", f)
		}
	}
	return b.String()
}

type loadError struct {
	key     string
	message string
}

func (d *Daemon) reload() {
	d.pendingErrors = nil
	machineChanged := false
	if sig := signature([]string{d.opts.Paths.MachineConfig()}); sig != d.machineSig || d.machine == nil {
		d.machineSig = sig
		m, issues := config.LoadMachine(d.opts.Paths.MachineConfig())
		switch {
		case len(issues) == 0, d.machine == nil:
			d.machine = m
			machineChanged = true
		}
		d.machineError = ""
		if len(issues) > 0 {
			d.machineError = issues.Error()
		}
	}
	if d.machineError != "" {
		d.pendingErrors = append(d.pendingErrors, loadError{machineKey, d.machineError})
	}

	reg, err := config.LoadRegistry(d.opts.Paths.Registry())
	if err != nil {
		d.pendingErrors = append(d.pendingErrors, loadError{"@registry", err.Error()})
		return
	}
	seen := map[string]bool{}
	for _, entry := range reg.Projects {
		seen[entry.Name] = true
		lp := d.projects[entry.Name]
		if lp == nil || lp.entry.Path != entry.Path {
			lp = &loadedProject{entry: entry}
			d.projects[entry.Name] = lp
		}
		files := append([]string{entry.Path + "/" + config.SharedFileName, entry.Path + "/" + config.LocalFileName}, lp.sources...)
		sig := signature(files)
		if sig == lp.signature && !machineChanged && lp.err == "" {
			continue
		}
		loader := config.Loader{Machine: d.machine, SkipGit: true}
		p, issues := loader.Load(entry.Name, entry.Path)
		if len(issues) > 0 {
			lp.err = issues.Error()
			d.pendingErrors = append(d.pendingErrors, loadError{entry.Name, lp.err})
		} else {
			lp.err = ""
			lp.project = p
			lp.machine = d.machine
			lp.sources = p.Sources
		}
		lp.signature = signature(append([]string{entry.Path + "/" + config.SharedFileName, entry.Path + "/" + config.LocalFileName}, lp.sources...))
	}
	for name := range d.projects {
		if !seen[name] {
			delete(d.projects, name)
		}
	}
}

func (d *Daemon) recordConfigErrors(st *state.State, now time.Time, notes *[]note) {
	current := map[string]bool{}
	for _, e := range d.pendingErrors {
		current[e.key] = true
		if st.ConfigErrors[e.key].Message == e.message {
			continue
		}
		st.ConfigErrors[e.key] = state.ConfigError{Message: e.message, Since: now}
		d.logf("config error in %s: %s", e.key, e.message)
		*notes = append(*notes, note{"invalid", "ags: invalid config for " + e.key, firstLine(e.message)})
	}
	for key := range st.ConfigErrors {
		if !current[key] {
			delete(st.ConfigErrors, key)
			d.logf("config of %s fixed", key)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
