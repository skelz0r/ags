package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/skelz0r/ags/internal/agstest"
)

type validation struct {
	Valid  bool `json:"valid"`
	Errors []struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Message string `json:"message"`
	} `json:"errors"`
	Warnings []struct {
		File    string `json:"file"`
		Message string `json:"message"`
	} `json:"warnings"`
	Jobs []struct {
		Name       string   `json:"name"`
		Origin     string   `json:"origin"`
		Schedule   string   `json:"schedule"`
		Command    string   `json:"command"`
		Precheck   string   `json:"precheck"`
		Enabled    bool     `json:"enabled"`
		Next       []string `json:"next"`
		Overrides  []string `json:"local_overrides"`
		PromptFile string   `json:"prompt_file"`
	} `json:"jobs"`
}

func validate(t *testing.T, w *World) (validation, Result) {
	t.Helper()
	res := w.CLI("validate", w.ProjectDir, "--json")
	var v validation
	res.Decode(t, &v)
	return v, res
}

const sharedFile = `version: 1
timezone: Europe/Paris
jobs:
  security-audit:
    schedule: "30 9 * * 1"
    prompt: Audit.
    precheck: "true"
  code-audit:
    schedule: "0 14 1 * *"
    prompt_file: .ags/code-audit.md
    enabled: false
`

func TestTheLocalFileReplacesASharedJobFieldByFieldAndAddsItsOwnJobs(t *testing.T) {
	w := New(t)
	w.SharedFile(sharedFile)
	w.Write(".ags/code-audit.md", "Audit the code.\n")
	w.LocalFile(`jobs:
  security-audit:
    command: slow
  code-audit:
    enabled: true
  deps-triage:
    schedule: "0 9 * * 5"
    prompt: Triage.
`)
	v, res := validate(t, w)
	if res.Exit != 0 || !v.Valid {
		t.Fatalf("expected valid, got %d %+v", res.Exit, v.Errors)
	}
	jobs := map[string]int{}
	for i, j := range v.Jobs {
		jobs[j.Name] = i
	}
	security := v.Jobs[jobs["security-audit"]]
	if security.Command != "slow" || security.Schedule != "30 9 * * 1" || security.Precheck != "true" || security.Origin != "shared" {
		t.Fatalf("the local command replaces only that field, got %+v", security)
	}
	if code := v.Jobs[jobs["code-audit"]]; !code.Enabled || code.PromptFile != ".ags/code-audit.md" {
		t.Fatalf("the local file enables the shared job, got %+v", code)
	}
	deps, ok := jobs["deps-triage"]
	if !ok || v.Jobs[deps].Origin != "local" || v.Jobs[deps].Command != "record" {
		t.Fatalf("the local job is added with the default command, got %+v", v.Jobs)
	}
	if len(v.Jobs[0].Next) != 3 {
		t.Fatalf("validate lists the next 3 occurrences")
	}
}

func TestAnIncompleteLocalJobIsAnError(t *testing.T) {
	w := New(t)
	w.SharedFile(sharedFile)
	w.Write(".ags/code-audit.md", "Audit the code.\n")
	w.LocalFile("jobs:\n  deps-triage:\n    schedule: \"0 9 * * 5\"\n")
	v, res := validate(t, w)
	if res.Exit != 2 || len(v.Errors) != 1 || !strings.Contains(v.Errors[0].Message, "incomplete") || v.Errors[0].Line != 2 || filepath.Base(v.Errors[0].File) != ".ags.local.yml" {
		t.Fatalf("expected an incomplete local job error on .ags.local.yml:2, got %d %+v", res.Exit, v.Errors)
	}
}

func TestValidationErrorsNameTheFileAndTheLine(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		line    int
		message string
	}{
		{"unknown command", "version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n    command: nope\n", 6, `unknown command "nope"`},
		{"unknown key", "version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n    shedule: x\n", 6, `unknown key "shedule"`},
		{"unknown top-level key", "version: 1\ntimezon: Europe/Paris\n", 2, `unknown key "timezon"`},
		{"missing prompt_file", "version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt_file: .ags/missing.md\n", 5, `prompt_file ".ags/missing.md": file not found`},
		{"invalid cron", "version: 1\njobs:\n  a:\n    schedule: \"61 * * * *\"\n    prompt: A.\n", 4, "invalid schedule"},
		{"yaml syntax", "version: 1\njobs:\n  a: [\n", 3, "did not find expected node content"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := New(t)
			w.SharedFile(c.file)
			v, res := validate(t, w)
			if res.Exit != 2 || res.OK || res.Error.Code != "invalid_config" {
				t.Fatalf("expected exit 2 invalid_config, got %d %+v", res.Exit, res.Error)
			}
			if len(v.Errors) != 1 {
				t.Fatalf("expected one error, got %+v", v.Errors)
			}
			e := v.Errors[0]
			if filepath.Base(e.File) != ".ags.yml" || e.Line != c.line || !strings.Contains(e.Message, c.message) {
				t.Fatalf("expected .ags.yml:%d %q, got %s:%d %q", c.line, c.message, e.File, e.Line, e.Message)
			}
		})
	}
}

func TestValidateWarnsWhenTheLocalFileIsNotIgnoredByGit(t *testing.T) {
	w := New(t)
	w.Write(".gitignore", "")
	w.LocalFile("jobs:\n  mine:\n    schedule: \"@daily\"\n    prompt: Mine.\n")
	v, res := validate(t, w)
	if res.Exit != 0 || len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0].Message, "not ignored by git") {
		t.Fatalf("expected a git warning, got %d %+v", res.Exit, v.Warnings)
	}
}

func TestEditingCommandsRefuseToRunInsideARun(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n")
	w.Register()
	w.Env["AGS_RUN_ID"] = "20261019T073000Z-abcdef"
	for _, args := range [][]string{
		{"run", "proj/a"},
		{"run", "proj/a", "--dry-run", "--dry-run=false"},
		{"add", w.ProjectDir, "--name", "other"},
		{"approve", "proj", "--yes"},
		{"pause", "proj/a"},
		{"resume", "proj/a"},
		{"remove", "proj"},
		{"new", "proj/b", "--schedule", "@daily", "--prompt", "B."},
		{"delete", "proj/a", "--yes"},
		{"edit", "proj/a"},
	} {
		res := w.CLI(append(args, "--json")...)
		if res.Exit != 1 || res.OK || res.Error.Code != "inside_run" {
			t.Errorf("ags %v should refuse inside a run, got %d %+v", args, res.Exit, res.Error)
		}
	}
	if res := w.CLI("run", "proj/a", "--dry-run", "--json"); res.Exit != 0 {
		t.Errorf("a dry run stays allowed inside a run, got %d %s", res.Exit, res.Stdout)
	}
	if res := w.CLI("jobs", "--json"); res.Exit != 0 {
		t.Errorf("reading stays allowed inside a run, got %d", res.Exit)
	}
}

func TestTheJSONContract(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n")
	w.Register()

	cases := []struct {
		name string
		args []string
		exit int
		ok   bool
		code string
	}{
		{"success", []string{"jobs"}, 0, true, ""},
		{"usage error", []string{"next"}, 2, false, "usage"},
		{"unknown command", []string{"frobnicate"}, 2, false, "usage"},
		{"not found", []string{"next", "proj/missing"}, 1, false, "not_found"},
		{"unknown project", []string{"run", "nope/a"}, 1, false, "not_found"},
		{"manual run", []string{"run", "proj/a"}, 0, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := w.CLI(append(c.args, "--json")...)
			var raw map[string]json.RawMessage
			json.Unmarshal([]byte(res.Stdout), &raw)
			for _, key := range []string{"ok", "data", "error"} {
				if _, present := raw[key]; !present {
					t.Fatalf("the envelope always has %q: %s", key, res.Stdout)
				}
			}
			if res.Exit != c.exit || res.OK != c.ok {
				t.Fatalf("expected exit %d ok=%v, got %d ok=%v: %s", c.exit, c.ok, res.Exit, res.OK, res.Stdout)
			}
			if c.code == "" && string(raw["error"]) != "null" {
				t.Fatalf("a success has a null error: %s", raw["error"])
			}
			if c.code != "" && (res.Error == nil || res.Error.Code != c.code || res.Error.Message == "") {
				t.Fatalf("expected error code %q with a message, got %+v", c.code, res.Error)
			}
		})
	}

	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n    command: failing\n")
	w.MustCLI("approve", "proj", "--yes")
	if res := w.CLI("run", "proj/a", "--json"); res.Exit != 1 || res.Error.Code != "run_failed" {
		t.Fatalf("a failed run exits 1 with run_failed, got %d %+v", res.Exit, res.Error)
	}
	w.SharedFile("version: 2\n")
	if res := w.CLI("jobs", "--project", "proj", "--json"); res.Exit != 0 {
		t.Fatalf("jobs reports invalid projects in data, got %d", res.Exit)
	}
	if res := w.CLI("next", "proj/a", "--json"); res.Exit != 2 || res.Error.Code != "invalid_config" {
		t.Fatalf("an invalid config exits 2, got %d %+v", res.Exit, res.Error)
	}
}

func TestDryRunPrintsThePrecheckTheRenderedPromptAndTheExactArgv(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
timezone: Europe/Paris
preprompt: Project preprompt.
jobs:
  a:
    schedule: "@daily"
    prompt: "Job {job.name} of {project.name}."
    precheck: "true"
`)
	w.LocalFile("preprompt: Local preprompt.\n")
	w.Register()
	var d struct {
		Precheck string   `json:"precheck"`
		Prompt   string   `json:"prompt"`
		Argv     []string `json:"argv"`
	}
	w.MustCLI("run", "proj/a", "--dry-run", "--json").Decode(t, &d)
	want := "Machine preprompt.\n\nProject preprompt.\n\nLocal preprompt.\n\nJob a of proj.\n"
	if d.Prompt != want || d.Precheck != "true" || strings.Join(d.Argv, " ") != "sh -c cat; echo done" {
		t.Fatalf("unexpected dry run %+v", d)
	}
	if runs := w.Runs(); len(runs) != 0 {
		t.Fatalf("a dry run runs nothing, got %d runs", len(runs))
	}
}

func TestAPromptPassedInArgvKeepsTheCommandPlaceholdersItMentions(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
jobs:
  a:
    schedule: "@daily"
    prompt: "Explain {project.path} and {run.id}."
    command: prompt-arg
`)
	w.Register()
	if res := w.MustCLI("run", "proj/a"); !strings.Contains(res.Stdout, "Explain {project.path} and {run.id}.") {
		t.Fatalf("the prompt must reach argv as written, got %q", res.Stdout)
	}
}

func TestAProjectPathHoldingThePromptPlaceholderStaysAsIsInArgv(t *testing.T) {
	w := New(t)
	w.ProjectDir = filepath.Join(w.Root, "a{prompt}b")
	if out, err := exec.Command("git", "init", "-q", w.ProjectDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	w.SharedFile(`version: 1
jobs:
  a:
    schedule: "@daily"
    prompt: Hello.
    command: path-and-prompt
`)
	w.Register()
	if res := w.MustCLI("run", "proj/a"); !strings.Contains(res.Stdout, w.ProjectDir+"|Machine preprompt.") {
		t.Fatalf("the project path must not get the prompt spliced in, got %q", res.Stdout)
	}
}

func TestADryRunShowsWhereThePrecheckOutputGoesWithoutRunningThePrecheck(t *testing.T) {
	w := New(t)
	w.SharedFile(`version: 1
jobs:
  a:
    schedule: "@daily"
    prompt: "Items: {precheck.output}"
    precheck: touch ran
`)
	w.Register()
	var d struct {
		Prompt string `json:"prompt"`
	}
	w.MustCLI("run", "proj/a", "--dry-run", "--json").Decode(t, &d)
	if d.Prompt != "Machine preprompt.\n\nItems: [output of the precheck, not run by --dry-run]\n" {
		t.Fatalf("unexpected dry run prompt %q", d.Prompt)
	}
	if _, err := os.Stat(filepath.Join(w.ProjectDir, "ran")); err == nil {
		t.Fatalf("a dry run must not run the precheck")
	}
}

func TestAManualRunOfAnUnapprovedJobIsRefused(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n")
	w.MustCLI("add", w.ProjectDir, "--name", "proj")
	if res := w.CLI("run", "proj/a", "--json"); res.Exit != 1 || res.Error.Code != "unapproved" {
		t.Fatalf("expected unapproved, got %d %+v", res.Exit, res.Error)
	}
}

func TestDeletingALocalJobEditsTheLocalFile(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n")
	w.LocalFile("jobs:\n  mine:\n    schedule: \"@daily\"\n    prompt: Mine.\n")
	w.Register()
	var edit struct {
		File string `json:"file"`
	}
	w.MustCLI("edit", "proj/mine", "--json").Decode(t, &edit)
	if filepath.Base(edit.File) != ".ags.local.yml" {
		t.Fatalf("a local job is edited in the local file, got %s", edit.File)
	}
	w.MustCLI("delete", "proj/mine", "--yes")
	var v validation
	w.MustCLI("validate", w.ProjectDir, "--json").Decode(t, &v)
	if len(v.Jobs) != 1 || v.Jobs[0].Name != "a" {
		t.Fatalf("expected only the shared job left, got %+v", v.Jobs)
	}
}

func TestAnInvalidMachineCommandIsAConfigErrorNotACrash(t *testing.T) {
	w := New(t)
	w.Write(w.Paths.MachineConfig(), strings.Replace(MachineConfig, "commands:\n", "commands:\n  empty:\n    argv: []\n", 1))
	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n    command: empty\n")
	res := w.CLI("add", w.ProjectDir, "--name", "proj", "--approve", "--json")
	if res.Exit != 2 || res.Error.Code != "invalid_config" || !strings.Contains(res.Error.Message, "argv is required") {
		t.Fatalf("expected invalid_config naming the machine error, got %d %+v", res.Exit, res.Error)
	}
}

func TestDeletingASharedJobAlsoDropsItsLocalOverride(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n  b:\n    schedule: \"@daily\"\n    prompt: B.\n")
	w.LocalFile("jobs:\n  a:\n    enabled: false\n")
	w.Register()
	w.MustCLI("delete", "proj/a", "--yes")
	var v validation
	w.MustCLI("validate", w.ProjectDir, "--json").Decode(t, &v)
	if len(v.Jobs) != 1 || v.Jobs[0].Name != "b" {
		t.Fatalf("expected only b left and a valid project, got %+v", v)
	}
}

func TestApprovingWithReviewedFingerprintsRefusesContentChangedSinceTheReview(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n")
	w.MustCLI("add", w.ProjectDir, "--name", "proj")
	var shown struct {
		Pending []struct {
			Fingerprint string `json:"fingerprint"`
		} `json:"pending"`
	}
	w.CLI("approve", "proj", "--json").Decode(t, &shown)
	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: Something else.\n")
	res := w.CLI("approve", "proj", "--yes", "--fingerprints", shown.Pending[0].Fingerprint, "--json")
	if res.Exit != 1 || res.OK {
		t.Fatalf("approval of an unseen version must be refused, got %d %s", res.Exit, res.Stdout)
	}
	if res := w.CLI("run", "proj/a", "--json"); res.Error == nil || res.Error.Code != "unapproved" {
		t.Fatalf("the job must stay unapproved, got %+v", res.Error)
	}
}

func TestANewSharedJobIsApprovedInTheVersionThatWasWritten(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n")
	w.Register()
	var job struct {
		Approved bool `json:"approved"`
	}
	w.MustCLI("new", "proj/b", "--schedule", "@daily", "--prompt", "B.", "--json").Decode(t, &job)
	if !job.Approved {
		t.Fatalf("a shared job written by ags new is approved")
	}
	var d struct {
		Prompt   string `json:"prompt"`
		Approved bool   `json:"approved"`
	}
	w.MustCLI("run", "proj/b", "--dry-run", "--json").Decode(t, &d)
	if !d.Approved || !strings.HasSuffix(d.Prompt, "B.\n") {
		t.Fatalf("expected the written version approved, got %+v", d)
	}
}

func TestShellCompletionOffersCommandsFlagsAndKnownTargets(t *testing.T) {
	w := New(t)
	w.SharedFile("version: 1\njobs:\n  a:\n    schedule: \"@daily\"\n    prompt: A.\n")
	w.Register()
	run := w.MustCLI("run", "proj/a", "--json")
	var ran struct {
		ID string `json:"id"`
	}
	run.Decode(t, &ran)

	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"commands", []string{""}, []string{"run", "show", "completion"}},
		{"jobs to run", []string{"run", ""}, []string{"proj/a"}},
		{"flags of a command", []string{"run", "--"}, []string{"--dry-run", "--skip-precheck", "--json"}},
		{"recent runs to show", []string{"show", ""}, []string{ran.ID}},
		{"projects and jobs to pause", []string{"pause", ""}, []string{"proj", "proj/a"}},
		{"a project to put a new job in", []string{"new", ""}, []string{"proj/"}},
		{"value of --project", []string{"runs", "--project", ""}, []string{"proj"}},
		{"value of --status", []string{"runs", "--status", ""}, []string{"failed", "precheck_error"}},
		{"inline value of --status", []string{"runs", "--status=f"}, []string{"--status=failed"}},
		{"files for a flag value starting with a dash", []string{"new", "proj/b", "--prompt-file", "-"}, []string{":files"}},
		{"jobs after an inline boolean", []string{"run", "--dry-run=false", ""}, []string{"proj/a"}},
		{"files for a path", []string{"validate", ""}, []string{":files"}},
		{"shells", []string{"completion", ""}, []string{"zsh"}},
		{"nothing past the last argument", []string{"run", "proj/a", ""}, nil},
		{"nothing for a free value", []string{"next", "-n", ""}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := w.CLI(append([]string{"__complete"}, c.words...)...)
			if res.Exit != 0 || res.Stderr != "" {
				t.Fatalf("completion never fails, got %d %q", res.Exit, res.Stderr)
			}
			got := map[string]bool{}
			for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
				if value, _, _ := strings.Cut(line, "\t"); value != "" {
					got[value] = true
				}
			}
			if c.want == nil && len(got) > 0 {
				t.Fatalf("expected no candidate, got %v", got)
			}
			for _, want := range c.want {
				if !got[want] {
					t.Fatalf("expected %q among %v", want, got)
				}
			}
		})
	}
}

func TestShellCompletionWritesNothing(t *testing.T) {
	w := New(t)
	for _, words := range [][]string{{"show", ""}, {"run", ""}, {"runs", "--project", ""}} {
		w.CLI(append([]string{"__complete"}, words...)...)
	}
	if _, err := os.Stat(w.Paths.StateDir); !os.IsNotExist(err) {
		t.Fatalf("completing created the state directory: %v", err)
	}
}
