package service

import "sort"

type Daemon struct {
	Program []string
	Env     map[string]string
	LogPath string
}

func (d Daemon) envNames() []string {
	names := make([]string, 0, len(d.Env))
	for name := range d.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
