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
	cliArg        = cli.Arg{Name: "cli", Description: "CLI name, for example gh, lark-cli"}
	agentSkillArg = cli.Arg{Name: "agentSkillId", Description: "Skill copy ID, from agent skill list"}
)

func addAgentParts(r *cli.Registry) {
	// 可用 CLI 与凭证
	r.Add(&cli.Command{
		Path:       []string{"agent", "cli", "add"},
		Summary:    "Add a CLI to the Agent's available list (unchanged if already there)",
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
		Summary:     "Remove the CLI permission and selection; shared project accounts are kept, private platform credentials are deleted, and existing tasks keep the permissions they started with",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{agentArg, cliArg},
		Output:      "null",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodDelete, nil, "agents", id, "clis", inv.Args[1])
		}),
	})
	credFlag := cli.Flag{Name: "from-file", Type: cli.TypePathOrStdin, Required: true, Description: "Credential file containing a JSON object {\"name\":\"value\"}; - means standard input. Credentials are not accepted as command-line arguments"}
	r.Add(&cli.Command{
		Path:       []string{"agent", "credential", "set-cli"},
		Summary:    "Platform Agents only: enter or overwrite private credentials according to the CLI template; for project Agents use project cli-account. Keys in the file are the template field names (see cli-template list). The output is metadata only",
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
		Summary:     "Platform Agents only: delete private CLI credentials (the permission stays); for project Agents use agent credential-selection clear",
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
		Summary:    "Platform Agents only: enter a model API key for a provider; for project Agents use project model-account. The file is {\"provider\":\"anthropic\",\"key\":\"...\",\"expectedConfigVersion\":1}; the version can be omitted. For authentication methods see authMethods in model providers; private platform OAuth is console only, and project OAuth uses project model-account login. The output is metadata only",
		Identities: externalOnly,
		Network:    true,
		Stdin:      true,
		Args:       []cli.Arg{agentArg},
		Flags:      []cli.Flag{{Name: "from-file", Type: cli.TypePathOrStdin, Required: true, Description: "Model API key request JSON file; - means standard input. Only provider/key/optional expectedConfigVersion; environment variables and OAuth credentials are not accepted"}},
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
		Summary:     "Platform Agents only: clear the private model account; for project Agents use agent credential-selection clear; existing tasks keep the version they started with",
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
		Summary:    "List the Agent's Skill copies",
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
		Summary:    "Show a Skill copy's file list and SKILL.md content",
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
		Summary:    "Copy a template from the Skills library to the Agent",
		Identities: externalOnly,
		Network:    true,
		Args:       []cli.Arg{agentArg},
		Flags:      []cli.Flag{{Name: "template", Type: cli.TypeString, Required: true, Description: "Template ID in the Skills library, from skill list"}},
		Output:     "AgentSkill",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodPost, map[string]string{"templateId": inv.String("template")}, "agents", id, "skills")
		}),
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "skill", "edit"},
		Summary:    "Edit a Skill copy's SKILL.md (only this file)",
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
		Summary:     "Overwrite the Skill copy entirely with the current content of its source template",
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
		Summary:     "Remove a Skill copy from the Agent",
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
	return cli.Flag{Name: "skill-md", Type: cli.TypePathOrStdin, Required: required, Description: "SKILL.md file; - means standard input"}
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
