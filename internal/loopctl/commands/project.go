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
	projectArg     = cli.Arg{Name: "project", Description: "Project name or project ID, from project list"}
	projectNameArg = cli.Arg{Name: "name", Description: "Project name, 1 to 100 characters; cannot contain / or #, cannot start with @, and cannot be in UUID format"}
	scopeFlags     = []cli.Flag{
		{Name: "project", Type: cli.TypeString, Description: "Project name or project ID, from project list"},
		{Name: "platform", Type: cli.TypeBool, Description: "Agents held by the platform (belonging to no project; the address is the name)"},
	}
	// 列出与创建 Agent 必须指定范围：Agent 的归属不能更改，不把默认项目当作隐含的范围。
	scopeRequired = cli.Constraint{Kind: cli.ExactlyOne, Flags: []string{"project", "platform"}, Hint: "用 loopctl project list 查看项目"}
	// 用量与异常的范围是筛选条件，都不指定时为全部。
	scopeOptional = cli.Constraint{Kind: cli.AtMostOne, Flags: []string{"project", "platform"}}
)

func addProject(r *cli.Registry) {
	r.Add(&cli.Command{
		Path:       []string{"project", "list"},
		Summary:    "For platform Agents, discover projects and their current Manager; for external identities, the project list (default project first)",
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
		Summary:    "Show a project: whether it is the default project, its Manager and its number of Agents",
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
		Summary:    "Create a project in the default organization; the name is unique among undeleted projects, and a duplicate returns name_conflict",
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
		Summary:    "Rename a project; the addresses of its Agents change with it, and it can be renamed even while Agents are busy",
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
		Summary:     "Soft-delete a project: all its Agents must be idle, otherwise agent_not_idle; the default project cannot be deleted. Its Agents are handled as deleted Agents, the Manager is removed, and the name can be reused",
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
		Summary:    "Assign or change the project Manager; it must be an undeleted Agent in this project",
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
		Summary:    "Remove the project Manager; succeeds even when there was none",
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
