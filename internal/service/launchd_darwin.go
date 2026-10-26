package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	Kind        = "LaunchAgent"
	Name        = "dev.ags"
	File        = Name + ".plist"
	IsolatedDir = "LaunchAgents"
)

func Dir(home, configHome string) string {
	return filepath.Join(home, "Library", "LaunchAgents")
}

func (d Daemon) Definition() []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	key(&b, "Label")
	str(&b, Name)
	key(&b, "ProgramArguments")
	b.WriteString("  <array>\n")
	for _, arg := range d.Program {
		b.WriteString("  ")
		str(&b, arg)
	}
	b.WriteString("  </array>\n")
	key(&b, "EnvironmentVariables")
	b.WriteString("  <dict>\n")
	for _, name := range d.envNames() {
		b.WriteString("  ")
		key(&b, name)
		b.WriteString("  ")
		str(&b, d.Env[name])
	}
	b.WriteString("  </dict>\n")
	key(&b, "RunAtLoad")
	b.WriteString("  <true/>\n")
	key(&b, "KeepAlive")
	b.WriteString("  <true/>\n")
	key(&b, "ProcessType")
	str(&b, "Background")
	key(&b, "StandardOutPath")
	str(&b, d.LogPath)
	key(&b, "StandardErrorPath")
	str(&b, d.LogPath)
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

func key(b *bytes.Buffer, s string) {
	b.WriteString("  <key>")
	xml.EscapeText(b, []byte(s))
	b.WriteString("</key>\n")
}

func str(b *bytes.Buffer, s string) {
	b.WriteString("  <string>")
	xml.EscapeText(b, []byte(s))
	b.WriteString("</string>\n")
}

func target() string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), Name)
}

func Loaded() bool {
	return exec.Command("launchctl", "print", target()).Run() == nil
}

func Load(path string) error {
	exec.Command("launchctl", "bootout", target()).Run()
	out, err := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func Unload() error {
	if !Loaded() {
		return nil
	}
	out, err := exec.Command("launchctl", "bootout", target()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootout: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
