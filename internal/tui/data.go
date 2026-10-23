package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skelz0r/ags/internal/cli"
	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/state"
)

type client struct {
	base *cli.App
}

type callError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (c client) call(out any, args ...string) (json.RawMessage, error) {
	var stdout, stderr bytes.Buffer
	app := &cli.App{
		Getenv:   c.base.Getenv,
		Stdin:    strings.NewReader(""),
		Stdout:   &stdout,
		Stderr:   &stderr,
		Now:      c.base.Now,
		Notifier: c.base.Notifier,
		Exe:      c.base.Exe,
	}
	app.Run(append(args, "--json"))
	var env struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *callError      `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		return nil, fmt.Errorf("ags %s: %v", strings.Join(args, " "), err)
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		json.Unmarshal(env.Data, out)
	}
	if !env.OK {
		return env.Data, fmt.Errorf("%s", env.Error.Message)
	}
	return env.Data, nil
}

type project struct {
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	Jobs     []cli.JobView `json:"jobs"`
	Errors   config.Issues `json:"errors"`
	Warnings config.Issues `json:"warnings"`
}

type status struct {
	alive  bool
	detail string
	errors []string
}

func (c client) projects() ([]project, error) {
	var ps []project
	_, err := c.call(&ps, "projects")
	return ps, err
}

func (c client) runs(filters ...string) ([]cli.RunView, error) {
	var rs []cli.RunView
	_, err := c.call(&rs, append([]string{"runs", "--limit", "200"}, filters...)...)
	return rs, err
}

func (c client) detail(id string) (string, error) {
	var d struct {
		cli.RunView
		Prompt      string `json:"prompt"`
		PrecheckLog string `json:"precheck_log"`
		Stdout      string `json:"stdout"`
		Stderr      string `json:"stderr"`
	}
	if _, err := c.call(&d, "show", id); err != nil {
		return "", err
	}
	return cli.RunDetailText(d.RunView, d.Prompt, d.PrecheckLog, d.Stdout, d.Stderr), nil
}

type pendingApproval struct {
	Job         string `json:"job"`
	First       bool   `json:"first"`
	Diff        string `json:"diff"`
	Fingerprint string `json:"fingerprint"`
}

func (c client) pending(target string) ([]pendingApproval, error) {
	var out struct {
		Pending []pendingApproval `json:"pending"`
	}
	data, err := c.call(&out, "approve", target)
	if err != nil && data == nil {
		return nil, err
	}
	return out.Pending, nil
}

func (c client) daemonStatus() status {
	store, err := state.Open(pathsOf(c.base))
	if err != nil {
		return status{detail: err.Error()}
	}
	st, err := store.Read()
	if err != nil {
		return status{detail: err.Error()}
	}
	alive, detail := cli.DaemonAlive(st, c.base.Now())
	s := status{alive: alive, detail: detail}
	for key, e := range st.ConfigErrors {
		s.errors = append(s.errors, key+": "+strings.SplitN(e.Message, "\n", 2)[0])
	}
	return s
}
