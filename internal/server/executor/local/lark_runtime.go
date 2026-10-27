package local

import (
	"context"
	"encoding/hex"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/executor"
)

// LarkRuntimeRequest comes only from the captured execution snapshot and local
// runtime directories. RuntimeID is snap.AgentID (the task instance ID for task
// execution); the configuration Agent is Binding.AgentID. App must set the
// trusted LaunchConfig's InputRoots to only Workspace, never private HOME.
type LarkRuntimeRequest struct {
	RuntimeID       uuid.UUID
	Binding         clicredential.LarkBinding
	Home, Workspace string
}

// LarkRuntime belongs to one captured runtime generation. Environment exposes
// only the private exec endpoint/capability for the PATH shim; it must not carry
// native HMAC capability, provider tokens, program selection or root overrides.
// Close quickly revokes admission/cancels children; only bounded Wait confirms
// native Start/Wait and HTTP drain. Cancellation/timeout is not a drain receipt.
type LarkRuntime interface {
	BinDir() string
	Environment() map[string]string
	Close() error
	Wait(context.Context) error
}

const larkExecURL = "LOOP_NOW_LARK_EXEC_URL"
const larkExecKey = "LOOP_NOW_LARK_EXEC_KEY"

type larkResource struct {
	runtime LarkRuntime
}

func nilLarkRuntime(r LarkRuntime) bool {
	if r == nil {
		return true
	}
	v := reflect.ValueOf(r)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return v.IsNil()
	}
	return false
}

func sameLarkBinding(a, b *clicredential.LarkBinding) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (e *Executor) openLarkRuntime(ctx context.Context, snap agentconfig.Snapshot, d agentDirs) (*larkResource, string, []string, error) {
	reject := func() (*larkResource, string, []string, error) {
		return nil, "", nil, fail(executor.ErrCredentialUnavailable, "Lark 用户运行通道不可用")
	}
	if snap.LarkBinding == nil {
		return nil, "", nil, nil
	}
	agent := snap.AgentID
	if snap.TaskID != uuid.Nil {
		agent = snap.ConfigurationAgentID
	}
	if e.o.LarkFactory == nil || !snap.LarkBinding.Valid() || snap.LarkBinding.AgentID != agent || !slices.Contains(snap.CLIs, "lark-cli") {
		return reject()
	}
	// The factory context governs startup only. Once published, the factory
	// must own an independent scope lifetime until Close/Wait; a retained Pi
	// outlives both this registration context and one Execute's run context.
	registration, done := context.WithTimeout(ctx, e.handoffTimeout())
	r, err := e.o.LarkFactory(registration, LarkRuntimeRequest{RuntimeID: snap.AgentID, Binding: *snap.LarkBinding, Home: d.home, Workspace: d.workspace})
	registrationErr := registration.Err()
	done()
	resource := &larkResource{runtime: r}
	if err != nil || registrationErr != nil || nilLarkRuntime(r) {
		_ = e.closeLarkRuntime(resource)
		return reject()
	}
	bin := r.BinDir()
	real, err := filepath.EvalSymlinks(filepath.Join(bin, "lark-cli"))
	info, statErr := os.Stat(filepath.Join(bin, "lark-cli"))
	if !filepath.IsAbs(bin) || bin != filepath.Clean(bin) || strings.ContainsAny(bin, ":\x00\r\n") || err != nil || real != filepath.Join(bin, "lark-cli") || statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		_ = e.closeLarkRuntime(resource)
		return reject()
	}
	vars := r.Environment()
	u, err := url.Parse(vars[larkExecURL])
	validURL := err == nil && u.Scheme == "http" && u.User == nil && u.Path == "/__loop_now/lark/exec/v1" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
	if validURL {
		host, port, splitErr := net.SplitHostPort(u.Host)
		n, portErr := strconv.ParseUint(port, 10, 16)
		validURL = splitErr == nil && host == "127.0.0.1" && portErr == nil && n > 0 && strconv.FormatUint(n, 10) == port
	}
	key, keyErr := hex.DecodeString(vars[larkExecKey])
	if len(vars) != 2 || !validURL || keyErr != nil || len(key) != 32 || len(vars[larkExecKey]) != 64 {
		clear(key)
		_ = e.closeLarkRuntime(resource)
		return reject()
	}
	clear(key)
	return resource, bin, []string{larkExecURL + "=" + vars[larkExecURL], larkExecKey + "=" + vars[larkExecKey]}, nil
}

func (e *Executor) closeLarkRuntime(r *larkResource) error {
	if r == nil {
		return nil
	}
	if nilLarkRuntime(r.runtime) {
		return nil
	}
	err := r.runtime.Close() // nonblocking revocation; actual drain belongs to Wait
	ctx, cancel := context.WithTimeout(context.Background(), e.handoffTimeout())
	defer cancel()
	waitErr := r.runtime.Wait(ctx) // even Close failure must attempt bounded drain
	if err != nil || waitErr != nil {
		return ErrRuntimeStopUnconfirmed
	}
	return nil
}
