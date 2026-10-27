package naming

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := []string{
		"开发助手", "a", "dev assistant", "前端-助手_1.0", "x@y", "C++ 助手",
		strings.Repeat("长", MaxLen),
		"{0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b}", // 带花括号不算 UUID 写法
		"0190a1b2c3d47e5f8a9b0c1d2e3f4a5b",       // 不带连字符不算 UUID 写法
	}
	for _, n := range ok {
		if msg := Validate("名称", n); msg != "" {
			t.Errorf("Validate(%q) = %q, want ok", n, msg)
		}
	}
	bad := map[string]string{
		"":                                     "不能为空",
		strings.Repeat("长", MaxLen+1):          "不能超过 100 个字符",
		"a\xffb":                               "不是有效的 UTF-8 文本",
		"a\tb":                                 "不能含控制字符",
		"a\u0085b":                             "不能含控制字符",
		" 助手":                                  "首尾不能有空白",
		"助手 ":                                  "首尾不能有空白",
		"\u3000助手":                             "首尾不能有空白",
		"助手\u00a0":                             "首尾不能有空白",
		"@助手":                                  "不能以 @ 开头",
		"前端/助手":                                "不能含 / 或 #",
		"助手#1":                                 "不能含 / 或 #",
		"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b": "不能是 UUID 格式",
		"0190A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B": "不能是 UUID 格式",
	}
	for n, want := range bad {
		if msg := Validate("名称", n); msg != "名称"+want {
			t.Errorf("Validate(%q) = %q, want %q", n, msg, "名称"+want)
		}
	}
}
