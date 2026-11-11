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
		Summary:    "List the providers in the model catalog bundled with the pinned Pi version",
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
		Summary:    "List the models of a provider",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{{Name: "provider", Description: "Provider ID, from model providers"}},
		Output:     "ModelList",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "model-providers", inv.Args[0], "models")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"cli-template", "list"},
		Summary:    "List the built-in CLI templates: credential fields, injection method and install status",
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
		Summary:     "List the undeleted Agents of a project or of the platform; --project or --platform is required",
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
		Summary:    "Show an Agent's configuration details (credentials as metadata only)",
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
		Summary:    "Create a regular Agent in a project or on the platform; --project or --platform is required, and it cannot be moved after creation",
		Identities: externalOnly,
		Network:    true,
		Stdin:      true,
		Flags: []cli.Flag{
			scopeFlags[0], scopeFlags[1],
			{Name: "name", Type: cli.TypeString, Required: true, Description: "Agent name, unique within the project (within the platform for platform Agents); cannot contain / or #, cannot start with @, and cannot be in UUID format"},
			{Name: "provider", Type: cli.TypeString, Required: true, Description: "Model provider ID, from model providers"},
			{Name: "model", Type: cli.TypeString, Required: true, Description: "Model ID, from model list <provider>"},
			{Name: "prompt-file", Type: cli.TypePathOrStdin, Description: "Read the Prompt from a file; - means standard input"},
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
		Summary:    "Change an Agent's name, model or Prompt; a change that includes the Prompt (including clearing it or the same text) must explicitly give the promptVersion from agent get, a version conflict exits with 8, and it does not read the new version and retry automatically; creating, or changing only the name/model, is not subject to this. Takes effect at the next dispatch",
		Identities: externalOnly,
		Network:    true,
		Stdin:      true,
		Args:       []cli.Arg{agentArg},
		Flags: []cli.Flag{
			{Name: "name", Type: cli.TypeString, Description: "New name"},
			{Name: "provider", Type: cli.TypeString, Description: "Model provider ID; give it together with --model"},
			{Name: "model", Type: cli.TypeString, Description: "Model ID; give it together with --provider"},
			{Name: "prompt-file", Type: cli.TypePathOrStdin, Description: "Read the new Prompt from a file; - means standard input; an empty file clears it, and --expected-prompt-version is still required"},
			{Name: "expected-prompt-version", Type: cli.TypeInt, Min: intp(1), Description: "The positive integer agent get.promptVersion the edit is based on; give it together with --prompt-file; configVersion is not used"},
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
		Summary:     "Soft-delete an idle Agent; shared project accounts are kept and private platform credentials are deleted; busy and task constraints are verified by the server",
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
		Summary:    "Show a regular Agent's status; an Agent can only view, read-only, Agents in the same project; accepts a UUID, a short name or a full address, but not a #task address",
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
		Summary:    "Page through an Agent's conversation history, each page in chronological order, including the execution status of each reply; without a position, returns the latest page",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{agentArg},
		Flags: []cli.Flag{
			{Name: "before", Type: cli.TypeString, Description: "Return the messages before this message (page backward)"},
			{Name: "after", Type: cli.TypeString, Description: "Return the messages after this message (page forward)"},
			{Name: "around-run", Type: cli.TypeString, Description: "Return a page centered on the user message of this execution (reply); the execution ID comes from anomaly list or run.id in the history"},
			{Name: "limit", Type: cli.TypeInt, Min: intp(1), Description: "Items per page; the default and maximum are set by the server"},
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
		Summary:    "Show the run record timeline of a reply: queuing, dispatch, each startup phase, tool calls, usage of each model call, and the final status and reason",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{agentArg, {Name: "runId", Description: "Execution (reply) ID, from run.id in agent history or runId in anomaly list"}},
		Output:     "RunTimeline",
		Run: withAgent(func(ctx context.Context, inv *cli.Invocation, id string) (any, error) {
			return get(ctx, inv, nil, "agents", id, "runs", inv.Args[1], "timeline")
		}),
	})
	r.Add(&cli.Command{
		Path:        []string{"agent", "session", "reset"},
		Summary:     "Confirm reopening an Agent's session (after the session was lost); the server refuses while the Agent is busy",
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
