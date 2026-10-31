package pirpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPipeCompactAndExportHTMLProtocol(t *testing.T) {
	f := newPipeFake(t)
	obs := &obsRec{}
	f.c.SetObserver(obs)
	ctx := testCtx(t)
	type compactOut struct {
		res CompactResult
		err error
	}
	compactDone := make(chan compactOut, 1)
	go func() {
		res, err := f.c.Compact(ctx, "保留关键决定")
		compactDone <- compactOut{res, err}
	}()
	cmd := f.next()
	if cmd["type"] != "compact" || cmd["customInstructions"] != "保留关键决定" {
		t.Fatalf("compact 参数不符：%v", cmd)
	}
	id := cmd["id"].(string)
	f.send(
		`{"type":"compaction_start","reason":"manual"}`,
		jsonLine(map[string]any{"type": "compaction_end", "reason": "manual", "aborted": false, "willRetry": false,
			"result": map[string]any{"summary": "s", "firstKeptEntryId": "e1", "tokensBefore": 4200, "estimatedTokensAfter": 350, "usage": usageJSON(300, 40, 0, 0)}}),
		okResp(id, "compact", map[string]any{"tokensBefore": 4200, "estimatedTokensAfter": 350}),
	)
	out := <-compactDone
	if out.err != nil || out.res != (CompactResult{TokensBefore: 4200, EstimatedTokensAfter: 350}) {
		t.Fatalf("compact 结果不符：%+v %v", out.res, out.err)
	}
	obs.waitUsage(t, 1)
	usage, inTurn, _, _ := obs.snapshot()
	if len(usage) != 1 || usage[0].Kind != UsageCompaction || !inTurn[0] {
		t.Fatalf("压缩用量应归属当前维护运行：%+v %v", usage, inTurn)
	}

	exportDone := make(chan struct {
		path string
		err  error
	}, 1)
	go func() {
		path, err := f.c.ExportHTML(ctx, "/tmp/session.html")
		exportDone <- struct {
			path string
			err  error
		}{path, err}
	}()
	cmd = f.next()
	if cmd["type"] != "export_html" || cmd["outputPath"] != "/tmp/session.html" {
		t.Fatalf("export_html 参数不符：%v", cmd)
	}
	f.send(okResp(cmd["id"].(string), "export_html", map[string]any{"path": "/tmp/session.html"}))
	exported := <-exportDone
	if exported.err != nil || exported.path != "/tmp/session.html" {
		t.Fatalf("导出路径不符：%q %v", exported.path, exported.err)
	}
}

func TestPipeCompactOmitsEmptyInstructions(t *testing.T) {
	f := newPipeFake(t)
	done := make(chan error, 1)
	go func() {
		_, err := f.c.Compact(testCtx(t), "")
		done <- err
	}()
	cmd := f.next()
	if _, exists := cmd["customInstructions"]; exists {
		t.Fatalf("空指令不应发送 customInstructions：%v", cmd)
	}
	f.send(okResp(cmd["id"].(string), "compact", map[string]any{"tokensBefore": 10, "estimatedTokensAfter": 5}))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestFakeCompactAbortAndContinue(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "instructions.txt")
	p := startFake(t, []string{"FAKEPI_COMPACT_DELAY=30s", "FAKEPI_COMPACT_CAPTURE=" + capture})
	ctx := testCtx(t)
	done := make(chan error, 1)
	go func() {
		_, err := p.Compact(ctx, "只保留待办")
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(capture); err == nil {
			if string(data) != "只保留待办" {
				t.Fatalf("压缩指令不符：%q", data)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等待 compact 开始超时")
		}
		time.Sleep(10 * time.Millisecond)
	}
	abortCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := p.Abort(abortCtx); err != nil {
		t.Fatalf("Abort 应能取消 compact：%v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("被取消的 compact 不应成功")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("compact 未在 Abort 后返回")
	}
	res, err := p.Prompt(ctx, "after", nil)
	if err != nil || res.Outcome != OutcomeCompleted || res.Text != "echo: after" {
		t.Fatalf("取消压缩后进程应可继续使用：%+v %v", res, err)
	}
}

func TestFakeExportHTMLWritesReturnedFile(t *testing.T) {
	p := startFake(t, []string{"FAKEPI_EXPORT_CONTENT=<html>session</html>"})
	output := filepath.Join(t.TempDir(), "session.html")
	returned, err := p.ExportHTML(testCtx(t), output)
	if err != nil || returned != output {
		t.Fatalf("导出返回不符：%q %v", returned, err)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "<html>session</html>" {
		t.Fatalf("导出文件不符：%q %v", data, err)
	}
}
