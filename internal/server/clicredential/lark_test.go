package clicredential

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	cm "loop-now/internal/server/credentialmodel"
	"strings"
	"testing"
	"time"
)

func TestLarkSecretCodecAndBinding(t *testing.T) {
	c := Lark{Type: "lark_user", AppSecret: "synthetic-app-secret", AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), RefreshExpiresAt: time.Now().Add(24 * time.Hour).UnixMilli(), Identity: cm.LarkIdentity{Brand: "feishu", AppID: "app-fixture", OpenID: "ou-fixture", DisplayName: "离线用户", VerifiedAt: time.Now()}, RuntimeVariantVersion: LarkUserVariant}
	b := LarkBinding{ProjectID: uuid.New(), AccountID: uuid.New(), VersionID: uuid.New(), AgentID: uuid.New(), Brand: c.Identity.Brand, AppID: c.Identity.AppID, OpenID: c.Identity.OpenID, RuntimeVariantVersion: LarkUserVariant}
	if !c.Matches(b) {
		t.Fatal("valid fixed identity rejected")
	}
	plain, e := c.Encode()
	if e != nil {
		t.Fatal(e)
	}
	defer clear(plain)
	r, e := DecodeLark(plain)
	if e != nil || r.Identity.GrantedScopes != nil {
		t.Fatal("unknown scopes must remain nil")
	}
	c.Identity.GrantedScopes = []string{}
	plain2, e := c.Encode()
	if e != nil {
		t.Fatal(e)
	}
	defer clear(plain2)
	r, e = DecodeLark(plain2)
	if e != nil || r.Identity.GrantedScopes == nil {
		t.Fatal("known empty scopes lost")
	}
	for _, mutate := range []func(*Lark){func(x *Lark) { x.RefreshToken = "" }, func(x *Lark) { x.ExpiresAt = 0 }, func(x *Lark) { x.Identity.Brand = "unknown" }, func(x *Lark) { x.Identity.OpenID = "" }, func(x *Lark) { x.RuntimeVariantVersion = "native" }} {
		bad := c
		mutate(&bad)
		if _, e := bad.Encode(); e == nil {
			t.Fatal("bad credential accepted")
		}
	}
	b.ProjectID = uuid.Nil
	if c.Matches(b) {
		t.Fatal("nil project accepted")
	}
	var obj map[string]any
	json.Unmarshal(plain, &obj)
	obj["unexpected"] = "rejected"
	bad, _ := json.Marshal(obj)
	if _, e = DecodeLark(bad); e == nil {
		t.Fatal("unknown field accepted")
	}
	if _, e = json.Marshal(c); e == nil {
		t.Fatal("implicit secret encoding accepted")
	}
	if strings.Contains(fmt.Sprintf("%v %#v", c, c), c.AccessToken) {
		t.Fatal("secret formatting leak")
	}
	c.Identity.GrantedScopes = []string{"offline_access"}
	cl := c.Clone()
	cl.Identity.GrantedScopes[0] = "other"
	if c.Identity.GrantedScopes[0] != "offline_access" {
		t.Fatal("clone aliases scopes")
	}
}
