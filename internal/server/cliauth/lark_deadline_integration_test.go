package cliauth

import (
	"context"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
	"testing"
	"time"
)

func TestLarkLoginCommitSourceLockCrossesDeadlineWithoutSeal(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	source := larkSource(t, f)
	in := larkStart(source)
	deadline := time.Now().Add(time.Second)
	op, _, err := f.st.NewProjectCLIAuthOperation(ctx, f.project, in, deadline)
	if err != nil {
		t.Fatal(err)
	}
	// 测试占住唯一的写连接，提交登录结果的写事务只能在它放开之后开始；放开时操作的期限已过。
	held, release, released := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		released <- f.st.Pool.InTx(ctx, func(*db.Tx) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	done := make(chan error, 1)
	identity := cm.LarkIdentity{Brand: "feishu", AppID: "app-fixture", OpenID: "ou-fixture", DisplayName: "test", VerifiedAt: time.Now()}
	go func() {
		_, _, e := f.st.CompleteProjectCLIAuthOperation(ctx, f.project, op.ID, store.ProjectCredentialWrite{AuthMethod: "oauth", LarkIdentity: &identity, Secret: []byte("must-not-write")})
		done <- e
	}()
	time.Sleep(time.Until(deadline) + 10*time.Millisecond)
	select {
	case e := <-done:
		t.Fatal("commit finished while the writer was held", e)
	default:
	}
	close(release)
	if err = <-released; err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	read, err := f.st.GetProjectCLIAuthOperation(ctx, f.project, op.ID)
	if err != nil || read.Status != "expired" || read.AuthorizationURL != nil || read.UserCode != nil || read.SourceVersionID == nil {
		t.Fatal("late login result lost terminal source audit")
	}
	var written int
	if err = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM project_cli_versions WHERE secret=$1`, []byte("must-not-write")).Scan(&written); err != nil || written != 0 {
		t.Fatal("late login result wrote credential", err)
	}
	var grants int
	if err = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM lark_cli_grants`).Scan(&grants); err != nil || grants != 0 {
		t.Fatal("expired source-lock waiter created user grant")
	}
}
