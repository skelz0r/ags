package notify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	NavetteProgram = "navette"
	NavetteType    = "ags"
	navetteTimeout = 20 * time.Second
)

var paneVariables = []string{"CLAUDE_CODE_BRIDGE_SESSION_ID", "HERDR_PANE_ID"}

type NavetteItem struct {
	Key         string
	Dir         string
	Title       string
	Status      string
	Description string
}

type Navette struct {
	Getenv func(string) string
}

func (n Navette) Program() (string, error) {
	for _, dir := range filepath.SplitList(n.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, NavetteProgram)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH", NavetteProgram)
}

func (n Navette) Post(ctx context.Context, item NavetteItem) error {
	program, err := n.Program()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, navetteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "post",
		"--key", item.Key, "--type", NavetteType, "--title", item.Title,
		"--status", item.Status, "--description", item.Description)
	if info, err := os.Stat(item.Dir); err == nil && info.IsDir() {
		cmd.Dir = item.Dir
	}
	cmd.Env = n.env()
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("navette post timed out after %s", navetteTimeout)
	}
	if err != nil {
		return fmt.Errorf("navette post: %v: %s", err, firstLine(string(out)))
	}
	return nil
}

const utf8Locale = "en_US.UTF-8"

func (n Navette) env() []string {
	var env []string
	utf8 := unicodeLocale()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "PATH" || name == "HOME" || inherited(name) || (!utf8 && name == "LC_ALL") {
			continue
		}
		env = append(env, kv)
	}
	if !utf8 {
		env = append(env, "LC_ALL="+utf8Locale)
	}
	return append(env, "PATH="+n.Getenv("PATH"), "HOME="+n.Getenv("HOME"))
}

func unicodeLocale() bool {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if value := os.Getenv(name); value != "" {
			value = strings.ToUpper(value)
			return strings.Contains(value, "UTF-8") || strings.Contains(value, "UTF8")
		}
	}
	return false
}

func inherited(name string) bool {
	for _, pane := range paneVariables {
		if name == pane {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
