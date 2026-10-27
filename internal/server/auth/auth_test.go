package auth

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestTokenRoundTrip(t *testing.T) {
	for _, prefix := range []string{AgentPrefix, ExternalPrefix} {
		tok, err := NewToken(prefix)
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^` + prefix + `[0-9a-f]{32}_[A-Za-z0-9_-]{43}$`).MatchString(tok.Plain) {
			t.Fatalf("令牌格式不对：%s", tok.Plain)
		}
		p, id, secret, ok := ParseToken(tok.Plain)
		if !ok || p != prefix || id != tok.ID {
			t.Fatalf("解析失败：%v %v %v", ok, p, id)
		}
		if !SecretMatches(tok.Hash, secret) {
			t.Fatal("密钥应与摘要匹配")
		}
		if SecretMatches(tok.Hash, strings.Repeat("A", 43)) {
			t.Fatal("错误的密钥不应匹配")
		}
	}
	a, _ := NewToken(ExternalPrefix)
	b, _ := NewToken(ExternalPrefix)
	if a.Plain == b.Plain || a.ID == b.ID {
		t.Fatal("两次签发的令牌应不同")
	}
}

func TestParseTokenRejectsMalformed(t *testing.T) {
	good, _ := NewToken(ExternalPrefix)
	for _, s := range []string{
		"",
		"lne_",
		"lnx_" + good.Plain[4:],
		strings.ToUpper(good.Plain[:36]) + good.Plain[36:],
		good.Plain[:len(good.Plain)-1],
		good.Plain + "A",
		strings.Replace(good.Plain, "_", "-", 2),
		good.Plain[:36] + "_" + strings.Repeat("!", 43),
	} {
		if _, _, _, ok := ParseToken(s); ok {
			t.Errorf("应拒绝：%q", s)
		}
	}
}

func TestDisplayPrefix(t *testing.T) {
	tok, _ := NewToken(ExternalPrefix)
	if got := DisplayPrefix(tok.ID); got != tok.Plain[:12] || !regexp.MustCompile(`^lne_[0-9a-f]{8}$`).MatchString(got) {
		t.Fatalf("显示前缀应为令牌的前 12 个字符：%s %s", got, tok.Plain)
	}
}

func TestBearerToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?token=lne_abc", nil)
	if _, present := BearerToken(r); present {
		t.Fatal("URL 中的令牌不应被读取")
	}
	r.Header.Set("Authorization", "bearer  lne_x ")
	if tok, present := BearerToken(r); !present || tok != "lne_x" {
		t.Fatalf("应读取 Bearer 令牌：%q %v", tok, present)
	}
	r.Header.Set("Authorization", "Basic abc")
	if tok, present := BearerToken(r); !present || tok != "" {
		t.Fatalf("非 Bearer 写法应返回空令牌：%q %v", tok, present)
	}
}
