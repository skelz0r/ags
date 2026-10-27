package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/state"
)

const killGrace = 5 * time.Second

type Notifier interface {
	Notify(event, title, message string)
}

type Runner struct {
	Store    *state.Store
	Machine  *config.Machine
	Notifier Notifier
	Now      func() time.Time
}

type Request struct {
	Project      *config.Project
	Job          *config.Job
	Run          *state.Run
	SkipPrecheck bool
}

func NewRun(p *config.Project, j *config.Job, trigger string, scheduledFor, decidedAt time.Time) *state.Run {
	return &state.Run{
		ID:            state.NewRunID(decidedAt),
		Project:       p.Name,
		Job:           j.Name,
		Trigger:       trigger,
		Status:        state.StatusRunning,
		ScheduledFor:  scheduledFor,
		LateBySeconds: int64(decidedAt.Sub(scheduledFor) / time.Second),
		StartedAt:     &decidedAt,
		Command:       j.CommandName,
		OwnerPID:      os.Getpid(),
	}
}

func (rn *Runner) Context(run *state.Run) config.RunContext {
	return config.RunContext{ID: run.ID, Trigger: run.Trigger, ScheduledFor: run.ScheduledFor, LateBy: run.LateBy()}
}

func (rn *Runner) Execute(ctx context.Context, req Request) *state.Run {
	run, p, j := req.Run, req.Project, req.Job
	rc := rn.Context(run)
	prompt := config.RenderPrompt(rn.Machine, p, j, rc)
	inv := config.Invoke(rn.Machine, p, j, rc, prompt)
	run.Argv = inv.Argv
	rn.Store.WriteRunFile(run, "prompt.md", []byte(prompt))
	rn.Store.SaveRun(run)

	if j.Precheck != "" && !req.SkipPrecheck {
		if !rn.precheck(ctx, p, j, run, rc) {
			return rn.finish(run)
		}
	}

	stdout, err := os.OpenFile(filepath.Join(rn.Store.RunDir(run), "stdout"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return rn.fail(run, err.Error())
	}
	stderr, err := os.OpenFile(filepath.Join(rn.Store.RunDir(run), "stderr"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		stdout.Close()
		return rn.fail(run, err.Error())
	}
	cmd := exec.Command(inv.Argv[0], inv.Argv[1:]...)
	cmd.Dir = inv.Dir
	cmd.Env = append(os.Environ(), inv.Env...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if inv.Command.Stdin == config.StdinPrompt {
		cmd.Stdin = strings.NewReader(inv.Stdin)
	}
	result := runProcess(ctx, cmd, inv.Command.Timeout)
	stdout.Close()
	stderr.Close()
	truncate(filepath.Join(rn.Store.RunDir(run), "stdout"))
	truncate(filepath.Join(rn.Store.RunDir(run), "stderr"))

	switch {
	case result.startErr != nil:
		return rn.fail(run, "start: "+result.startErr.Error())
	case result.interrupted:
		run.ExitCode = result.exitCode
		return rn.fail(run, state.ReasonInterrupted)
	case result.timedOut:
		run.ExitCode = result.exitCode
		return rn.fail(run, fmt.Sprintf("%s after %s", state.ReasonTimeout, inv.Command.Timeout))
	case result.exitCode != nil && *result.exitCode == 0:
		run.ExitCode = result.exitCode
		run.Status = state.StatusSucceeded
		return rn.finish(run)
	default:
		run.ExitCode = result.exitCode
		return rn.fail(run, describeExit(result.exitCode))
	}
}

func (rn *Runner) precheck(ctx context.Context, p *config.Project, j *config.Job, run *state.Run, rc config.RunContext) bool {
	logPath := filepath.Join(rn.Store.RunDir(run), "precheck.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		run.Status, run.Reason = state.StatusPrecheckError, err.Error()
		return false
	}
	stdoutPath := logPath + ".stdout"
	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		log.Close()
		run.Status, run.Reason = state.StatusPrecheckError, err.Error()
		return false
	}
	cmd := exec.Command("/bin/sh", "-c", j.Precheck)
	cmd.Dir = p.Dir
	cmd.Env = append(os.Environ(), config.RunEnv(p, j, rc)...)
	cmd.Stdout = multiFile{stdout, log}
	cmd.Stderr = log
	result := runProcess(ctx, cmd, rn.Machine.PrecheckTimeout)
	stdout.Close()
	log.Close()
	out, _ := os.ReadFile(stdoutPath)
	os.Remove(stdoutPath)
	truncate(logPath)

	switch {
	case result.startErr != nil:
		run.Status, run.Reason = state.StatusPrecheckError, "start: "+result.startErr.Error()
	case result.interrupted:
		run.Status, run.Reason = state.StatusFailed, state.ReasonInterrupted
	case result.timedOut:
		run.Status, run.Reason = state.StatusPrecheckError, fmt.Sprintf("precheck timeout after %s", rn.Machine.PrecheckTimeout)
	case *result.exitCode == 0:
		return true
	case *result.exitCode == 1:
		run.Status = state.StatusSkipped
		run.Reason = state.ReasonPrecheck
		if detail := firstLines(string(out)); detail != "" {
			run.Reason += ": " + detail
		}
	default:
		run.Status, run.Reason = state.StatusPrecheckError, "precheck "+describeExit(result.exitCode)
	}
	if run.Status == state.StatusPrecheckError {
		rn.notify(state.StatusPrecheckError, run, run.Reason)
	}
	return false
}

func (rn *Runner) fail(run *state.Run, reason string) *state.Run {
	run.Status, run.Reason = state.StatusFailed, reason
	rn.notify(state.StatusFailed, run, reason)
	return rn.finish(run)
}

func (rn *Runner) finish(run *state.Run) *state.Run {
	now := rn.Now()
	run.EndedAt = &now
	entry, _ := rn.Store.Record(run)
	return entry
}

func (rn *Runner) notify(event string, run *state.Run, message string) {
	if rn.Notifier != nil {
		rn.Notifier.Notify(event, fmt.Sprintf("ags: %s/%s %s", run.Project, run.Job, event), message)
	}
}

type processResult struct {
	exitCode    *int
	timedOut    bool
	interrupted bool
	startErr    error
}

func runProcess(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) processResult {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = killGrace
	if err := cmd.Start(); err != nil {
		return processResult{startErr: err}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var result processResult
	var err error
	select {
	case err = <-done:
	case <-timer.C:
		result.timedOut = true
		err = killGroup(cmd, done)
	case <-ctx.Done():
		result.interrupted = true
		err = killGroup(cmd, done)
	}
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		code = -1
	}
	result.exitCode = &code
	return result
}

func killGroup(cmd *exec.Cmd, done chan error) error {
	pgid := cmd.Process.Pid
	syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case err := <-done:
		syscall.Kill(-pgid, syscall.SIGKILL)
		return err
	case <-time.After(killGrace):
		syscall.Kill(-pgid, syscall.SIGKILL)
		return <-done
	}
}

func describeExit(code *int) string {
	if code == nil {
		return "no exit code"
	}
	if *code < 0 {
		return "killed by a signal"
	}
	return fmt.Sprintf("exit %d", *code)
}

func firstLines(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

func truncate(path string) {
	if info, err := os.Stat(path); err == nil && info.Size() > state.OutputLimit {
		os.Truncate(path, state.OutputLimit)
	}
}

type multiFile []*os.File

func (m multiFile) Write(b []byte) (int, error) {
	for _, f := range m {
		if _, err := f.Write(b); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}
