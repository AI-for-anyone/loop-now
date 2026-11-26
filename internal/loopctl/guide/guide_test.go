package guide

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/commands"
	"loop-now/internal/server/content"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

var update = flag.Bool("update", false, "update golden files")

const goldenVersion = "golden-version"

func render(t *testing.T) string {
	t.Helper()
	b, err := Render(goldenVersion)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The content of one version does not depend on time zone or environment and is byte-for-byte identical.
func TestDeterministic(t *testing.T) {
	a := render(t)
	t.Setenv("TZ", "Pacific/Kiritimati")
	t.Setenv("LANG", "C")
	t.Setenv("LOOPCTL_AGENT_TOKEN", "lna_x")
	if b := render(t); a != b {
		t.Fatal("two renders produced different content")
	}
	if !bytes.Equal(Files()["SKILL.md"], Files()["SKILL.md"]) || len(Files()) != 1 {
		t.Fatal("Files should hold only one stable SKILL.md")
	}
}

func TestGolden(t *testing.T) {
	got := []byte(render(t))
	p := filepath.Join("testdata", "SKILL.md")
	if *update {
		os.WriteFile(p, got, 0o644)
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read golden file: %v (generate it with -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("SKILL.md differs from %s; after checking the change, update it with go test -run TestGolden -update", p)
	}
}

func TestFrontmatterPassesPlatformValidation(t *testing.T) {
	meta, err := content.ParseSkillMarkdown(Files()["SKILL.md"])
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != SkillName {
		t.Fatalf("name is %q", meta.Name)
	}
}

var (
	fence  = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
	inline = regexp.MustCompile("`([^`\n]+)`")
)

// commandLines extracts every command in the instructions that starts with loopctl: lines in code blocks and inline
// code; heredocs are removed.
func commandLines(md string) []string {
	var out []string
	for _, m := range fence.FindAllStringSubmatch(md, -1) {
		for _, line := range strings.Split(m[1], "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "loopctl ") {
				out = append(out, strings.TrimSpace(line))
			}
		}
	}
	rest := fence.ReplaceAllString(md, "")
	for _, m := range inline.FindAllStringSubmatch(rest, -1) {
		if strings.HasPrefix(m[1], "loopctl ") {
			out = append(out, m[1])
		}
	}
	for i, l := range out {
		if j := strings.Index(l, "<<"); j >= 0 {
			out[i] = strings.TrimSpace(l[:j])
		}
	}
	return out
}

// shellWords splits single- and double-quoted words following shell rules.
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	in, quote, has := false, rune(0), false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, has = r, true
		case r == ' ' || r == '\t':
			if in || has {
				out = append(out, cur.String())
				cur.Reset()
				in, has = false, false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if in || has {
		out = append(out, cur.String())
	}
	return out
}

// Every command in the instructions can be parsed by the same version with the Agent identity; every Agent command
// is written into the instructions; not a single external-only command appears.
func TestCommandsInGuide(t *testing.T) {
	md := string(Files()["SKILL.md"])
	r := commands.Registry()
	lines := commandLines(md)
	if len(lines) < 10 {
		t.Fatalf("found only %d commands: %v", len(lines), lines)
	}
	seen := map[string]bool{}
	for _, l := range lines {
		argv := shellWords(l)[1:]
		if e := cli.Check(r, argv, cli.IdentityAgent); e != nil {
			t.Errorf("command %q in the guide does not parse with the Agent identity: %v", l, e)
			continue
		}
		for _, c := range r.Commands() {
			if len(argv) >= len(c.Path) && strings.Join(argv[:len(c.Path)], " ") == c.Name() {
				seen[c.Name()] = true
			}
		}
	}
	for _, c := range r.Commands() {
		mentioned := regexp.MustCompile(`loopctl ` + regexp.QuoteMeta(c.Name()) + `(\s|` + "`" + `|$)`).MatchString(md)
		if c.AllowedFor(cli.IdentityAgent) && !seen[c.Name()] {
			t.Errorf("Agent command %s is missing from the guide", c.Name())
		}
		if !c.AllowedFor(cli.IdentityAgent) && mentioned {
			t.Errorf("the guide mentions the external-only command %s", c.Name())
		}
	}
}

func TestReminderCommand(t *testing.T) {
	cmd := InboxReminderCommand()
	if cmd != "loopctl inbox list --unread" {
		t.Fatalf("%q", cmd)
	}
	if e := cli.Check(commands.Registry(), shellWords(cmd)[1:], cli.IdentityAgent); e != nil {
		t.Fatal(e)
	}
}

// Generation fails when the template refers to a command that does not exist or that the Agent cannot use.
func TestTemplateReferenceChecked(t *testing.T) {
	orig := skillTemplate
	t.Cleanup(func() { skillTemplate = orig })
	for _, ref := range []string{`{{cmd "inbox archive"}}`, `{{cmd "agent list"}}`} {
		skillTemplate = orig + ref
		if _, err := Render("x"); err == nil {
			t.Errorf("rendering should fail when the template refers to %s", ref)
		}
	}
}

func TestShellWords(t *testing.T) {
	got := shellWords(`loopctl inbox send @console --text "build done" --x 'a b' -`)
	want := []string{"loopctl", "inbox", "send", "@console", "--text", "build done", "--x", "a b", "-"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
}

// The complete JSON examples in the instructions match the real API, so that a Manager following the Skill does
// not end up with an invalid workflow.
func TestTaskJSONExamplesMatchContract(t *testing.T) {
	doc := clitest.Load(t, "agent-openapi.yaml")
	md := strings.ReplaceAll(render(t), "<new action UUID>", "0192f1a2-0000-7000-8000-000000000053")
	md = strings.ReplaceAll(md, "<executor Agent ID>", "0192f1a2-0000-7000-8000-000000000001")
	md = strings.ReplaceAll(md, "<implementing Agent ID>", "0192f1a2-0000-7000-8000-000000000001")
	md = strings.ReplaceAll(md, "<verifying Agent ID>", "0192f1a2-0000-7000-8000-000000000002")
	md = strings.ReplaceAll(md, "<repository folder>", "/code/app")
	md = strings.ReplaceAll(md, "<notes folder>", "/docs/notes")
	blocks := regexp.MustCompile("(?s)```json\\n(.*?)```").FindAllStringSubmatch(md, -1)
	if len(blocks) != 4 {
		t.Fatalf("expected create, submit, git workspace and copy workspace JSON examples, got %d", len(blocks))
	}
	for i, name := range []string{"TaskCreateInput", "TaskSubmitInput", "TaskCreateInput", "TaskCreateInput"} {
		var body any
		if err := json.Unmarshal([]byte(blocks[i][1]), &body); err != nil {
			t.Fatal(err)
		}
		if err := doc.Components.Schemas[name].Value.VisitJSON(body, openapi3.EnableJSONSchema2020()); err != nil {
			t.Fatalf("example %d (%s) violates contract: %v", i, name, err)
		}
		if name != "TaskCreateInput" {
			continue
		}
		// The flow itself passes the server's workflow checks (edges, loops and their caps, input mappings).
		var in taskmodel.CreateInput
		if err := json.Unmarshal([]byte(blocks[i][1]), &in); err != nil {
			t.Fatal(err)
		}
		if err := in.Workflow.Validate(workflow.Limits{MaxSteps: 100, MaxEdges: 300}); err != nil {
			t.Fatalf("example %d workflow: %v", i, err)
		}
		checkFlowEnds(t, i, in.Workflow)
		// The workspace examples: git mode by default for a repository, copy mode named, never with a branch.
		if want := map[int]string{2: "", 3: taskmodel.WorkspaceModeCopy}[i]; i >= 2 && (in.Workspace == nil || in.Workspace.Mode != want ||
			(want == taskmodel.WorkspaceModeCopy && (in.Workspace.Branch != nil || in.Workspace.BaseBranch != nil))) {
			t.Errorf("example %d workspace = %+v, want mode %q", i, in.Workspace, want)
		}
	}
}

// checkFlowEnds checks the rule the server applies on submit: only a step without outgoing edges may submit
// edgeId null, so a step told to submit null has none, and the flow has such a step reachable from the start.
func checkFlowEnds(t *testing.T, i int, d workflow.Definition) {
	t.Helper()
	next := map[string][]string{}
	for _, e := range d.Edges {
		next[e.From] = append(next[e.From], e.To)
	}
	submitsNull := regexp.MustCompile(`(?i)edgeId"?:? ?null`)
	for _, s := range d.Steps {
		if submitsNull.MatchString(s.Description) && len(next[s.ID]) > 0 {
			t.Errorf("example %d: step %q is told to submit edgeId null but has outgoing edges", i, s.ID)
		}
	}
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(id string) bool {
		if seen[id] {
			return false
		}
		seen[id] = true
		if len(next[id]) == 0 {
			return true
		}
		for _, to := range next[id] {
			if walk(to) {
				return true
			}
		}
		return false
	}
	if !walk(d.StartStepID) {
		t.Errorf("example %d: no step without outgoing edges is reachable from the start, so the task cannot end", i)
	}
}
