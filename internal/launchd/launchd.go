package launchd

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type Agent struct {
	Label   string
	Program []string
	Env     map[string]string
	LogPath string
}

func (a Agent) Plist() []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	key(&b, "Label")
	str(&b, a.Label)
	key(&b, "ProgramArguments")
	b.WriteString("  <array>\n")
	for _, arg := range a.Program {
		b.WriteString("  ")
		str(&b, arg)
	}
	b.WriteString("  </array>\n")
	key(&b, "EnvironmentVariables")
	b.WriteString("  <dict>\n")
	names := make([]string, 0, len(a.Env))
	for name := range a.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteString("  ")
		key(&b, name)
		b.WriteString("  ")
		str(&b, a.Env[name])
	}
	b.WriteString("  </dict>\n")
	key(&b, "RunAtLoad")
	b.WriteString("  <true/>\n")
	key(&b, "KeepAlive")
	b.WriteString("  <true/>\n")
	key(&b, "ProcessType")
	str(&b, "Background")
	key(&b, "StandardOutPath")
	str(&b, a.LogPath)
	key(&b, "StandardErrorPath")
	str(&b, a.LogPath)
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

func domain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func Loaded(label string) bool {
	return exec.Command("launchctl", "print", domain()+"/"+label).Run() == nil
}

func Bootstrap(label, plist string) error {
	exec.Command("launchctl", "bootout", domain()+"/"+label).Run()
	out, err := exec.Command("launchctl", "bootstrap", domain(), plist).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func Bootout(label string) error {
	if !Loaded(label) {
		return nil
	}
	out, err := exec.Command("launchctl", "bootout", domain()+"/"+label).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootout: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
