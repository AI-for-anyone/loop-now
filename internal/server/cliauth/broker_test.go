package cliauth

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/clicredential"
	"net"
	"os"
	"testing"
	"time"
)

type grantResolveFunc func(context.Context, clicredential.Binding) (clicredential.Google, error)

func (f grantResolveFunc) Resolve(ctx context.Context, b clicredential.Binding) (clicredential.Google, error) {
	return f(ctx, b)
}
func TestGoogleBrokerScopeCloseAndNoRefreshSecret(t *testing.T) {
	binding := clicredential.Binding{VersionID: uuid.New(), AgentID: uuid.New(), ProjectID: uuid.New(), AccountID: uuid.New(), CLI: "gcloud"}
	calls := 0
	b, e := NewBroker(binding, grantResolveFunc(func(ctx context.Context, got clicredential.Binding) (clicredential.Google, error) {
		calls++
		if got != binding {
			t.Error("request changed captured binding")
		}
		c := syntheticGoogle()
		c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		return c, nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close(context.Background()) })
	for p, mode := range map[string]os.FileMode{b.path: 0600, b.dir: 0700} {
		fi, e := os.Stat(p)
		if e != nil || fi.Mode().Perm() != mode {
			t.Fatal("private socket permissions")
		}
	}
	for _, capability := range []string{"wrong", b.capability} {
		conn, e := net.Dial("unix", b.path)
		if e != nil {
			t.Fatal(e)
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		id := uuid.New()
		json.NewEncoder(conn).Encode(map[string]any{"protocol": 1, "type": "resolve", "requestId": id, "capability": capability})
		raw, e := io.ReadAll(conn)
		conn.Close()
		if e != nil {
			t.Fatal("broker did not close reply", e)
		}
		var result map[string]json.RawMessage
		if json.Unmarshal(raw, &result) != nil {
			t.Fatal("broker response invalid")
		}
		if capability == b.capability {
			if len(result) != 4 || result["accessToken"] == nil || result["refreshToken"] != nil {
				t.Fatal("broker returned non-access fields")
			}
		} else if string(result["code"]) != `"forbidden"` {
			t.Fatal("wrong capability accepted")
		}
	}
	if calls != 1 {
		t.Fatal("unauthorized request resolved a grant")
	}
	// A stalled pre-frame client must not hold shutdown or leave its socket.
	conn, e := net.Dial("unix", b.path)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = b.Close(ctx); e != nil {
		t.Fatal("broker shutdown stuck", e)
	}
	if _, e = os.Stat(b.dir); !os.IsNotExist(e) {
		t.Fatal("broker resources retained")
	}
}
