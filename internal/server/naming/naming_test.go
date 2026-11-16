package naming

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := []string{
		"开发助手", "a", "dev assistant", "前端-助手_1.0", "x@y", "C++ 助手",
		strings.Repeat("长", MaxLen),
		"{0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b}", // with braces it is not the UUID form
		"0190a1b2c3d47e5f8a9b0c1d2e3f4a5b",       // without hyphens it is not the UUID form
	}
	for _, n := range ok {
		if msg := Validate("名称", n); msg != "" {
			t.Errorf("Validate(%q) = %q, want ok", n, msg)
		}
	}
	bad := map[string]string{
		"":                                     " must not be empty.",
		strings.Repeat("长", MaxLen+1):          " must be at most 100 characters.",
		"a\xffb":                               " must be valid UTF-8 text.",
		"a\tb":                                 " must not contain control characters.",
		"a\u0085b":                             " must not contain control characters.",
		" 助手":                                  " must not start or end with whitespace.",
		"助手 ":                                  " must not start or end with whitespace.",
		"\u3000助手":                             " must not start or end with whitespace.",
		"助手\u00a0":                             " must not start or end with whitespace.",
		"@助手":                                  " must not start with @.",
		"前端/助手":                                " must not contain / or #.",
		"助手#1":                                 " must not contain / or #.",
		"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b": " must not be in UUID format.",
		"0190A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B": " must not be in UUID format.",
	}
	for n, want := range bad {
		if msg := Validate("名称", n); msg != "名称"+want {
			t.Errorf("Validate(%q) = %q, want %q", n, msg, "名称"+want)
		}
	}
}
