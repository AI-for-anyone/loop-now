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
)

var update = flag.Bool("update", false, "更新 golden 文件")

const goldenVersion = "golden-version"

func render(t *testing.T) string {
	t.Helper()
	b, err := Render(goldenVersion)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 同一版本的内容不受时区与环境影响，逐字节相同。
func TestDeterministic(t *testing.T) {
	a := render(t)
	t.Setenv("TZ", "Pacific/Kiritimati")
	t.Setenv("LANG", "C")
	t.Setenv("LOOPCTL_AGENT_TOKEN", "lna_x")
	if b := render(t); a != b {
		t.Fatal("两次生成的内容不同")
	}
	if !bytes.Equal(Files()["SKILL.md"], Files()["SKILL.md"]) || len(Files()) != 1 {
		t.Fatal("Files 应只有一个稳定的 SKILL.md")
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
		t.Fatalf("读取 golden 文件：%v（用 -update 生成）", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("SKILL.md 与 %s 不一致；确认改动后用 go test -run TestGolden -update 更新", p)
	}
}

func TestFrontmatterPassesPlatformValidation(t *testing.T) {
	meta, err := content.ParseSkillMarkdown(Files()["SKILL.md"])
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != SkillName {
		t.Fatalf("名称为 %q", meta.Name)
	}
}

var (
	fence  = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
	inline = regexp.MustCompile("`([^`\n]+)`")
)

// commandLines 取出说明中每一条以 loopctl 开头的命令：代码块中的行与行内代码；去掉 heredoc。
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

// shellWords 按 shell 规则切分单引号与双引号。
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

// 说明中的每条命令都能被同一版本以 Agent 身份解析；每条 Agent 命令都写进了说明；外部专用命令一条也不出现。
func TestCommandsInGuide(t *testing.T) {
	md := string(Files()["SKILL.md"])
	r := commands.Registry()
	lines := commandLines(md)
	if len(lines) < 10 {
		t.Fatalf("只取到 %d 条命令：%v", len(lines), lines)
	}
	seen := map[string]bool{}
	for _, l := range lines {
		argv := shellWords(l)[1:]
		if e := cli.Check(r, argv, cli.IdentityAgent); e != nil {
			t.Errorf("说明中的命令 %q 无法以 Agent 身份解析：%v", l, e)
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
			t.Errorf("Agent 命令 %s 没有写进说明", c.Name())
		}
		if !c.AllowedFor(cli.IdentityAgent) && mentioned {
			t.Errorf("说明中出现了外部身份专用的命令 %s", c.Name())
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

// 模板引用不存在或 Agent 不能用的命令时，生成失败。
func TestTemplateReferenceChecked(t *testing.T) {
	orig := skillTemplate
	t.Cleanup(func() { skillTemplate = orig })
	for _, ref := range []string{`{{cmd "inbox archive"}}`, `{{cmd "agent list"}}`} {
		skillTemplate = orig + ref
		if _, err := Render("x"); err == nil {
			t.Errorf("引用 %s 时应生成失败", ref)
		}
	}
}

func TestShellWords(t *testing.T) {
	got := shellWords(`loopctl inbox send @console --text "构建 完成" --x 'a b' -`)
	want := []string{"loopctl", "inbox", "send", "@console", "--text", "构建 完成", "--x", "a b", "-"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
}

// 使用说明中的完整JSON示例与正式API一致，防止Manager照着Skill得到无效流程。
func TestTaskJSONExamplesMatchContract(t *testing.T) {
	doc := clitest.Load(t, "agent-openapi.yaml")
	md := strings.ReplaceAll(render(t), "<新动作UUID>", "0192f1a2-0000-7000-8000-000000000053")
	md = strings.ReplaceAll(md, "<执行AgentID>", "0192f1a2-0000-7000-8000-000000000001")
	blocks := regexp.MustCompile("(?s)```json\\n(.*?)```").FindAllStringSubmatch(md, -1)
	if len(blocks) != 2 {
		t.Fatalf("expected create and submit JSON examples, got %d", len(blocks))
	}
	for i, name := range []string{"TaskCreateInput", "TaskSubmitInput"} {
		var body any
		if err := json.Unmarshal([]byte(blocks[i][1]), &body); err != nil {
			t.Fatal(err)
		}
		if err := doc.Components.Schemas[name].Value.VisitJSON(body, openapi3.EnableJSONSchema2020()); err != nil {
			t.Fatalf("%s example violates contract: %v", name, err)
		}
	}
}
