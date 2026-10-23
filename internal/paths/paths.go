package paths

import (
	"os"
	"path/filepath"
	"strings"
)

const Label = "dev.ags"

type Paths struct {
	Home         string
	Isolated     bool
	ConfigDir    string
	StateDir     string
	LaunchAgents string
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
			LaunchAgents: filepath.Join(root, "LaunchAgents"),
			ClaudeSkills: filepath.Join(root, "claude-skills"),
		}
	}
	home := getenv("HOME")
	return Paths{
		Home:         home,
		ConfigDir:    filepath.Join(home, ".config", "ags"),
		StateDir:     filepath.Join(home, ".local", "state", "ags"),
		LaunchAgents: filepath.Join(home, "Library", "LaunchAgents"),
		ClaudeSkills: filepath.Join(home, ".claude", "skills"),
	}
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
func (p Paths) Plist() string         { return filepath.Join(p.LaunchAgents, Label+".plist") }

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
