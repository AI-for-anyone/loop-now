// Package commands 定义 loopctl 的全部命令。命令定义是解析器、schema、--help 与内置 Skill 的唯一来源。
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

var (
	both         = []cli.Identity{cli.IdentityAgent, cli.IdentityExternal}
	externalOnly = []cli.Identity{cli.IdentityExternal}
)

// Registry 返回 loopctl 的完整命令表。
func Registry() *cli.Registry {
	r := cli.NewRegistry()
	r.Group("config", "Local connection configuration of an external identity (~/.config/loopctl/config.json)")
	r.Group("inbox", "Inbox messages: list summaries, read one by one, send, reply")
	r.Group("model", "Pi model catalog")
	r.Group("cli-template", "Built-in CLI template catalog")
	r.Group("project", "Create, rename and delete projects; their Manager and shared accounts")
	r.Group("project manager", "Assign and remove the project Manager")
	r.Group("agent", "Manage Agents in projects and Agents held by the platform")
	r.Group("agent session", "Agent sessions")
	r.Group("agent source", "Sources of an Agent's working folder (code repositories or uploaded material)")
	r.Group("agent file", "Files in an uploaded-material source")
	r.Group("agent cli", "CLIs available to an Agent")
	r.Group("agent credential", "An Agent's CLI credentials and model account credentials (write-only)")
	r.Group("agent skill", "An Agent's Skill copies")
	r.Group("skill", "Skills template library")
	r.Group("usage", "Token usage statistics")
	r.Group("anomaly", "Failed and interrupted replies")
	r.Group("task", "Task queries, creation and node actions for in-platform Agents; permissions are verified by the server")
	r.Group("task artifact", "Explicitly save and download task file artifacts")
	r.Group("plugin", "Plugins: install, update, uninstall and list (external identities); validate and pack plugin directories locally")
	addLocal(r)
	addInbox(r)
	addCatalog(r)
	addProject(r)
	addProjectCredentials(r)
	addCLIAuth(r)
	addDBConnections(r)
	addAgent(r)
	addAgentParts(r)
	addSkill(r)
	addOps(r)
	addTasks(r)
	addSleep(r)
	addPlugins(r)
	return r
}

// agentArg 是引用 Agent 的位置参数。
var agentArg = cli.Arg{Name: "agent", Description: "The Agent's ID, or its address: organization/project/Agent, or project/Agent within the same organization; for an Agent held by the platform, just its name"}

func intp(n int) *int { return &n }

// call 发出请求并返回服务端的响应体，作为命令的 data。
func call(ctx context.Context, inv *cli.Invocation, r conn.Request) (any, error) {
	resp, e := inv.Client.Do(ctx, r)
	if e != nil {
		return nil, e
	}
	return resp.Data, nil
}

// get 发出可以重试的 GET 请求。
func get(ctx context.Context, inv *cli.Invocation, query url.Values, path ...string) (any, error) {
	return call(ctx, inv, conn.Request{Method: http.MethodGet, Path: path, Query: query, Retry: true})
}

// send 发出不重试的写请求；body 为空时不带请求体。
func send(ctx context.Context, inv *cli.Invocation, method string, body any, path ...string) (any, error) {
	r := conn.Request{Method: method, Path: path}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, output.Errorf(output.CodeInternalError, "无法编码请求体：%v", err)
		}
		r.Body, r.ContentType = b, "application/json"
	}
	return call(ctx, inv, r)
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID 判断是否为 8-4-4-4-12 的十六进制 UUID 写法。
func IsUUID(s string) bool { return uuidPattern.MatchString(s) }

// agentID 把 <agent> 参数解析为 Agent ID：UUID 直接使用，其余按地址调用 GET /agents?address= 查找。
func agentID(ctx context.Context, inv *cli.Invocation, ref string) (string, error) {
	if IsUUID(ref) {
		return ref, nil
	}
	resp, e := inv.Client.Do(ctx, conn.Request{Method: http.MethodGet, Path: []string{"agents"}, Query: url.Values{"address": {ref}}, Retry: true})
	if e != nil {
		return "", e
	}
	var body struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(resp.Data, &body); err != nil {
		return "", output.Errorf(output.CodeUnexpectedResponse, "按地址查找 Agent 的响应无法解析")
	}
	switch len(body.Agents) {
	case 0:
		return "", output.Errorf(output.CodeNotFound, "找不到地址为 %q 的 Agent", ref)
	case 1:
		return body.Agents[0].ID, nil
	default:
		return "", output.Errorf(output.CodeUnexpectedResponse, "地址 %q 对应了 %d 个 Agent", ref, len(body.Agents))
	}
}

// readInput 读取 path-or-stdin 选项指向的内容：- 表示标准输入。
func readInput(inv *cli.Invocation, flag string) ([]byte, error) {
	v := inv.String(flag)
	if v == "-" {
		b, err := io.ReadAll(inv.Sys.Stdin)
		if err != nil {
			return nil, output.Errorf(output.CodeInputUnreadable, "无法读取标准输入：%v", err)
		}
		return b, nil
	}
	return readFile(v)
}

func readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, output.Errorf(output.CodeInputUnreadable, "无法读取文件 %s：%v", path, cause(err))
	}
	return b, nil
}

func cause(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// text 读取 --text 或 --text-file 提供的正文，正文不能为空。
func text(inv *cli.Invocation) (string, error) {
	var s string
	if inv.Has("text") {
		s = inv.String("text")
	} else {
		b, err := readInput(inv, "text-file")
		if err != nil {
			return "", err
		}
		s = string(b)
	}
	if strings.TrimSpace(s) == "" {
		return "", output.Errorf(output.CodeInvalidArgument, "正文不能为空")
	}
	return s, nil
}
