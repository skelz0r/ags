package service

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	Kind        = "systemd user unit"
	Name        = "ags.service"
	File        = Name
	IsolatedDir = "systemd"
)

func Dir(home, configHome string) string {
	return filepath.Join(configHome, "systemd", "user")
}

func (d Daemon) Definition() []byte {
	var b bytes.Buffer
	b.WriteString("[Unit]\nDescription=ags, scheduler of coding agents\n\n[Service]\nType=simple\n")
	args := make([]string, len(d.Program))
	for i, arg := range d.Program {
		args[i] = `"` + execQuoted.Replace(arg) + `"`
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(args, " "))
	for _, name := range d.envNames() {
		fmt.Fprintf(&b, "Environment=\"%s\"\n", envQuoted.Replace(name+"="+d.Env[name]))
	}
	fmt.Fprintf(&b, "StandardOutput=append:%s\n", specifiers.Replace(d.LogPath))
	fmt.Fprintf(&b, "StandardError=append:%s\n", specifiers.Replace(d.LogPath))
	b.WriteString("Restart=always\nRestartSec=10\n\n[Install]\nWantedBy=default.target\n")
	return b.Bytes()
}

var specifiers = strings.NewReplacer("%", "%%")

var envQuoted = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "\n", `\n`)

var execQuoted = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$", "\n", `\n`)

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func Loaded() bool {
	return systemctl("is-active", "--quiet", Name) == nil
}

func Load(path string) error {
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := systemctl("enable", path); err != nil {
		return err
	}
	return systemctl("restart", Name)
}

func Unload() error {
	var stderr bytes.Buffer
	show := exec.Command("systemctl", "--user", "show", Name, "--property=ActiveState,UnitFileState")
	show.Stderr = &stderr
	out, err := show.Output()
	if err != nil {
		return fmt.Errorf("systemctl --user show %s: %v: %s", Name, err, strings.TrimSpace(stderr.String()))
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			props[key] = value
		}
	}
	if state := props["ActiveState"]; state != "inactive" && state != "failed" {
		if err := systemctl("stop", Name); err != nil {
			return err
		}
	}
	if state := props["UnitFileState"]; state != "" && state != "bad" {
		return systemctl("disable", Name)
	}
	return nil
}
