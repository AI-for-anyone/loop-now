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
	r.Group("config", "外部身份的本地连接配置（~/.config/loopctl/config.json）")
	r.Group("inbox", "Inbox 消息：列出摘要、逐条读取、发送、回复")
	r.Group("model", "Pi 模型目录")
	r.Group("cli-template", "内置 CLI 模板目录")
	r.Group("project", "项目的新建、改名、删除、Manager 与共享账号")
	r.Group("project manager", "项目 Manager 的指定与取消")
	r.Group("agent", "管理项目中与平台持有的 Agent")
	r.Group("agent session", "Agent 会话")
	r.Group("agent source", "Agent 工作文件夹的来源（代码仓库或上传资料）")
	r.Group("agent file", "上传资料来源中的文件")
	r.Group("agent cli", "Agent 可用的 CLI")
	r.Group("agent credential", "Agent 的 CLI 凭证与模型账号凭证（只写不读）")
	r.Group("agent skill", "Agent 的 Skill 副本")
	r.Group("skill", "Skills 模板库")
	r.Group("usage", "token 用量统计")
	r.Group("anomaly", "失败与被打断的回复")
	r.Group("task", "平台内 Agent 的任务查询、创建与节点动作；权限由服务端验证")
	r.Group("task artifact", "显式保存与下载任务文件成果")
	r.Group("plugin", "插件：安装、更新、卸载与列出（外部身份）；在本机校验与打包插件目录")
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
var agentArg = cli.Arg{Name: "agent", Description: "Agent 的 ID，或地址：组织名/项目名/Agent名，同组织时可写 项目名/Agent名；平台持有的 Agent 直接写名称"}

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
