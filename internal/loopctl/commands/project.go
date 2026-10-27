package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

var (
	projectArg     = cli.Arg{Name: "project", Description: "项目名或项目 ID，取自 project list"}
	projectNameArg = cli.Arg{Name: "name", Description: "项目名，1 到 100 个字符；不能含 / 或 #，不能以 @ 开头，不能是 UUID 格式"}
	scopeFlags     = []cli.Flag{
		{Name: "project", Type: cli.TypeString, Description: "项目名或项目 ID，取自 project list"},
		{Name: "platform", Type: cli.TypeBool, Description: "平台持有的 Agent（不属于任何项目，地址就是名称）"},
	}
	// 列出与创建 Agent 必须指定范围：Agent 的归属不能更改，不把默认项目当作隐含的范围。
	scopeRequired = cli.Constraint{Kind: cli.ExactlyOne, Flags: []string{"project", "platform"}, Hint: "用 loopctl project list 查看项目"}
	// 用量与异常的范围是筛选条件，都不指定时为全部。
	scopeOptional = cli.Constraint{Kind: cli.AtMostOne, Flags: []string{"project", "platform"}}
)

func addProject(r *cli.Registry) {
	r.Add(&cli.Command{
		Path:       []string{"project", "list"},
		Summary:    "平台 Agent 发现项目与当前 Manager；外部身份沿用项目列表（默认项目在前）",
		Identities: both,
		Network:    true,
		Retry:      true,
		Output:     "Agent: PlatformProjectDirectory；external: {projects: Project[]}",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "projects")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"project", "get"},
		Summary:    "查看一个项目：是否为默认项目、Manager 与 Agent 数量",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{projectArg},
		Output:     "Project",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			id, err := projectID(ctx, inv, inv.Args[0])
			if err != nil {
				return nil, err
			}
			return get(ctx, inv, nil, "projects", id)
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"project", "create"},
		Summary:    "在默认组织下新建项目；项目名在未删除的项目中唯一，重名返回 name_conflict",
		Identities: externalOnly,
		Network:    true,
		Args:       []cli.Arg{projectNameArg},
		Output:     "Project",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return send(ctx, inv, http.MethodPost, map[string]any{"name": inv.Args[0]}, "projects")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"project", "rename"},
		Summary:    "给项目改名；项目内 Agent 的地址随之改变，Agent 忙时也可以改名",
		Identities: externalOnly,
		Network:    true,
		Args:       []cli.Arg{projectArg, projectNameArg},
		Output:     "Project",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			id, err := projectID(ctx, inv, inv.Args[0])
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPatch, map[string]any{"name": inv.Args[1]}, "projects", id)
		},
	})
	r.Add(&cli.Command{
		Path:        []string{"project", "delete"},
		Summary:     "软删除项目：项目内 Agent 都须空闲，否则 agent_not_idle；默认项目不能删除。项目内 Agent 按删除 Agent 处理，Manager 取消，名称可以再用",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{projectArg},
		Output:      "null",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			id, err := projectID(ctx, inv, inv.Args[0])
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodDelete, nil, "projects", id)
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"project", "manager", "set"},
		Summary:    "指定或更换项目 Manager；必须是本项目内未删除的 Agent",
		Identities: externalOnly,
		Network:    true,
		Args:       []cli.Arg{projectArg, agentArg},
		Output:     "Project",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			id, err := projectID(ctx, inv, inv.Args[0])
			if err != nil {
				return nil, err
			}
			agent, err := agentID(ctx, inv, inv.Args[1])
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPut, map[string]any{"agentId": agent}, "projects", id, "manager")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"project", "manager", "clear"},
		Summary:    "取消项目 Manager；原本没有 Manager 时也成功",
		Identities: externalOnly,
		Network:    true,
		Args:       []cli.Arg{projectArg},
		Output:     "Project",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			id, err := projectID(ctx, inv, inv.Args[0])
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodDelete, nil, "projects", id, "manager")
		},
	})
}

// projectID 把项目名或 ID 解析为项目 ID：UUID 直接使用（项目名不能是 UUID 格式），其余在未删除的项目中按名称完全匹配。
func projectID(ctx context.Context, inv *cli.Invocation, ref string) (string, error) {
	if IsUUID(ref) {
		return ref, nil
	}
	resp, e := inv.Client.Do(ctx, conn.Request{Method: http.MethodGet, Path: []string{"projects"}, Retry: true})
	if e != nil {
		return "", e
	}
	var body struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(resp.Data, &body); err != nil {
		return "", output.Errorf(output.CodeUnexpectedResponse, "项目列表的响应无法解析")
	}
	for _, p := range body.Projects {
		if p.Name == ref {
			return p.ID, nil
		}
	}
	return "", output.Errorf(output.CodeNotFound, "没有名为 %q 的项目，用 loopctl project list 查看项目", ref)
}

// scopePath 返回列出与创建 Agent 的路径：/projects/{projectId}/agents 或 /platform/agents。
func scopePath(ctx context.Context, inv *cli.Invocation) ([]string, error) {
	if inv.Bool("platform") {
		return []string{"platform", "agents"}, nil
	}
	id, err := projectID(ctx, inv, inv.String("project"))
	if err != nil {
		return nil, err
	}
	return []string{"projects", id, "agents"}, nil
}

// scopeQuery 把可选的范围加入用量与异常的查询参数。
func scopeQuery(ctx context.Context, inv *cli.Invocation, q url.Values) error {
	if inv.Bool("platform") {
		q.Set("platform", "true")
	}
	if inv.Has("project") {
		id, err := projectID(ctx, inv, inv.String("project"))
		if err != nil {
			return err
		}
		q.Set("projectId", id)
	}
	return nil
}
