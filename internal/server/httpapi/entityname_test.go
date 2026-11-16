package httpapi

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/naming"
)

// TestEntityNameSchemaMatchesNaming confirms that the EntityName constraints in OpenAPI agree with naming.Validate:
// every code point (up to U+3100, plus a few common high characters) is placed at the start, in the middle, at the end
// and alone, and the results of both sides are compared.
func TestEntityNameSchemaMatchesNaming(t *testing.T) {
	doc := contracttest.Load(t)
	ref, ok := doc.Components.Schemas["EntityName"]
	if !ok || ref.Value == nil {
		t.Fatal("the contract has no EntityName")
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
		"", "开发助手", strings.Repeat("长", naming.MaxLen), strings.Repeat("长", naming.MaxLen+1), // check-english: zh (names may contain Han characters; the length limit counts runes)
		"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", "0190A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B",
		"{0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b}", "0190a1b2c3d47e5f8a9b0c1d2e3f4a5b",
	)

	mismatches := 0
	for _, name := range corpus {
		want := naming.Validate("Name", name) == ""
		got := schema.VisitJSON(name, openapi3.EnableJSONSchema2020()) == nil
		if got != want {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("%q: the contract says %v, naming says %v", name, got, want)
			}
		}
	}
	if mismatches > 20 {
		t.Errorf("%d mismatches in total", mismatches)
	}
}
