package httpapi

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/naming"
)

// TestEntityNameSchemaMatchesNaming 确认 OpenAPI 中 EntityName 的约束与 naming.Validate 判定一致：
// 每个码点（到 U+3100，外加几个常见的高位字符）分别放在开头、中间、结尾与单独出现，比对两边的结果。
func TestEntityNameSchemaMatchesNaming(t *testing.T) {
	doc := contracttest.Load(t)
	ref, ok := doc.Components.Schemas["EntityName"]
	if !ok || ref.Value == nil {
		t.Fatal("契约中没有 EntityName")
	}
	schema := ref.Value

	var corpus []string
	runes := []rune{0xFEFF, 0xFFFD, 0x1F600, 0x10FFFF}
	for r := rune(0); r <= 0x3100; r++ {
		runes = append(runes, r)
	}
	for _, r := range runes {
		c := string(r)
		corpus = append(corpus, c, "a"+c, c+"a", "a"+c+"b")
	}
	corpus = append(corpus,
		"", "开发助手", strings.Repeat("长", naming.MaxLen), strings.Repeat("长", naming.MaxLen+1),
		"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", "0190A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B",
		"{0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b}", "0190a1b2c3d47e5f8a9b0c1d2e3f4a5b",
	)

	mismatches := 0
	for _, name := range corpus {
		want := naming.Validate("名称", name) == ""
		got := schema.VisitJSON(name, openapi3.EnableJSONSchema2020()) == nil
		if got != want {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("%q：契约判定 %v，naming 判定 %v", name, got, want)
			}
		}
	}
	if mismatches > 20 {
		t.Errorf("共 %d 处不一致", mismatches)
	}
}
