package naming

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := []string{
		"Dev helper", "a", "dev assistant", "frontend-helper_1.0", "x@y", "C++ helper",
		strings.Repeat("长", MaxLen),              // check-english: zh (the length limit counts runes, not bytes)
		"{0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b}", // with braces it is not the UUID form
		"0190a1b2c3d47e5f8a9b0c1d2e3f4a5b",       // without hyphens it is not the UUID form
	}
	for _, n := range ok {
		if msg := Validate("Name", n); msg != "" {
			t.Errorf("Validate(%q) = %q, want ok", n, msg)
		}
	}
	bad := map[string]string{
		"":                                     " must not be empty.",
		strings.Repeat("长", MaxLen+1):          " must be at most 100 characters.", // check-english: zh (the length limit counts runes, not bytes)
		"a\xffb":                               " must be valid UTF-8 text.",
		"a\tb":                                 " must not contain control characters.",
		"a\u0085b":                             " must not contain control characters.",
		" helper":                              " must not start or end with whitespace.",
		"helper ":                              " must not start or end with whitespace.",
		"\u3000helper":                         " must not start or end with whitespace.", // check-english: zh (the ideographic space counts as whitespace)
		"helper\u00a0":                         " must not start or end with whitespace.",
		"@helper":                              " must not start with @.",
		"frontend/helper":                      " must not contain / or #.",
		"helper#1":                             " must not contain / or #.",
		"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b": " must not be in UUID format.",
		"0190A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B": " must not be in UUID format.",
	}
	for n, want := range bad {
		if msg := Validate("Name", n); msg != "Name"+want {
			t.Errorf("Validate(%q) = %q, want %q", n, msg, "Name"+want)
		}
	}
}
