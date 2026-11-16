package httpapi_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// A plugin's README (plugins/SPEC.md §2.3): read through the preview before install, by plugin after install; the file
// is chosen by locale, an invalid locale gives 422, a preview that does not exist or was already used for an install
// gives 404; without a README, file and markdown are null. Requests and responses are validated against the contract.
func TestPluginReadmeAPI(t *testing.T) {
	e := newEnv(t)
	dir := writePluginDir(t, "guide")
	for name, content := range map[string]string{"README.md": "# Guide", "README.zh-CN.md": "# 指南", "readme.en-us.md": "# Guide (US)"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) (any, any) {
		t.Helper()
		r := decode[map[string]any](t, e.expect(200, "GET", path, nil))
		if r["truncated"] != false {
			t.Fatalf("%s：%v", path, r)
		}
		return r["file"], r["markdown"]
	}
	pv := decode[map[string]any](t, e.expect(201, "POST", "/plugin-previews", map[string]any{"source": map[string]any{"kind": "local", "location": dir}}))
	preview := "/plugin-previews/" + pv["previewId"].(string) + "/readme"
	for locale, want := range map[string][2]string{"": {"README.md", "# Guide"}, "?locale=zh-CN": {"README.zh-CN.md", "# 指南"}, "?locale=en-US": {"readme.en-us.md", "# Guide (US)"}, "?locale=fr": {"README.md", "# Guide"}} {
		if file, markdown := read(preview + locale); file != want[0] || markdown != want[1] {
			t.Fatalf("预览 %q：%v %v", locale, file, markdown)
		}
	}
	for _, bad := range []string{"?locale=zh_CN", "?locale=", "?locale=x"} {
		e.expect(422, "GET", preview+bad, nil, "X-Contract-Invalid-Request", "1")
	}
	e.expect(404, "GET", "/plugin-previews/"+uuid.NewString()+"/readme", nil)
	e.expect(404, "GET", "/plugin-previews/not-a-uuid/readme", nil, "X-Contract-Invalid-Request", "1")

	e.expect(201, "POST", "/plugins", map[string]any{"previewId": pv["previewId"], "confirm": true})
	e.expect(404, "GET", preview, nil)
	if file, markdown := read("/plugins/guide/readme?locale=zh-CN"); file != "README.zh-CN.md" || markdown != "# 指南" {
		t.Fatalf("已安装：%v %v", file, markdown)
	}
	e.expect(422, "GET", "/plugins/guide/readme?locale=zh_CN", nil, "X-Contract-Invalid-Request", "1")
	e.expect(404, "GET", "/plugins/missing/readme", nil)

	e.installPlugin(writePluginDir(t, "bare"))
	if file, markdown := read("/plugins/bare/readme"); file != nil || markdown != nil {
		t.Fatalf("没有 README：%v %v", file, markdown)
	}
}
