package commands_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "更新 golden 文件")

// 完整的 schema 输出固定在 golden 文件中，命令的任何变化都会在评审时显示为差异。
func TestSchemaGolden(t *testing.T) {
	r := newEnv(t).run("schema")
	r.ok(t)
	var buf bytes.Buffer
	json.Indent(&buf, r.Env.Data, "", "  ")
	buf.WriteByte('\n')
	p := filepath.Join("testdata", "schema.json")
	if *update {
		os.WriteFile(p, buf.Bytes(), 0o644)
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取 golden 文件：%v（用 -update 生成）", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("schema 输出与 %s 不一致；确认改动后用 go test -run TestSchemaGolden -update 更新", p)
	}
}
