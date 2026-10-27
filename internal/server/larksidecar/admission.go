package larksidecar

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const probeProtocol = "loop-now-lark-sandbox-probe-v2"

type probeRequest struct {
	Protocol, Nonce, Binding            string
	Allowed, Other4, Same6, Other6, DNS string
}
type probeResult struct {
	Protocol, Nonce, Binding                       string
	Allowed, Other4, Same6, Other6, DNS, Inherited bool
}

// A bounded writer for trusted-probe output. No raw stderr is surfaced/logged.
type probeOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *probeOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4096 {
		b.overflow = true
		return 0, errors.New("probe output limit")
	}
	return b.Buffer.Write(p)
}

// admitSandbox performs production admission with the exact native profile and
// independently owned positive controls. It carries no capability or credential.
// Every invocation is bound to this profile/broker/native/probe; no global cache.
func (b *Broker) admitSandbox(ctx context.Context, cfg LaunchConfig, profile string) error {
	return b.admitSandboxWith(ctx, cfg, profile, "/usr/bin/sandbox-exec")
}
func (b *Broker) admitSandboxWith(ctx context.Context, cfg LaunchConfig, profile, sandbox string) error {
	reject := func() error { return errors.New("lark sandbox unavailable") }
	if validateExecutable(cfg.ProbeBinary, cfg.ProbeSHA256) != nil {
		return reject()
	}
	child, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
		wg.Wait()
	}()
	listen := func(network, addr string) (net.Listener, error) {
		ln, e := net.Listen(network, addr)
		if e != nil {
			return nil, e
		}
		closers = append(closers, ln)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				c, e := ln.Accept()
				if e != nil {
					return
				}
				_ = c.Close()
			}
		}()
		return ln, nil
	}
	allowed := strings.TrimPrefix(b.address, "http://")
	_, port, e := net.SplitHostPort(allowed)
	if e != nil {
		return reject()
	}
	other4, e := listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return reject()
	}
	same6 := b.address6
	if same6 != net.JoinHostPort("::1", port) {
		return reject()
	}
	other6, e := listen("tcp6", "[::1]:0")
	if e != nil {
		return reject()
	}
	udp, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		return reject()
	}
	closers = append(closers, udp)
	// Positive controls prove each owned target is reachable before classifying
	// EPERM/EACCES in the restricted process. Refused/timeouts never pass.
	for _, target := range []struct{ network, address string }{{"tcp4", allowed}, {"tcp4", other4.Addr().String()}, {"tcp6", same6}, {"tcp6", other6.Addr().String()}} {
		c, e := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(child, target.network, target.address)
		if e != nil {
			return reject()
		}
		_ = c.Close()
	}
	u, e := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(child, "udp4", udp.LocalAddr().String())
	if e != nil {
		return reject()
	}
	_, e = u.Write([]byte("owned-control"))
	_ = u.Close()
	if e != nil {
		return reject()
	}
	_ = udp.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 64)
	if _, _, e = udp.ReadFrom(buf); e != nil {
		return reject()
	}
	_ = udp.SetReadDeadline(time.Time{})
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return reject()
	}
	digest := sha256.Sum256([]byte(profile + "\x00" + allowed + "\x00" + cfg.BinarySHA256 + "\x00" + cfg.ProbeSHA256))
	req := probeRequest{Protocol: probeProtocol, Nonce: hex.EncodeToString(nonce), Binding: hex.EncodeToString(digest[:]), Allowed: allowed, Other4: other4.Addr().String(), Same6: same6, Other6: other6.Addr().String(), DNS: udp.LocalAddr().String()}
	raw, _ := json.Marshal(req)
	cmd := exec.CommandContext(child, sandbox, "-p", profile, cfg.ProbeBinary, "--probe-v1")
	cmd.Dir = cfg.WorkingDir
	cmd.Env = []string{"HOME=" + cfg.Home, "PATH=/usr/bin:/bin", "GODEBUG=netdns=go"}
	cmd.Stdin = bytes.NewReader(raw)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if e == syscall.ESRCH {
			return nil
		}
		return e
	}
	var out probeOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil || out.overflow || child.Err() != nil {
		code := -1
		if exit, ok := e.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
		return fmt.Errorf("sandbox probe execution rejected: exit=%d timeout=%t overflow=%t", code, child.Err() != nil, out.overflow)
	}
	var res probeResult
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	dec.DisallowUnknownFields()
	if dec.Decode(&res) != nil || dec.Decode(new(any)) != io.EOF || res.Protocol != probeProtocol || res.Nonce != req.Nonce || res.Binding != req.Binding || !res.Allowed || !res.Other4 || !res.Same6 || !res.Other6 || !res.DNS || !res.Inherited {
		return fmt.Errorf("sandbox probe policy rejected: allowed=%t other4=%t same6=%t other6=%t dns=%t inherited=%t", res.Allowed, res.Other4, res.Same6, res.Other6, res.DNS, res.Inherited)
	}
	// The owned UDP endpoint must not have received a probe packet after control.
	_ = udp.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	if _, _, e = udp.ReadFrom(buf); e == nil {
		return reject()
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed || ctx.Err() != nil {
		return reject()
	}
	return nil
}
