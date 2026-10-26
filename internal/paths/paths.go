package paths

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/skelz0r/ags/internal/service"
)

type Paths struct {
	Home         string
	Isolated     bool
	ConfigDir    string
	StateDir     string
	ServiceDir   string
	ClaudeSkills string
}

func Resolve(getenv func(string) string) Paths {
	if root := getenv("AGS_HOME"); root != "" {
		root = ExpandHome(root, getenv("HOME"))
		return Paths{
			Home:         getenv("HOME"),
			Isolated:     true,
			ConfigDir:    filepath.Join(root, "config"),
			StateDir:     filepath.Join(root, "state"),
			ServiceDir:   filepath.Join(root, service.IsolatedDir),
			ClaudeSkills: filepath.Join(root, "claude-skills"),
		}
	}
	home := getenv("HOME")
	configHome := xdg(getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config"))
	stateHome := xdg(getenv("XDG_STATE_HOME"), filepath.Join(home, ".local", "state"))
	return Paths{
		Home:         home,
		ConfigDir:    filepath.Join(configHome, "ags"),
		StateDir:     filepath.Join(stateHome, "ags"),
		ServiceDir:   service.Dir(home, configHome),
		ClaudeSkills: filepath.Join(home, ".claude", "skills"),
	}
}

func xdg(value, fallback string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return fallback
}

func FromEnv() Paths {
	return Resolve(os.Getenv)
}

func (p Paths) MachineConfig() string { return filepath.Join(p.ConfigDir, "config.yml") }
func (p Paths) Registry() string      { return filepath.Join(p.ConfigDir, "projects.yml") }
func (p Paths) StateFile() string     { return filepath.Join(p.StateDir, "state.json") }
func (p Paths) StateLock() string     { return filepath.Join(p.StateDir, "state.lock") }
func (p Paths) RunsDir() string       { return filepath.Join(p.StateDir, "runs") }
func (p Paths) History() string       { return filepath.Join(p.StateDir, "history.jsonl") }
func (p Paths) DaemonLog() string     { return filepath.Join(p.StateDir, "daemon.log") }
func (p Paths) DaemonLock() string    { return filepath.Join(p.StateDir, "daemon.lock") }
func (p Paths) SkillsCopy() string    { return filepath.Join(p.StateDir, "skills") }
func (p Paths) ServiceFile() string   { return filepath.Join(p.ServiceDir, service.File) }

func ExpandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func Abbreviate(path, home string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+"/")) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
