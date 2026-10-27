package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/output"
)

var (
	cliArg        = cli.Arg{Name: "cli", Description: "CLI 名称，例如 gh、lark-cli"}
	agentSkillArg = cli.Arg{Name: "agentSkillId", Description: "Skill 副本 ID，取自 agent skill list"}
)

func addAgentParts(r *cli.Registry) {
	// 可用 CLI 与凭证
	r.Add(&cli.Command{
		Path:       []string{"agent", "cli", "add"},
		Summary:    "把 CLI 加入 Agent 的可用列表（已存在时不变）",
		Identities: externalOnly,
		Network:    true,
		Args:       []cli.Arg{agentArg, cliArg},
		Output:     "AgentCli",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodPut, nil, "agents", id, "clis", inv.Args[1])
		}),
	})
	r.Add(&cli.Command{
		Path:        []string{"agent", "cli", "remove"},
		Summary:     "移除 CLI 权限与选择；项目共享账号保留，平台私有凭证删除，已有任务保持启动权限",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{agentArg, cliArg},
		Output:      "null",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodDelete, nil, "agents", id, "clis", inv.Args[1])
		}),
	})
	credFlag := cli.Flag{Name: "from-file", Type: cli.TypePathOrStdin, Required: true, Description: "凭证文件，内容为 JSON 对象 {\"名称\":\"值\"}；- 表示标准输入。凭证不接受命令行参数"}
	r.Add(&cli.Command{
		Path:       []string{"agent", "credential", "set-cli"},
		Summary:    "仅平台 Agent：按 CLI 模板录入或覆盖私有凭证；项目 Agent 请用 project cli-account。文件中的键为模板字段名（见 cli-template list）。输出只有元数据",
		Identities: externalOnly,
		Network:    true,
		Stdin:      true,
		Args:       []cli.Arg{agentArg, cliArg},
		Flags:      []cli.Flag{credFlag},
		Output:     "CredentialMeta",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			m, err := credentialFile(inv)
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPut, map[string]any{"fields": m}, "agents", id, "clis", inv.Args[1], "credential")
		}),
	})
	r.Add(&cli.Command{
		Path:        []string{"agent", "credential", "clear-cli"},
		Summary:     "仅平台 Agent：删除私有 CLI 凭证（权限保留）；项目 Agent 请用 agent credential-selection clear",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{agentArg, cliArg},
		Output:      "null",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodDelete, nil, "agents", id, "clis", inv.Args[1], "credential")
		}),
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "credential", "set-model"},
		Summary:    "仅平台 Agent：按提供方录入模型 API Key；项目 Agent 请用 project model-account。文件为 {\"provider\":\"anthropic\",\"key\":\"...\",\"expectedConfigVersion\":1}，版本可省略。认证方式见 model providers 的 authMethods；平台私有 OAuth 仅控制台，项目 OAuth 用 project model-account login。输出只有元数据",
		Identities: externalOnly,
		Network:    true,
		Stdin:      true,
		Args:       []cli.Arg{agentArg},
		Flags:      []cli.Flag{{Name: "from-file", Type: cli.TypePathOrStdin, Required: true, Description: "模型 API Key 请求 JSON 文件；- 表示标准输入。仅 provider/key/可选 expectedConfigVersion，不接受环境变量或 OAuth 凭证"}},
		Output:     "CredentialMeta",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			m, err := modelCredentialFile(inv)
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPut, m, "agents", id, "model-credential")
		}),
	})
	r.Add(&cli.Command{
		Path:        []string{"agent", "credential", "clear-model"},
		Summary:     "仅平台 Agent：清除私有模型账号；项目 Agent 请用 agent credential-selection clear；已有任务保持启动版本",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{agentArg},
		Output:      "null",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodDelete, nil, "agents", id, "model-credential")
		}),
	})

	// Skill 副本
	r.Add(&cli.Command{
		Path:       []string{"agent", "skill", "list"},
		Summary:    "列出 Agent 的 Skill 副本",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{agentArg},
		Output:     "{skills: AgentSkillSummary[]}",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return get(ctx, inv, nil, "agents", id, "skills")
		}),
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "skill", "get"},
		Summary:    "查看 Skill 副本的文件列表与 SKILL.md 内容",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{agentArg, agentSkillArg},
		Output:     "AgentSkill",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return get(ctx, inv, nil, "agents", id, "skills", inv.Args[1])
		}),
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "skill", "add"},
		Summary:    "把 Skills 库中的模板复制给 Agent",
		Identities: externalOnly,
		Network:    true,
		Args:       []cli.Arg{agentArg},
		Flags:      []cli.Flag{{Name: "template", Type: cli.TypeString, Required: true, Description: "Skills 库中的模板 ID，取自 skill list"}},
		Output:     "AgentSkill",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodPost, map[string]string{"templateId": inv.String("template")}, "agents", id, "skills")
		}),
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "skill", "edit"},
		Summary:    "编辑 Skill 副本的 SKILL.md（只改这个文件）",
		Identities: externalOnly,
		Network:    true,
		Stdin:      true,
		Args:       []cli.Arg{agentArg, agentSkillArg},
		Flags:      []cli.Flag{skillMdFlag(true)},
		Output:     "AgentSkill",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			md, err := skillMarkdown(inv)
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPut, map[string]string{"content": md}, "agents", id, "skills", inv.Args[1], "skill-md")
		}),
	})
	r.Add(&cli.Command{
		Path:        []string{"agent", "skill", "recopy"},
		Summary:     "用来源模板的当前内容整体覆盖 Skill 副本",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{agentArg, agentSkillArg},
		Output:      "AgentSkill",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodPost, nil, "agents", id, "skills", inv.Args[1], "recopy")
		}),
	})
	r.Add(&cli.Command{
		Path:        []string{"agent", "skill", "remove"},
		Summary:     "从 Agent 上移除 Skill 副本",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{agentArg, agentSkillArg},
		Output:      "null",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodDelete, nil, "agents", id, "skills", inv.Args[1])
		}),
	})
}

// modelCredentialFile validates the formal API Key input without including secrets in errors.
func modelCredentialFile(inv *cli.Invocation) (json.RawMessage, error) {
	b, err := readInput(inv, "from-file")
	if err != nil {
		return nil, err
	}
	var in struct {
		Provider              *string `json:"provider"`
		Key                   *string `json:"key"`
		ExpectedConfigVersion *int64  `json:"expectedConfigVersion"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if !utf8.Valid(b) || dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF || in.Provider == nil || in.Key == nil || *in.Key == "" || (in.ExpectedConfigVersion != nil && *in.ExpectedConfigVersion < 1) {
		return nil, output.Errorf(output.CodeInvalidArgument, "模型凭证文件必须包含字符串 provider/key 及可选正整数 expectedConfigVersion；不接受环境变量或 OAuth 凭证")
	}
	// Explicit null is not an integer and is not part of the formal optional version field.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	for name := range fields {
		if name != "provider" && name != "key" && name != "expectedConfigVersion" {
			return nil, output.Errorf(output.CodeInvalidArgument, "模型凭证文件仅接受 provider/key/expectedConfigVersion 字段")
		}
	}
	if v, ok := fields["expectedConfigVersion"]; ok && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return nil, output.Errorf(output.CodeInvalidArgument, "expectedConfigVersion 必须为正整数或省略")
	}
	return json.RawMessage(b), nil
}

// credentialFile 读取凭证文件：JSON 对象，值为字符串。错误说明中不包含文件内容。
func credentialFile(inv *cli.Invocation) (map[string]string, error) {
	b, err := readInput(inv, "from-file")
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil || m == nil {
		return nil, output.Errorf(output.CodeInvalidArgument, "凭证文件必须是 JSON 对象，形如 {\"名称\":\"值\"}，值为字符串")
	}
	return m, nil
}

func skillMdFlag(required bool) cli.Flag {
	return cli.Flag{Name: "skill-md", Type: cli.TypePathOrStdin, Required: required, Description: "SKILL.md 文件，- 表示标准输入"}
}

func skillMarkdown(inv *cli.Invocation) (string, error) {
	b, err := readInput(inv, "skill-md")
	if err != nil {
		return "", err
	}
	if !utf8.Valid(b) {
		return "", output.Errorf(output.CodeInvalidArgument, "SKILL.md 必须是 UTF-8 文本")
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", output.Errorf(output.CodeInvalidArgument, "SKILL.md 不能为空")
	}
	return string(b), nil
}

// trackedWriter 记录写入错误，用于区分“写本地文件失败”与“读取响应中断”。
type trackedWriter struct {
	w   io.Writer
	err error
}

func (t *trackedWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if err != nil {
		t.err = err
	}
	return n, err
}
