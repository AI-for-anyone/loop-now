package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

var (
	taskArg        = cli.Arg{Name: "taskId", Description: "任务 ID，取自 task create/get/current 的结果"}
	taskNodeArg    = cli.Arg{Name: "nodeRunId", Description: "节点运行 ID，取自当前任务；重试或回路后须使用新的 ID"}
	taskActionFlag = cli.Flag{Name: "action-id", Type: cli.TypeString, Required: true, Description: "本次动作的 UUID；人工重发同一动作时保持相同 ID 和内容，不得复用于其他动作"}
	taskInputFlag  = cli.Flag{Name: "from-file", Type: cli.TypePathOrStdin, Required: true, Description: "正式 TaskCreateInput/TaskSubmitInput JSON 文件；- 从标准输入读取，含调用者生成的 actionId"}
	taskRunFlag    = cli.Flag{Name: "run", Type: cli.TypeString, Description: "本轮平台prompt给出的准确运行UUID；专用恢复节点必传，不读最新run或自动重试"}
	taskRunIDFlag  = cli.Flag{Name: "run-id", Type: cli.TypeString, Description: "本轮准确运行UUID；与 --run 同义，二者不能同时提供；专用恢复节点必传"}
)

func addTasks(r *cli.Registry) {
	agentOnly := []cli.Identity{cli.IdentityAgent}
	add := func(c *cli.Command) {
		c.Identities = agentOnly
		c.Network = true
		r.Add(c)
	}
	add(&cli.Command{
		Path:    []string{"task", "list"},
		Summary: "当前项目 Manager 分页列出本项目任务",
		Retry:   true,
		Flags:   []cli.Flag{cursorFlag, limitFlag},
		Output:  "TaskPage",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, pageQuery(inv), "tasks")
		},
	})
	add(&cli.Command{
		Path:    []string{"task", "agents"},
		Summary: "当前项目 Manager 查看本项目可选 Agent 的安全目录",
		Retry:   true,
		Output:  "TaskAgentList",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "tasks", "agents")
		}})
	add(&cli.Command{
		Path:    []string{"task", "create"},
		Summary: "Manager 创建固定 workflow 的任务；超时可能已创建，先查询再决定是否重发",
		Stdin:   true,
		Flags:   []cli.Flag{taskInputFlag},
		Output:  "TaskDetail",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return taskJSON(ctx, inv, "tasks")
		}})
	add(&cli.Command{
		Path:    []string{"task", "get"},
		Summary: "查看有权访问的任务、节点记录、输出及已保存成果",
		Retry:   true,
		Args:    []cli.Arg{taskArg},
		Output:  "TaskDetail",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "tasks", inv.Args[0])
		}})
	add(&cli.Command{
		Path:    []string{"task", "history"},
		Summary: "分页查看指定节点运行的会话，原样保留执行状态与具体错误",
		Retry:   true,
		Args:    []cli.Arg{taskArg, taskNodeArg},
		Flags:   []cli.Flag{cursorFlag, limitFlag},
		Output:  "TaskMessagePage",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, pageQuery(inv), "tasks", inv.Args[0], "nodes", inv.Args[1], "messages")
		},
	})
	add(&cli.Command{
		Path:    []string{"task", "current"},
		Summary: "任务实例查看当前任务；常态身份没有当前任务",
		Retry:   true,
		Output:  "TaskDetail",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "tasks", "current")
		}})
	add(&cli.Command{
		Path:    []string{"task", "submit"},
		Summary: "提交当前节点输出与出口；保存成功后仍须正常结束当前轮，平台才推进",
		Stdin:   true,
		Args:    []cli.Arg{taskArg, taskNodeArg},
		Flags:   []cli.Flag{taskInputFlag, taskRunFlag, taskRunIDFlag},
		Output:  "TaskNodeRun",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return taskJSON(ctx, inv, "tasks", inv.Args[0], "nodes", inv.Args[1], "submit")
		}})
	add(&cli.Command{
		Path:    []string{"task", "fail"},
		Summary: "声明当前节点失败并保留原因，任务阻塞等待人或 Manager 处理",
		Stdin:   true,
		Args:    []cli.Arg{taskArg, taskNodeArg},
		Flags:   []cli.Flag{taskActionFlag, taskRunFlag, taskRunIDFlag, {Name: "reason-file", Type: cli.TypePathOrStdin, Required: true, Description: "失败原因 UTF-8 文件；- 从标准输入读取，不能为空"}},
		Output:  "TaskNodeRun",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			if err := taskRun(inv); err != nil {
				return nil, err
			}
			action, err := taskAction(inv)
			if err != nil {
				return nil, err
			}
			reason, err := taskText(inv, "reason-file")
			if err != nil {
				return nil, err
			}
			body := map[string]any{"actionId": action, "reason": reason}
			if id, present := explicitTaskRun(inv); present {
				body["runId"] = id
			}
			return send(ctx, inv, http.MethodPost, body, "tasks", inv.Args[0], "nodes", inv.Args[1], "fail")
		}})
	add(&cli.Command{
		Path:        []string{"task", "retry"},
		Summary:     "当前项目 Manager 重试阻塞节点；沿用任务副本/会话/快照，关闭上次未答问题",
		Stdin:       true,
		Destructive: true,
		Args:        []cli.Arg{taskArg},
		Flags:       []cli.Flag{taskActionFlag, {Name: "expected-node-run", Type: cli.TypeString, Required: true, Description: "预期阻塞节点运行的 UUID；节点已变化时服务端拒绝"}, {Name: "note-file", Type: cli.TypePathOrStdin, Description: "可选重试说明；- 从标准输入读取"}},
		Output:      "TaskDetail",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			action, err := taskAction(inv)
			if err != nil {
				return nil, err
			}
			node := inv.String("expected-node-run")
			if !IsUUID(node) {
				return nil, output.Errorf(output.CodeInvalidArgument, "--expected-node-run 必须是 UUID")
			}
			body := map[string]any{"actionId": action, "expectedNodeRunId": node}
			if inv.Has("note-file") {
				note, err := taskText(inv, "note-file")
				if err != nil {
					return nil, err
				}
				body["note"] = note
			}
			return send(ctx, inv, http.MethodPost, body, "tasks", inv.Args[0], "retry")
		}})
	add(&cli.Command{
		Path:        []string{"task", "cancel"},
		Summary:     "当前项目 Manager 取消任务；终止执行、关闭未答问题并保留历史",
		Destructive: true,
		Args:        []cli.Arg{taskArg},
		Flags:       []cli.Flag{taskActionFlag},
		Output:      "TaskDetail",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			action, err := taskAction(inv)
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPost, map[string]any{"actionId": action}, "tasks", inv.Args[0], "cancel")
		}})
	add(&cli.Command{
		Path:    []string{"task", "artifact", "put"},
		Summary: "当前节点显式保存文件成果，返回平台持有的引用；不会自动提交节点",
		Args:    []cli.Arg{taskArg, taskNodeArg},
		Flags:   []cli.Flag{taskActionFlag, {Name: "file", Type: cli.TypePath, Required: true, Description: "本地成果文件"}, {Name: "name", Type: cli.TypeString, Description: "成果名称，默认取本地文件名"}, {Name: "content-type", Type: cli.TypeString, Default: "application/octet-stream", Description: "成果 MIME 类型"}},
		Output:  "TaskArtifact",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			action, err := taskAction(inv)
			if err != nil {
				return nil, err
			}
			path := inv.String("file")
			data, err := readFile(path)
			if err != nil {
				return nil, err
			}
			name := inv.String("name")
			if name == "" {
				name = filepath.Base(path)
			}
			q := url.Values{"actionId": {action}, "nodeRunId": {inv.Args[1]}, "name": {name}, "contentType": {inv.String("content-type")}}
			return call(ctx, inv, conn.Request{Method: http.MethodPost, Path: []string{"tasks", inv.Args[0], "artifacts"}, Query: q, Body: data, ContentType: "application/octet-stream"})
		}})
	add(&cli.Command{
		Path:    []string{"task", "artifact", "list"},
		Summary: "从授权任务详情列出平台保存的成果元数据",
		Retry:   true,
		Args:    []cli.Arg{taskArg},
		Output:  "{artifacts: TaskArtifact[]}",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			resp, err := inv.Client.Do(ctx, conn.Request{Method: http.MethodGet, Path: []string{"tasks", inv.Args[0]},
				Retry: true})
			if err != nil {
				return nil, err
			}
			var detail map[string]json.RawMessage
			if json.Unmarshal(resp.Data, &detail) != nil || len(detail["artifacts"]) == 0 || detail["artifacts"][0] != '[' {
				return nil, output.Errorf(output.CodeUnexpectedResponse, "任务响应缺少 artifacts 数组")
			}
			return map[string]json.RawMessage{"artifacts": detail["artifacts"]}, nil
		}})
	add(&cli.Command{
		Path:    []string{"task", "artifact", "get"},
		Summary: "下载任务成果到指定文件，SHA-256 校验通过后原子保存",
		Retry:   true,
		Args:    []cli.Arg{taskArg, {Name: "artifactId", Description: "平台产物 ID，取自 task artifact list/put"}},
		Flags:   []cli.Flag{{Name: "out", Type: cli.TypePath, Required: true, Description: "本地输出文件路径；成功后替换同名文件"}},
		Output:  "{taskId, artifactId, size, sha256, savedTo}",
		Run:     taskArtifactGet})
}

func taskAction(inv *cli.Invocation) (string, error) {
	id := inv.String("action-id")
	if !IsUUID(id) {
		return "", output.Errorf(output.CodeInvalidArgument, "--action-id 必须是 UUID")
	}
	return id, nil
}

func taskText(inv *cli.Invocation, flag string) (string, error) {
	b, err := readInput(inv, flag)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(b) || flag != "note-file" && strings.TrimSpace(string(b)) == "" {
		return "", output.Errorf(output.CodeInvalidArgument, "--%s 须为非空 UTF-8 文本", flag)
	}
	return string(b), nil
}

func taskJSON(ctx context.Context, inv *cli.Invocation, path ...string) (any, error) {
	if err := taskRun(inv); err != nil {
		return nil, err
	}
	b, err := readInput(inv, "from-file")
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if !utf8.Valid(b) || json.Unmarshal(b, &obj) != nil || obj == nil {
		return nil, output.Errorf(output.CodeInvalidArgument, "--from-file 须包含一个 UTF-8 JSON 对象")
	}
	if id, present := explicitTaskRun(inv); present {
		if _, exists := obj["runId"]; exists {
			return nil, output.Errorf(output.CodeInvalidArgument, "运行参数与输入JSON的runId不能重复提供")
		}
		obj["runId"], _ = json.Marshal(id)
		b, _ = json.Marshal(obj)
	}
	return call(ctx, inv, conn.Request{Method: http.MethodPost, Path: path, Body: b, ContentType: "application/json"})
}

func taskRun(inv *cli.Invocation) error {
	if inv.Has("run") && inv.Has("run-id") {
		return output.Errorf(output.CodeInvalidArgument, "--run与--run-id不能同时提供")
	}
	if id, present := explicitTaskRun(inv); present && (!IsUUID(id) || id == "00000000-0000-0000-0000-000000000000") {
		return output.Errorf(output.CodeInvalidArgument, "运行参数必须是本轮准确非零UUID")
	}
	return nil
}

func explicitTaskRun(inv *cli.Invocation) (string, bool) {
	if inv.Has("run-id") {
		return inv.String("run-id"), true
	}
	return inv.String("run"), inv.Has("run")
}

func taskArtifactGet(ctx context.Context, inv *cli.Invocation) (any, error) {
	dest, err := filepath.Abs(inv.String("out"))
	if err != nil {
		return nil, output.Errorf(output.CodeOutputUnwritable, "无法确定输出路径")
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".loopctl-artifact-*")
	if err != nil {
		return nil, output.Errorf(output.CodeOutputUnwritable, "无法创建成果临时文件：%v", cause(err))
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	var size int64
	var sum string
	consume := func(resp *http.Response) error {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return output.Errorf(output.CodeOutputUnwritable, "无法重置临时文件：%v", err)
		}
		if err := f.Truncate(0); err != nil {
			return output.Errorf(output.CodeOutputUnwritable, "无法重置临时文件：%v", err)
		}
		h := sha256.New()
		w := &trackedWriter{w: f}
		n, err := io.Copy(io.MultiWriter(w, h), resp.Body)
		if w.err != nil {
			return output.Errorf(output.CodeOutputUnwritable, "无法保存成果：%v", w.err)
		}
		if err != nil {
			return err
		}
		size, sum = n, hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(resp.Header.Get("X-Content-SHA256"), sum) {
			return output.Errorf(output.CodeUnexpectedResponse, "成果的 SHA-256 缺失或与内容不一致")
		}
		return nil
	}
	_, e := inv.Client.Do(ctx, conn.Request{Method: http.MethodGet, Path: []string{"tasks", inv.Args[0], "artifacts", inv.Args[1]}, Accept: "application/octet-stream",
		Retry: true, Consume: consume})
	if e != nil {
		return nil, e
	}
	if err := f.Sync(); err != nil {
		return nil, output.Errorf(output.CodeOutputUnwritable, "无法保存成果：%v", err)
	}
	if err := f.Close(); err != nil {
		return nil, output.Errorf(output.CodeOutputUnwritable, "无法保存成果：%v", err)
	}
	if err := os.Rename(f.Name(), dest); err != nil {
		return nil, output.Errorf(output.CodeOutputUnwritable, "无法保存成果：%v", cause(err))
	}
	return map[string]any{"taskId": inv.Args[0], "artifactId": inv.Args[1], "size": size, "sha256": sum, "savedTo": dest}, nil
}
