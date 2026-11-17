package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skelz0r/ags/internal/config"
)

const HerdrPluginID = "dev.ags"

const herdrManifest = `id = "` + HerdrPluginID + `"
name = "ags"
version = "0.1.0"
min_herdr_version = "0.9.3"
description = "Open the ags board (what waits for you, projects, jobs, runs) in a popup"
platforms = ["linux", "macos"]

[[actions]]
id = "board"
title = "ags board"
description = "What waits for you in ags: failing prechecks, blocked reports, jobs to approve"
contexts = ["global"]
command = ["sh", "bin/open-board"]

[[panes]]
id = "board"
title = "ags"
placement = "popup"
width = "100%"
height = "100%"
command = ["sh", "bin/board"]
`

const herdrOpenBoard = `#!/bin/sh
exec "${HERDR_BIN_PATH:-herdr}" plugin pane open --plugin "$HERDR_PLUGIN_ID" --entrypoint board
`

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (a *App) herdrBoard(exe string) string {
	env := a.daemonService(exe).Env
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("#!/bin/sh\nexec env")
	for _, k := range keys {
		b.WriteString(" " + k + "=" + shellQuote(env[k]))
	}
	b.WriteString(" " + shellQuote(exe) + "\n")
	return b.String()
}

func (a *App) writeHerdrPlugin(exe string) error {
	dir := a.paths.HerdrPlugin()
	files := map[string]string{
		"herdr-plugin.toml": herdrManifest,
		"bin/open-board":    herdrOpenBoard,
		"bin/board":         a.herdrBoard(exe),
	}
	for name, content := range files {
		if err := config.WriteFileAtomic(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

type herdrPlugin struct {
	Dir     string `json:"dir"`
	Linked  bool   `json:"linked"`
	Warning string `json:"warning,omitempty"`
}

func herdrLinkedDir(herdr string) (string, bool, error) {
	out, err := exec.Command(herdr, "plugin", "list").Output()
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "- "+HerdrPluginID+" ") {
			continue
		}
		dir := ""
		if i := strings.Index(line, "[local:"); i >= 0 {
			dir = strings.TrimSuffix(strings.TrimSpace(line[i+len("[local:"):]), "]")
		}
		return dir, true, nil
	}
	return "", false, nil
}

func herdrRun(herdr string, args ...string) error {
	if out, err := exec.Command(herdr, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("herdr %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

func (a *App) installHerdrPlugin(exe string, b *strings.Builder) (herdrPlugin, error) {
	dir := a.paths.HerdrPlugin()
	plugin := herdrPlugin{Dir: dir}
	if err := a.writeHerdrPlugin(exe); err != nil {
		return plugin, err
	}
	herdr, err := exec.LookPath("herdr")
	switch {
	case a.paths.Isolated:
		fmt.Fprintf(b, "wrote the herdr plugin %s, not linked (AGS_HOME set)\n", dir)
		return plugin, nil
	case err != nil:
		plugin.Warning = "herdr not found in PATH, link it with: herdr plugin link " + dir
		fmt.Fprintf(b, "wrote the herdr plugin %s; %s\n", dir, plugin.Warning)
		return plugin, nil
	}
	linked, found, err := herdrLinkedDir(herdr)
	switch {
	case err != nil:
		plugin.Warning = "herdr plugin list failed: " + err.Error()
	case found && linked == dir:
		plugin.Linked = true
		fmt.Fprintf(b, "updated the herdr plugin %s\n", dir)
		return plugin, nil
	case found:
		if err := herdrRun(herdr, "plugin", "unlink", HerdrPluginID); err != nil {
			plugin.Warning = err.Error()
		}
	}
	if plugin.Warning == "" {
		if err := herdrRun(herdr, "plugin", "link", dir); err != nil {
			plugin.Warning = err.Error()
		} else {
			plugin.Linked = true
			fmt.Fprintf(b, "linked the herdr plugin %s (action: ags board)\n", dir)
			return plugin, nil
		}
	}
	fmt.Fprintf(b, "wrote the herdr plugin %s, not linked: %s\n", dir, plugin.Warning)
	return plugin, nil
}

func (a *App) uninstallHerdrPlugin(b *strings.Builder) herdrPlugin {
	dir := a.paths.HerdrPlugin()
	plugin := herdrPlugin{Dir: dir}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return plugin
	}
	herdr, lookErr := exec.LookPath("herdr")
	if lookErr != nil && !a.paths.Isolated {
		plugin.Warning = "herdr not found in PATH: if " + HerdrPluginID + " is still linked, run herdr plugin unlink " + HerdrPluginID
		fmt.Fprintf(b, "%s\n", plugin.Warning)
	}
	if lookErr == nil && !a.paths.Isolated {
		_, found, err := herdrLinkedDir(herdr)
		if err == nil && found {
			err = herdrRun(herdr, "plugin", "unlink", HerdrPluginID)
			if err == nil {
				fmt.Fprintf(b, "unlinked the herdr plugin %s\n", HerdrPluginID)
			}
		}
		if err != nil {
			plugin.Linked = found
			plugin.Warning = err.Error() + ", " + dir + " kept"
			fmt.Fprintf(b, "%s\n", plugin.Warning)
			return plugin
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		plugin.Warning = strings.TrimPrefix(plugin.Warning+"; "+err.Error(), "; ")
		fmt.Fprintf(b, "%s\n", err)
	} else {
		fmt.Fprintf(b, "removed %s\n", dir)
	}
	return plugin
}
