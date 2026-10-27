package modelauth

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/modelcredential"
)

type resolverFunc func(context.Context, modelcredential.Binding) (modelcredential.Credential, error)

func (f resolverFunc) Resolve(ctx context.Context, binding modelcredential.Binding) (modelcredential.Credential, error) {
	return f(ctx, binding)
}

func TestRuntimePrivateChannelBindsIdentityAndSanitizesFailure(t *testing.T) {
	ref := modelcredential.Binding{VersionID: uuid.New(), AgentID: uuid.New(), Provider: "openai-codex"}
	var calls atomic.Int32
	bridge, err := NewRuntimeBridge(ref, resolverFunc(func(_ context.Context, got modelcredential.Binding) (modelcredential.Credential, error) {
		if got != ref {
			t.Error("runtime selected another authorization")
		}
		if calls.Add(1) == 1 {
			return oauth(t, "account-A", "private-fd-only", time.Now().Add(time.Hour)), nil
		}
		return modelcredential.Credential{}, errors.New("provider-secret-body-sentinel")
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	child := bridge.ChildFiles()
	if err := child[0].SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(child[0])
	for _, id := range []string{"first", "second"} {
		if _, err := fmt.Fprintf(child[1], "{\"id\":%q,\"method\":\"refresh\",\"provider\":\"openai-codex\"}\n", id); err != nil {
			t.Fatal(err)
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			ID         string
			OK         bool
			Credential json.RawMessage
			Code       string
		}
		if err := json.Unmarshal(line, &response); err != nil || response.ID != id {
			t.Fatal("bad correlation", err)
		}
		if id == "first" {
			value, err := modelcredential.Decode(response.Credential)
			if err != nil || !response.OK || !value.SameAccount(oauth(t, "account-A", "x", time.Now())) {
				t.Fatal("private response missing same-account credential", err)
			}
		} else if response.OK || response.Code != "auth_unavailable" || strings.Contains(string(line), "sentinel") || len(response.Credential) > 0 {
			t.Fatal("unsafe runtime failure response")
		}
	}
}
func TestRuntimeRejectsSelectorsOversizedFramesAndUnknownProvider(t *testing.T) {
	for name, frame := range map[string]string{
		"grant":    `{"id":"1","method":"refresh","provider":"openai-codex","versionId":"other"}`,
		"agent":    `{"id":"1","method":"refresh","provider":"openai-codex","agentId":"other"}`,
		"task":     `{"id":"1","method":"refresh","provider":"openai-codex","taskId":"other"}`,
		"provider": `{"id":"1","method":"refresh","provider":"anthropic"}`,
		"oversize": strings.Repeat("x", MaxFrame+1),
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			bridge, err := NewRuntimeBridge(modelcredential.Binding{VersionID: uuid.New(), AgentID: uuid.New(), Provider: "openai-codex"}, resolverFunc(func(context.Context, modelcredential.Binding) (modelcredential.Credential, error) {
				calls.Add(1)
				return modelcredential.Credential{}, nil
			}), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer bridge.Close()
			child := bridge.ChildFiles()
			child[1].SetWriteDeadline(time.Now().Add(time.Second))
			child[0].SetReadDeadline(time.Now().Add(time.Second))
			_, _ = fmt.Fprintln(child[1], frame)
			_, err = bufio.NewReader(child[0]).ReadBytes('\n')
			if err == nil || calls.Load() != 0 {
				t.Fatal("untrusted runtime frame reached resolver")
			}
		})
	}
}
func TestRuntimePipeExitCancelsWaiterWithinBound(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	bridge, err := NewRuntimeBridge(modelcredential.Binding{VersionID: uuid.New(), AgentID: uuid.New(), Provider: "openai-codex"}, resolverFunc(func(ctx context.Context, _ modelcredential.Binding) (modelcredential.Credential, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return modelcredential.Credential{}, ctx.Err()
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	child := bridge.ChildFiles()
	fmt.Fprintln(child[1], `{"id":"exit","method":"refresh","provider":"openai-codex"}`)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resolver not started")
	}
	child[1].Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("exited Pi left a hanging waiter")
	}
}
