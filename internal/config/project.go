package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/skelz0r/ags/internal/schedule"
	"gopkg.in/yaml.v3"
)

const (
	SharedFileName = ".ags.yml"
	LocalFileName  = ".ags.local.yml"
	OriginShared   = "shared"
	OriginLocal    = "local"
	DefaultCatchUp = 12 * time.Hour
)

var jobName = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

var jobKeys = []string{"schedule", "prompt", "prompt_file", "command", "precheck", "catch_up", "enabled"}

type field struct {
	Value string
	Line  int
	File  string
}

type rawJob struct {
	Name   string
	Line   int
	Fields map[string]field
}

type rawFile struct {
	Path          string
	Exists        bool
	Version       *field
	Timezone      *field
	Preprompt     *field
	PrepromptFile *field
	Jobs          []*rawJob
}

type Job struct {
	Project        string
	Name           string
	Origin         string
	File           string
	Line           int
	ScheduleExpr   string
	Schedule       *schedule.Schedule
	Prompt         string
	PromptFile     string
	CommandName    string
	Precheck       string
	CatchUp        time.Duration
	CatchUpNever   bool
	Enabled        bool
	LocalOverrides []string
	Fingerprint    string
	ApprovalText   string
}

func (j *Job) Key() string { return j.Project + "/" + j.Name }

func (j *Job) NeedsApproval() bool { return j.Origin == OriginShared }

type Project struct {
	Name           string
	Dir            string
	Timezone       string
	Location       *time.Location
	Preprompt      string
	LocalPreprompt string
	Jobs           []*Job
	Warnings       Issues
	Sources        []string
	SharedFile     string
	LocalFile      string
}

func (p *Project) Job(name string) *Job {
	for _, j := range p.Jobs {
		if j.Name == name {
			return j
		}
	}
	return nil
}

type Loader struct {
	Machine   *Machine
	Overrides map[string][]byte
	SkipGit   bool
	snapshot  map[string]*snapshotFile
}

type snapshotFile struct {
	data   []byte
	exists bool
	err    error
}

func (l Loader) read(path string) ([]byte, bool, error) {
	if data, ok := l.Overrides[path]; ok {
		return data, true, nil
	}
	if f, ok := l.snapshot[path]; ok {
		return f.data, f.exists, f.err
	}
	f := &snapshotFile{}
	f.data, f.err = os.ReadFile(path)
	f.exists = f.err == nil
	if errors.Is(f.err, fs.ErrNotExist) {
		f.data, f.err = nil, nil
	}
	l.snapshot[path] = f
	return f.data, f.exists, f.err
}

func (l Loader) Load(name, dir string) (*Project, Issues) {
	l.snapshot = map[string]*snapshotFile{}
	p := &Project{
		Name:       name,
		Dir:        dir,
		SharedFile: filepath.Join(dir, SharedFileName),
		LocalFile:  filepath.Join(dir, LocalFileName),
		Sources:    []string{filepath.Join(dir, SharedFileName), filepath.Join(dir, LocalFileName)},
	}
	var issues Issues
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		issues.add(dir, 0, "project directory not found")
		return nil, issues
	}
	shared, sharedIssues := l.parseFile(p.SharedFile, true)
	local, localIssues := l.parseFile(p.LocalFile, false)
	issues = append(issues, sharedIssues...)
	issues = append(issues, localIssues...)
	if len(issues) > 0 {
		return nil, issues
	}
	issues = append(issues, l.resolve(p, shared, local)...)
	if local.Exists && !l.SkipGit && !gitIgnored(dir, LocalFileName) {
		p.Warnings.add(p.LocalFile, 0, "not ignored by git: add %s to .gitignore", LocalFileName)
	}
	if len(issues) > 0 {
		return nil, issues
	}
	return p, nil
}

func (l Loader) parseFile(path string, shared bool) (*rawFile, Issues) {
	rf := &rawFile{Path: path}
	var issues Issues
	data, exists, err := l.read(path)
	if err != nil {
		issues.add(path, 0, "%v", err)
		return rf, issues
	}
	if !exists {
		return rf, nil
	}
	rf.Exists = true
	root, parseIssues := parseDocument(path, data)
	if parseIssues != nil {
		return rf, parseIssues
	}
	scalarField := func(p pair) *field {
		s, err := scalarString(p.value)
		if err != nil {
			issues.add(path, p.key.Line, "%s: %v", p.key.Value, err)
			return nil
		}
		return &field{Value: s, Line: p.key.Line, File: path}
	}
	for _, p := range pairsOf(root) {
		switch p.key.Value {
		case "version":
			rf.Version = scalarField(p)
		case "timezone":
			rf.Timezone = scalarField(p)
		case "preprompt":
			rf.Preprompt = scalarField(p)
		case "preprompt_file":
			rf.PrepromptFile = scalarField(p)
		case "jobs":
			issues = append(issues, rf.parseJobs(p.value)...)
		default:
			issues.add(path, p.key.Line, "unknown key %q", p.key.Value)
		}
	}
	switch {
	case rf.Version == nil && shared:
		issues.add(path, 1, "version is required (version: 1)")
	case rf.Version != nil && rf.Version.Value != "1":
		issues.add(path, rf.Version.Line, "unsupported version %q, expected 1", rf.Version.Value)
	}
	return rf, issues
}

func (rf *rawFile) parseJobs(node *yaml.Node) Issues {
	var issues Issues
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		issues.add(rf.Path, node.Line, "jobs: expected a mapping")
		return issues
	}
	for _, p := range pairsOf(node) {
		job := &rawJob{Name: p.key.Value, Line: p.key.Line, Fields: map[string]field{}}
		if !jobName.MatchString(job.Name) {
			issues.add(rf.Path, job.Line, "invalid job name %q: use [a-z0-9-], at most 40 characters", job.Name)
		}
		if p.value.Kind != yaml.MappingNode {
			issues.add(rf.Path, job.Line, "job %q: expected a mapping", job.Name)
			continue
		}
		for _, f := range pairsOf(p.value) {
			if !contains(jobKeys, f.key.Value) {
				issues.add(rf.Path, f.key.Line, "job %q: unknown key %q", job.Name, f.key.Value)
				continue
			}
			if f.value.Kind != yaml.ScalarNode {
				issues.add(rf.Path, f.key.Line, "job %q: %s: expected a scalar", job.Name, f.key.Value)
				continue
			}
			value := f.value.Value
			if f.value.Tag == "!!null" {
				value = ""
			}
			if f.key.Value == "enabled" && f.value.Tag != "!!bool" {
				issues.add(rf.Path, f.key.Line, "job %q: enabled: expected true or false", job.Name)
				continue
			}
			job.Fields[f.key.Value] = field{Value: value, Line: f.key.Line, File: rf.Path}
		}
		if _, ok := job.Fields["prompt"]; ok {
			if _, both := job.Fields["prompt_file"]; both {
				issues.add(rf.Path, job.Line, "job %q: prompt and prompt_file are exclusive", job.Name)
			}
		}
		rf.Jobs = append(rf.Jobs, job)
	}
	return issues
}

func (rf *rawFile) job(name string) *rawJob {
	for _, j := range rf.Jobs {
		if j.Name == name {
			return j
		}
	}
	return nil
}

func (l Loader) resolve(p *Project, shared, local *rawFile) Issues {
	var issues Issues
	p.Location = time.Local
	p.Timezone = time.Local.String()
	for _, tz := range []*field{shared.Timezone, local.Timezone} {
		if tz == nil || tz.Value == "" {
			continue
		}
		loc, err := time.LoadLocation(tz.Value)
		if err != nil {
			issues.add(tz.File, tz.Line, "unknown timezone %q", tz.Value)
			continue
		}
		p.Location, p.Timezone = loc, tz.Value
	}
	var err error
	if p.Preprompt, err = l.preprompt(p, shared); err != nil {
		issues = append(issues, err.(Issues)...)
	}
	if p.LocalPreprompt, err = l.preprompt(p, local); err != nil {
		issues = append(issues, err.(Issues)...)
	}
	for _, raw := range shared.Jobs {
		override := local.job(raw.Name)
		job, jobIssues := l.resolveJob(p, raw, override, shared.Path)
		issues = append(issues, jobIssues...)
		if job != nil {
			p.Jobs = append(p.Jobs, job)
		}
	}
	for _, raw := range local.Jobs {
		if shared.job(raw.Name) != nil {
			continue
		}
		incomplete := false
		if _, ok := raw.Fields["schedule"]; !ok {
			incomplete = true
			issues.add(local.Path, raw.Line, "local job %q is incomplete: schedule is required for a job not in %s", raw.Name, SharedFileName)
		}
		_, hasPrompt := raw.Fields["prompt"]
		_, hasPromptFile := raw.Fields["prompt_file"]
		if !hasPrompt && !hasPromptFile {
			incomplete = true
			issues.add(local.Path, raw.Line, "local job %q is incomplete: prompt or prompt_file is required for a job not in %s", raw.Name, SharedFileName)
		}
		if incomplete {
			continue
		}
		job, jobIssues := l.resolveJob(p, raw, nil, local.Path)
		issues = append(issues, jobIssues...)
		if job != nil {
			job.Origin = OriginLocal
			p.Jobs = append(p.Jobs, job)
		}
	}
	return issues
}

func (l Loader) preprompt(p *Project, rf *rawFile) (string, error) {
	var issues Issues
	if rf.Preprompt != nil && rf.PrepromptFile != nil {
		issues.add(rf.Path, rf.PrepromptFile.Line, "preprompt and preprompt_file are exclusive")
		return "", issues
	}
	if rf.Preprompt != nil {
		return rf.Preprompt.Value, nil
	}
	if rf.PrepromptFile == nil {
		return "", nil
	}
	text, path, err := l.readPromptFile(p.Dir, rf.PrepromptFile.Value)
	p.Sources = append(p.Sources, path)
	if err != nil {
		issues.add(rf.Path, rf.PrepromptFile.Line, "preprompt_file %q: %v", rf.PrepromptFile.Value, err)
		return "", issues
	}
	return text, nil
}

func (l Loader) readPromptFile(dir, rel string) (string, string, error) {
	path := rel
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, rel)
	}
	data, exists, err := l.read(path)
	if err != nil {
		return "", path, err
	}
	if !exists {
		return "", path, fmt.Errorf("file not found")
	}
	return string(data), path, nil
}

func (l Loader) resolveJob(p *Project, shared, local *rawJob, file string) (*Job, Issues) {
	var issues Issues
	fields := map[string]field{}
	for k, v := range shared.Fields {
		fields[k] = v
	}
	job := &Job{
		Project: p.Name,
		Name:    shared.Name,
		Origin:  OriginShared,
		File:    file,
		Line:    shared.Line,
		Enabled: true,
		CatchUp: DefaultCatchUp,
	}
	if local != nil {
		for k := range local.Fields {
			if k == "prompt" || k == "prompt_file" {
				delete(fields, "prompt")
				delete(fields, "prompt_file")
			}
		}
		for k, v := range local.Fields {
			fields[k] = v
			job.LocalOverrides = append(job.LocalOverrides, k)
		}
		sort.Strings(job.LocalOverrides)
	}
	fieldFile := func(key string) string {
		if f, ok := fields[key]; ok {
			return f.File
		}
		return job.File
	}
	line := func(key string) int {
		if f, ok := fields[key]; ok {
			return f.Line
		}
		return job.Line
	}

	job.ScheduleExpr = fields["schedule"].Value
	if _, ok := fields["schedule"]; !ok {
		issues.add(job.File, job.Line, "job %q: schedule is required", job.Name)
	} else if sched, err := schedule.Parse(job.ScheduleExpr, p.Location); err != nil {
		issues.add(fieldFile("schedule"), line("schedule"), "job %q: %v", job.Name, err)
	} else {
		job.Schedule = sched
	}

	if f, ok := fields["prompt_file"]; ok {
		text, path, err := l.readPromptFile(p.Dir, f.Value)
		p.Sources = append(p.Sources, path)
		job.PromptFile = f.Value
		if err != nil {
			issues.add(f.File, f.Line, "job %q: prompt_file %q: %v", job.Name, f.Value, err)
		}
		job.Prompt = text
	} else if f, ok := fields["prompt"]; ok {
		job.Prompt = f.Value
	} else {
		issues.add(job.File, job.Line, "job %q: prompt or prompt_file is required", job.Name)
	}
	if strings.TrimSpace(job.Prompt) == "" && len(issues) == 0 {
		issues.add(fieldFile("prompt"), line("prompt"), "job %q: the prompt is empty", job.Name)
	}

	job.CommandName = fields["command"].Value
	if job.CommandName == "" {
		job.CommandName = l.Machine.DefaultCommand
	}
	switch {
	case job.CommandName == "":
		issues.add(fieldFile("command"), line("command"), "job %q: no command and no default_command in %s", job.Name, l.Machine.Path)
	case l.Machine.Commands[job.CommandName] == nil:
		issues.add(fieldFile("command"), line("command"), "job %q: unknown command %q, not defined in %s", job.Name, job.CommandName, l.Machine.Path)
	}

	job.Precheck = fields["precheck"].Value
	if job.Precheck == "" && strings.Contains(job.Prompt, PrecheckOutputPlaceholder) {
		promptKey := "prompt"
		if job.PromptFile != "" {
			promptKey = "prompt_file"
		}
		p.Warnings.add(fieldFile(promptKey), line(promptKey), "job %q: the prompt uses %s but the job has no precheck: it is replaced by nothing", job.Name, PrecheckOutputPlaceholder)
	}

	if f, ok := fields["catch_up"]; ok {
		if f.Value == "never" {
			job.CatchUpNever = true
		} else if d, err := ParseDuration(f.Value); err != nil {
			issues.add(f.File, f.Line, "job %q: catch_up: %v", job.Name, err)
		} else {
			job.CatchUp = d
		}
	}
	if f, ok := fields["enabled"]; ok {
		job.Enabled = f.Value == "true"
	}
	if len(issues) > 0 {
		return nil, issues
	}
	job.ApprovalText = l.approvalText(p, shared)
	sum := sha256.Sum256([]byte(job.ApprovalText))
	job.Fingerprint = hex.EncodeToString(sum[:])
	return job, nil
}

func (l Loader) approvalText(p *Project, shared *rawJob) string {
	prompt := shared.Fields["prompt"].Value
	if f, ok := shared.Fields["prompt_file"]; ok {
		var path string
		prompt, path, _ = l.readPromptFile(p.Dir, f.Value)
		p.Sources = append(p.Sources, path)
	}
	command := shared.Fields["command"].Value
	if command == "" {
		command = "(default)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "command: %s\n", command)
	fmt.Fprintf(&b, "precheck: %s\n", shared.Fields["precheck"].Value)
	b.WriteString("project preprompt:\n")
	b.WriteString(indentBlock(p.Preprompt))
	b.WriteString("prompt:\n")
	b.WriteString(indentBlock(prompt))
	return b.String()
}

func indentBlock(text string) string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return ""
	}
	return "  " + strings.ReplaceAll(text, "\n", "\n  ") + "\n"
}

func gitIgnored(dir, name string) bool {
	cmd := exec.Command("git", "-C", dir, "check-ignore", "-q", name)
	err := cmd.Run()
	if err == nil {
		return true
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false
	}
	return true
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
