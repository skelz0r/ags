package config

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultCommandTimeout  = 5 * time.Minute
	DefaultPrecheckTimeout = 60 * time.Second
	StdinPrompt            = "prompt"
	StdinNone              = "none"
)

type Command struct {
	Name      string
	Argv      []string
	Stdin     string
	PromptArg bool
	Cwd       string
	Env       map[string]string
	Timeout   time.Duration
	Line      int
}

type Notifications struct {
	On       []string
	Channels []string
}

type Machine struct {
	Path            string
	Exists          bool
	DefaultCommand  string
	Preprompt       string
	PrecheckTimeout time.Duration
	Commands        map[string]*Command
	Notifications   Notifications
}

func (m *Machine) Notifies(event string) bool {
	if len(m.Notifications.Channels) == 0 {
		return false
	}
	for _, on := range m.Notifications.On {
		if on == event {
			return true
		}
	}
	return false
}

var defaultNotifications = Notifications{
	On:       []string{"failed", "missed", "precheck_error", "invalid", "unapproved"},
	Channels: []string{"desktop"},
}

func LoadMachine(path string) (*Machine, Issues) {
	m := &Machine{
		Path:            path,
		PrecheckTimeout: DefaultPrecheckTimeout,
		Commands:        map[string]*Command{},
		Notifications:   defaultNotifications,
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	var issues Issues
	if err != nil {
		issues.add(path, 0, "%v", err)
		return m, issues
	}
	m.Exists = true
	root, parseIssues := parseDocument(path, data)
	if parseIssues != nil {
		return m, parseIssues
	}
	for _, p := range pairsOf(root) {
		line := p.key.Line
		switch p.key.Value {
		case "default_command":
			m.DefaultCommand, err = scalarString(p.value)
		case "preprompt":
			m.Preprompt, err = scalarString(p.value)
		case "precheck_timeout":
			var s string
			if s, err = scalarString(p.value); err == nil {
				m.PrecheckTimeout, err = ParseDuration(s)
			}
		case "commands":
			issues = append(issues, m.loadCommands(p.value)...)
		case "notifications":
			issues = append(issues, m.loadNotifications(p.value)...)
		default:
			issues.add(path, line, "unknown key %q", p.key.Value)
		}
		if err != nil {
			issues.add(path, line, "%s: %v", p.key.Value, err)
			err = nil
		}
	}
	if m.DefaultCommand != "" && m.Commands[m.DefaultCommand] == nil {
		issues.add(path, lookup(root, "default_command").Line, "default_command %q is not defined under commands", m.DefaultCommand)
	}
	return m, issues
}

func (m *Machine) loadCommands(node *yaml.Node) Issues {
	var issues Issues
	if node.Kind != yaml.MappingNode {
		issues.add(m.Path, node.Line, "commands: expected a mapping")
		return issues
	}
	for _, p := range pairsOf(node) {
		c := &Command{Name: p.key.Value, Stdin: StdinPrompt, Timeout: DefaultCommandTimeout, Line: p.key.Line, Env: map[string]string{}}
		if p.value.Kind != yaml.MappingNode {
			issues.add(m.Path, p.key.Line, "command %q: expected a mapping", c.Name)
			continue
		}
		stdinSet := false
		before := len(issues)
		for _, f := range pairsOf(p.value) {
			var err error
			switch f.key.Value {
			case "argv":
				c.Argv, err = stringList(f.value)
			case "stdin":
				c.Stdin, err = scalarString(f.value)
				stdinSet = true
				if err == nil && c.Stdin != StdinPrompt && c.Stdin != StdinNone {
					err = errorf("expected prompt or none")
				}
			case "prompt_arg":
				c.PromptArg, err = scalarBool(f.value)
			case "cwd":
				c.Cwd, err = scalarString(f.value)
			case "env":
				c.Env, err = stringMap(f.value)
			case "timeout":
				var s string
				if s, err = scalarString(f.value); err == nil {
					c.Timeout, err = ParseDuration(s)
				}
			default:
				issues.add(m.Path, f.key.Line, "command %q: unknown key %q", c.Name, f.key.Value)
			}
			if err != nil {
				issues.add(m.Path, f.key.Line, "command %q: %s: %v", c.Name, f.key.Value, err)
			}
		}
		if len(c.Argv) == 0 {
			issues.add(m.Path, c.Line, "command %q: argv is required", c.Name)
		}
		if c.PromptArg {
			if !stdinSet {
				c.Stdin = StdinNone
			}
			if !strings.Contains(strings.Join(c.Argv, "\x00"), "{prompt}") {
				issues.add(m.Path, c.Line, "command %q: prompt_arg needs {prompt} in argv", c.Name)
			}
		}
		if len(issues) == before {
			m.Commands[c.Name] = c
		}
	}
	return issues
}

func (m *Machine) loadNotifications(node *yaml.Node) Issues {
	var issues Issues
	if node.Kind != yaml.MappingNode {
		issues.add(m.Path, node.Line, "notifications: expected a mapping")
		return issues
	}
	for _, p := range pairsOf(node) {
		var err error
		switch p.key.Value {
		case "on":
			m.Notifications.On, err = stringList(p.value)
		case "channels":
			m.Notifications.Channels, err = stringList(p.value)
			for _, ch := range m.Notifications.Channels {
				if err == nil && ch != "desktop" && ch != "macos" {
					err = errorf("unknown channel %q, only desktop is supported", ch)
				}
			}
		default:
			issues.add(m.Path, p.key.Line, "notifications: unknown key %q", p.key.Value)
		}
		if err != nil {
			issues.add(m.Path, p.key.Line, "notifications.%s: %v", p.key.Value, err)
		}
	}
	return issues
}
