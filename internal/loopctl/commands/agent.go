package commands

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"loop-now/internal/loopctl/cli"
)

func addCatalog(r *cli.Registry) {
	r.Add(&cli.Command{
		Path:       []string{"model", "providers"},
		Summary:    "列出固定版本 Pi 自带模型目录中的提供方",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Output:     "ModelProviderList",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "model-providers")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"model", "list"},
		Summary:    "列出某个提供方下的模型",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{{Name: "provider", Description: "提供方 ID，取自 model providers"}},
		Output:     "ModelList",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "model-providers", inv.Args[0], "models")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"cli-template", "list"},
		Summary:    "列出内置 CLI 模板：凭证字段、注入方式与安装状态",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Output:     "{templates: CliTemplate[]}",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "cli-templates")
		},
	})
}

// withAgent 先把第一个位置参数解析为 Agent ID，再执行 fn。
func withAgent(fn func(ctx context.Context, inv *cli.Invocation, id string) (any, error)) cli.RunFunc {
	return func(ctx context.Context, inv *cli.Invocation) (any, error) {
		id, err := agentID(ctx, inv, inv.Args[0])
		if err != nil {
			return nil, err
		}
		return fn(ctx, inv, id)
	}
}

// agentFields 组装创建与修改 Agent 的请求体。
func agentFields(inv *cli.Invocation) (map[string]any, error) {
	body := map[string]any{}
	if inv.Has("name") {
		body["name"] = inv.String("name")
	}
	if inv.Has("provider") {
		body["model"] = map[string]string{"provider": inv.String("provider"), "modelId": inv.String("model")}
	}
	if inv.Has("prompt-file") {
		b, err := readInput(inv, "prompt-file")
		if err != nil {
			return nil, err
		}
		body["prompt"] = string(b)
	}
	return body, nil
}

func addAgent(r *cli.Registry) {
	r.Add(&cli.Command{
		Path:        []string{"agent", "list"},
		Summary:     "列出一个项目或平台中未删除的 Agent；必须指定 --project 或 --platform",
		Identities:  externalOnly,
		Network:     true,
		Retry:       true,
		Flags:       scopeFlags,
		Constraints: []cli.Constraint{scopeRequired},
		Output:      "AgentList {agents: AgentListItem[]}，每项带 scope、projectId、isManager",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			path, err := scopePath(ctx, inv)
			if err != nil {
				return nil, err
			}
			return get(ctx, inv, nil, path...)
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "get"},
		Summary:    "查看 Agent 的配置详情（凭证只有元数据）",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{agentArg},
		Output:     "Agent",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return get(ctx, inv, nil, "agents", id)
		}),
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "create"},
		Summary:    "在一个项目或平台中创建常态 Agent；必须指定 --project 或 --platform，建好后不能移动",
		Identities: externalOnly,
		Network:    true,
		Stdin:      true,
		Flags: []cli.Flag{
			scopeFlags[0], scopeFlags[1],
			{Name: "name", Type: cli.TypeString, Required: true, Description: "Agent 名称，在项目内（平台 Agent 在平台内）唯一；不能含 / 或 #，不能以 @ 开头，不能是 UUID 格式"},
			{Name: "provider", Type: cli.TypeString, Required: true, Description: "模型提供方 ID，取自 model providers"},
			{Name: "model", Type: cli.TypeString, Required: true, Description: "模型 ID，取自 model list <provider>"},
			{Name: "prompt-file", Type: cli.TypePathOrStdin, Description: "从文件读取 Prompt，- 表示标准输入"},
		},
		Constraints: []cli.Constraint{scopeRequired},
		Output:      "Agent",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			body, err := agentFields(inv)
			if err != nil {
				return nil, err
			}
			path, err := scopePath(ctx, inv)
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPost, body, path...)
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "update"},
		Summary:    "修改 Agent 的名称、模型或 Prompt；含 Prompt（包括清空/同文本）须显式提供 agent get 的 promptVersion，版本冲突退出 8，不自动读取新版本重试；创建及仅改名称/模型不受此条件影响。下一派发生效",
		Identities: externalOnly,
		Network:    true,
		Stdin:      true,
		Args:       []cli.Arg{agentArg},
		Flags: []cli.Flag{
			{Name: "name", Type: cli.TypeString, Description: "新名称"},
			{Name: "provider", Type: cli.TypeString, Description: "模型提供方 ID，与 --model 一起提供"},
			{Name: "model", Type: cli.TypeString, Description: "模型 ID，与 --provider 一起提供"},
			{Name: "prompt-file", Type: cli.TypePathOrStdin, Description: "从文件读取新的 Prompt，- 表示标准输入；空文件清空，也须提供 --expected-prompt-version"},
			{Name: "expected-prompt-version", Type: cli.TypeInt, Min: intp(1), Description: "编辑所依据的 agent get.promptVersion 正整数；与 --prompt-file 同时提供，不使用 configVersion"},
		},
		Constraints: []cli.Constraint{
			{Kind: cli.AtLeastOne, Flags: []string{"name", "provider", "prompt-file"}},
			{Kind: cli.Together, Flags: []string{"provider", "model"}},
			{Kind: cli.Together, Flags: []string{"prompt-file", "expected-prompt-version"}, Hint: "先用 agent get 查看 prompt 与 promptVersion，核对编辑基线后显式提交；冲突时保留本地草稿，不自动覆盖"},
		},
		Output: "Agent",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			body, err := agentFields(inv)
			if err != nil {
				return nil, err
			}
			if inv.Has("prompt-file") {
				body["expectedPromptVersion"] = inv.Int("expected-prompt-version")
			}
			return send(ctx, inv, http.MethodPatch, body, "agents", id)
		}),
	})
	r.Add(&cli.Command{
		Path:        []string{"agent", "delete"},
		Summary:     "软删除空闲 Agent；项目共享账号保留，平台私有凭证删除；忙闲与任务约束由服务端验证",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{agentArg},
		Output:      "null",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodDelete, nil, "agents", id)
		}),
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "status"},
		Summary:    "查看常态 Agent 状态；Agent 仅可只读查看同项目，支持 UUID、短名或完整地址，不接受 #任务地址",
		Identities: both,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{agentArg},
		Output:     "AgentStatus",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			if inv.Identity == cli.IdentityAgent {
				return get(ctx, inv, url.Values{"agent": {inv.Args[0]}}, "agents", "status")
			}
			return withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
				return get(ctx, inv, nil, "agents", id, "status")
			})(ctx, inv)
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "history"},
		Summary:    "分页查看 Agent 的对话历史，每页按时间正序，包括各条回复的执行状态；不指定位置时返回最新一页",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{agentArg},
		Flags: []cli.Flag{
			{Name: "before", Type: cli.TypeString, Description: "返回这条消息之前的消息（向前翻页）"},
			{Name: "after", Type: cli.TypeString, Description: "返回这条消息之后的消息（向后翻页）"},
			{Name: "around-run", Type: cli.TypeString, Description: "返回以这次执行（回复）的用户消息为中心的一页，执行 ID 取自 anomaly list 或历史中的 run.id"},
			{Name: "limit", Type: cli.TypeInt, Min: intp(1), Description: "每页条数；默认值与上限由服务端确定"},
		},
		Constraints: []cli.Constraint{{Kind: cli.AtMostOne, Flags: []string{"before", "after", "around-run"}}},
		Output:      "MessagePage（hasMore 表示之前还有更早的消息，hasMoreAfter 表示之后还有更新的消息）",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			q := url.Values{}
			for flag, param := range map[string]string{"before": "before", "after": "after", "around-run": "aroundRun"} {
				if inv.Has(flag) {
					q.Set(param, inv.String(flag))
				}
			}
			if inv.Has("limit") {
				q.Set("limit", strconv.Itoa(inv.Int("limit")))
			}
			return get(ctx, inv, q, "agents", id, "messages")
		}),
	})
	r.Add(&cli.Command{
		Path:       []string{"agent", "timeline"},
		Summary:    "查看一条回复的运行记录时间线：入队、派发、启动各阶段、工具调用、每次模型调用的用量、结束状态与原因",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{agentArg, {Name: "runId", Description: "执行（回复）ID，取自 agent history 中的 run.id，或 anomaly list 中的 runId"}},
		Output:     "RunTimeline",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return get(ctx, inv, nil, "agents", id, "runs", inv.Args[1], "timeline")
		}),
	})
	r.Add(&cli.Command{
		Path:        []string{"agent", "session", "reset"},
		Summary:     "确认重开 Agent 的会话（用于会话丢失之后）；Agent 正忙时服务端拒绝",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{agentArg},
		Output:      "SessionInfo",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return send(ctx, inv, http.MethodPost, nil, "agents", id, "session", "reset")
		}),
	})
}
