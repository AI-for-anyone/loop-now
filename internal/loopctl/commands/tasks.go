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
	taskArg        = cli.Arg{Name: "taskId", Description: "Task ID, from the result of task create/get/current"}
	taskNodeArg    = cli.Arg{Name: "nodeRunId", Description: "Node run ID, from the current task; after a retry or loop, the new ID must be used"}
	taskActionFlag = cli.Flag{Name: "action-id", Type: cli.TypeString, Required: true, Description: "UUID of this action; when resending the same action by hand keep the same ID and content, and never reuse it for another action"}
	taskInputFlag  = cli.Flag{Name: "from-file", Type: cli.TypePathOrStdin, Required: true, Description: "Formal TaskCreateInput/TaskSubmitInput JSON file; - reads standard input; contains the caller-generated actionId"}
	taskRunFlag    = cli.Flag{Name: "run", Type: cli.TypeString, Description: "The exact run UUID given in the platform's prompt for this turn; required for the dedicated recovery node; it does not read the latest run or retry automatically"}
	taskRunIDFlag  = cli.Flag{Name: "run-id", Type: cli.TypeString, Description: "The exact run UUID for this turn; synonym of --run, and the two cannot be given together; required for the dedicated recovery node"}
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
		Summary: "For the current project Manager: page through the project's tasks",
		Retry:   true,
		Flags:   []cli.Flag{cursorFlag, limitFlag},
		Output:  "TaskPage",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, pageQuery(inv), "tasks")
		},
	})
	add(&cli.Command{
		Path:    []string{"task", "agents"},
		Summary: "For the current project Manager: show the safe directory of Agents available in the project",
		Retry:   true,
		Output:  "TaskAgentList",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "tasks", "agents")
		}})
	add(&cli.Command{
		Path:    []string{"task", "create"},
		Summary: "For the Manager: create a task with a fixed workflow; after a timeout it may already be created, so check before deciding whether to resend",
		Stdin:   true,
		Flags:   []cli.Flag{taskInputFlag},
		Output:  "TaskDetail",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return taskJSON(ctx, inv, "tasks")
		}})
	add(&cli.Command{
		Path:    []string{"task", "get"},
		Summary: "Show a task you may access, with its node records, outputs and saved artifacts",
		Retry:   true,
		Args:    []cli.Arg{taskArg},
		Output:  "TaskDetail",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "tasks", inv.Args[0])
		}})
	add(&cli.Command{
		Path:    []string{"task", "history"},
		Summary: "Page through the conversation of a node run, keeping execution states and specific errors as is",
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
		Summary: "For a task instance: show its current task; regular identities have no current task",
		Retry:   true,
		Output:  "TaskDetail",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "tasks", "current")
		}})
	add(&cli.Command{
		Path:    []string{"task", "submit"},
		Summary: "Submit the current node's output and exit; after it is saved, the current turn must still end normally before the platform advances",
		Stdin:   true,
		Args:    []cli.Arg{taskArg, taskNodeArg},
		Flags:   []cli.Flag{taskInputFlag, taskRunFlag, taskRunIDFlag},
		Output:  "TaskNodeRun",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return taskJSON(ctx, inv, "tasks", inv.Args[0], "nodes", inv.Args[1], "submit")
		}})
	add(&cli.Command{
		Path:    []string{"task", "fail"},
		Summary: "Declare that the current node failed, keeping the reason; the task blocks until a person or the Manager handles it",
		Stdin:   true,
		Args:    []cli.Arg{taskArg, taskNodeArg},
		Flags:   []cli.Flag{taskActionFlag, taskRunFlag, taskRunIDFlag, {Name: "reason-file", Type: cli.TypePathOrStdin, Required: true, Description: "UTF-8 file with the failure reason; - reads standard input; cannot be empty"}},
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
		Summary:     "For the current project Manager: retry the blocked node; reuses the task copy/session/snapshot and closes the last unanswered question",
		Stdin:       true,
		Destructive: true,
		Args:        []cli.Arg{taskArg},
		Flags:       []cli.Flag{taskActionFlag, {Name: "expected-node-run", Type: cli.TypeString, Required: true, Description: "UUID of the expected blocked node run; the server refuses if the node has changed"}, {Name: "note-file", Type: cli.TypePathOrStdin, Description: "Optional retry note; - reads standard input"}},
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
		Summary:     "For the current project Manager: cancel the task; ends the execution, closes unanswered questions and keeps the history",
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
		Summary: "For the current node: explicitly save a file artifact and return a platform-held reference; does not submit the node automatically",
		Args:    []cli.Arg{taskArg, taskNodeArg},
		Flags:   []cli.Flag{taskActionFlag, {Name: "file", Type: cli.TypePath, Required: true, Description: "Local artifact file"}, {Name: "name", Type: cli.TypeString, Description: "Artifact name; defaults to the local file name"}, {Name: "content-type", Type: cli.TypeString, Default: "application/octet-stream", Description: "Artifact MIME type"}},
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
		Summary: "List the metadata of the artifacts the platform saved, from the authorized task details",
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
		Summary: "Download a task artifact to the given file and save it atomically after the SHA-256 check passes",
		Retry:   true,
		Args:    []cli.Arg{taskArg, {Name: "artifactId", Description: "Platform artifact ID, from task artifact list/put"}},
		Flags:   []cli.Flag{{Name: "out", Type: cli.TypePath, Required: true, Description: "Local output file path; replaces the file of the same name on success"}},
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
