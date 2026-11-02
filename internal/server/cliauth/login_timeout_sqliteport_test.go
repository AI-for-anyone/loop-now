//go:build sqliteport

// SQLITE-PORT(G4): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 login_timeout_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：TestGoogleLoginTimeoutUsesIndependentTerminalContext

package cliauth

import (
	"context"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGoogleLoginTimeoutUsesIndependentTerminalContext(t *testing.T) {
	f := newGoogleFixture(t)
	helper := googleLoginFunc(func(ctx context.Context, _ uuid.UUID, _ func(string) error) (clicredential.Google, error) {
		<-ctx.Done()
		return clicredential.Google{}, ctx.Err()
	})
	s := NewService(f.st, helper, nil, ServiceOptions{LoginTimeout: 100 * time.Millisecond, RecordTimeout: time.Second}, nil)
	t.Cleanup(func() { s.Close(context.Background()) })
	name := "仅合成超时"
	in := cm.CLIAuthStart{OperationID: uuid.New(), CLI: "gcloud", AuthType: "google_user", Name: &name}
	if _, err := s.Start(context.Background(), f.project, in); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, err := s.Get(context.Background(), f.project, in.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Operation.Status == "expired" {
			if state.Operation.AuthorizationURL != nil {
				t.Fatal("timeout retained URL")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expired helper deadline prevented independent terminal write")
		}
		time.Sleep(time.Millisecond)
	}
	var versions int
	if err := f.st.Pool.QueryRow(context.Background(), "SELECT count(*) FROM project_cli_versions").Scan(&versions); err != nil || versions != 0 {
		t.Fatal("timeout sealed credential", err)
	}
}
