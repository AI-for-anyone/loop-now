package larkauth

import (
	"context"
	"encoding/json"
	"io"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLarkCoordinatorChildProcess(t *testing.T) {
	if os.Getenv("LARK_TEST_CHILD") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var binding clicredential.LarkBinding
	if json.Unmarshal([]byte(os.Getenv("LARK_TEST_BINDING")), &binding) != nil || !binding.Valid() {
		t.Fatal("invalid child binding")
	}
	endpoint, e := url.Parse(os.Getenv("LARK_TEST_ENDPOINT"))
	if e != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" {
		t.Fatal("child endpoint not owned loopback")
	}
	st, e := store.Open(ctx, os.Getenv("LOOP_NOW_TEST_DATABASE_URL"), store.OpenOptions{SearchPath: os.Getenv("LARK_TEST_SCHEMA")})
	if e != nil {
		t.Fatal("TEST connection failed")
	}
	defer st.Close()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	rf := larkRefreshFunc(func(ctx context.Context, old clicredential.Lark) (clicredential.Lark, error) {
		plain, e := old.Encode()
		if e != nil {
			return old, e
		}
		defer clear(plain)
		req, e := http.NewRequestWithContext(ctx, "POST", endpoint.String(), strings.NewReader(string(plain)))
		if e != nil {
			return old, e
		}
		response, e := client.Do(req)
		if e != nil {
			return old, e
		}
		defer response.Body.Close()
		data, e := io.ReadAll(io.LimitReader(response.Body, clicredential.MaxBytes+1))
		defer clear(data)
		if e != nil || response.StatusCode != 200 {
			return old, ErrUnavailable
		}
		return clicredential.DecodeLark(data)
	})
	c := NewCoordinator(st, rf, CoordinatorOptions{WaitTimeout: 8 * time.Second}, nil)
	defer c.Close(ctx)
	source, e := c.TokenSource(binding)
	if e != nil {
		t.Fatal("child source failed")
	}
	access, e := source.AccessToken(ctx)
	if e != nil || access != "cross-process-access-2" {
		t.Fatal("child did not resolve committed generation")
	}
}
func TestLarkActualProcessesShareOnePGRefresh(t *testing.T) {
	f := newLarkFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var calls atomic.Int32
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		raw, e := io.ReadAll(io.LimitReader(r.Body, clicredential.MaxBytes+1))
		defer clear(raw)
		old, e2 := clicredential.DecodeLark(raw)
		if e != nil || e2 != nil || old.RefreshToken != "fixture-refresh-1" || calls.Add(1) != 1 {
			w.WriteHeader(400)
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		old.AccessToken = "cross-process-access-2"
		old.RefreshToken = "cross-process-refresh-2"
		old.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		plain, e := old.Encode()
		if e != nil {
			w.WriteHeader(500)
			return
		}
		defer clear(plain)
		w.Write(plain)
	}))
	defer provider.Close()
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(f.binding)
	schema := f.st.Pool.Config().ConnConfig.RuntimeParams["search_path"]
	commands := make([]*exec.Cmd, 4)
	for i := range commands {
		cmd := exec.CommandContext(ctx, self, "-test.run=^TestLarkCoordinatorChildProcess$", "-test.count=1")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LARK_TEST_CHILD=1", "LARK_TEST_BINDING=" + string(encoded), "LARK_TEST_SCHEMA=" + schema, "LARK_TEST_ENDPOINT=" + provider.URL, "LOOP_NOW_TEST_DATABASE_URL=" + f.st.Pool.Config().ConnString()}
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if e = cmd.Start(); e != nil {
			close(release)
			t.Fatal("child start failed")
		}
		commands[i] = cmd
	}
	close(release)
	var wg sync.WaitGroup
	failed := make(chan bool, 4)
	for _, cmd := range commands {
		wg.Add(1)
		go func(cmd *exec.Cmd) { defer wg.Done(); failed <- cmd.Wait() != nil }(cmd)
	}
	wg.Wait()
	close(failed)
	for bad := range failed {
		if bad {
			t.Fatal("isolated child failed (output withheld)")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("multiple OS processes consumed refresh more than once")
	}
	row, e := f.st.GetLarkCLIGrant(ctx, f.binding)
	if e != nil || row.Generation != 2 || row.RefreshState != "ready" {
		t.Fatal("cross-process result was not committed")
	}
}
