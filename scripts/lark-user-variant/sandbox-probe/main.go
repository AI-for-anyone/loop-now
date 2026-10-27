// Platform-owned admission probe, shipped and SHA-pinned separately from the
// unmodified official CLI. It accepts only caller-owned numeric loopback targets.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const protocol = "loop-now-lark-sandbox-probe-v2"

type request struct {
	Protocol, Nonce, Binding            string
	Allowed, Other4, Same6, Other6, DNS string
}
type result struct {
	Protocol, Nonce, Binding                       string
	Allowed, Other4, Same6, Other6, DNS, Inherited bool
}

func numeric(s string, v6 bool) bool {
	host, port, e := net.SplitHostPort(s)
	ip := net.ParseIP(host)
	return e == nil && port != "" && ip != nil && ip.IsLoopback() && (ip.To4() == nil) == v6
}
func denied(err error) bool { return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) }
func tcp(ctx context.Context, network, address string, allow bool) bool {
	if ctx.Err() != nil {
		return false
	}
	host, port, _ := net.SplitHostPort(address)
	n, _ := strconv.Atoi(port)
	family := syscall.AF_INET
	var addr syscall.Sockaddr
	if network == "tcp6" {
		family = syscall.AF_INET6
		var ip [16]byte
		copy(ip[:], net.ParseIP(host).To16())
		addr = &syscall.SockaddrInet6{Port: n, Addr: ip}
	} else {
		var ip [4]byte
		copy(ip[:], net.ParseIP(host).To4())
		addr = &syscall.SockaddrInet4{Port: n, Addr: ip}
	}
	fd, e := syscall.Socket(family, syscall.SOCK_STREAM, syscall.IPPROTO_TCP)
	if e == nil {
		syscall.CloseOnExec(fd)
		defer syscall.Close(fd)
		e = syscall.Connect(fd, addr)
	}
	if allow {
		return e == nil
	}
	return denied(e)
}
func udp(ctx context.Context, address string) bool {
	c, e := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "udp4", address)
	if e != nil {
		return denied(e)
	}
	defer c.Close()
	// A fixed DNS-shaped packet, sent only to the owned numeric fixture. No
	// system resolver, host DNS server or external hostname is contacted.
	_, e = c.Write([]byte{0x51, 0x41, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'f', 'i', 'x', 't', 'u', 'r', 'e', 7, 'i', 'n', 'v', 'a', 'l', 'i', 'd', 0, 0, 1, 0, 1})
	return denied(e)
}
func main() {
	if e := run(); e != nil {
		os.Exit(2)
	}
}
func run() error {
	if len(os.Args) != 2 || (os.Args[1] != "--probe-v1" && os.Args[1] != "--child-v1") {
		return errors.New("mode")
	}
	var req request
	dec := json.NewDecoder(io.LimitReader(os.Stdin, 4097))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || req.Protocol != protocol || len(req.Nonce) != 32 || len(req.Binding) != 64 {
		return errors.New("frame")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("trailing frame")
	}
	if !numeric(req.Allowed, false) || !numeric(req.Other4, false) || !numeric(req.Same6, true) || !numeric(req.Other6, true) || !numeric(req.DNS, false) {
		return errors.New("nonfixture target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := result{Protocol: protocol, Nonce: req.Nonce, Binding: req.Binding}
	res.Allowed = tcp(ctx, "tcp4", req.Allowed, true)
	res.Other4 = tcp(ctx, "tcp4", req.Other4, false)
	res.Same6 = tcp(ctx, "tcp6", req.Same6, true)
	res.Other6 = tcp(ctx, "tcp6", req.Other6, false)
	res.DNS = udp(ctx, req.DNS)
	if os.Args[1] == "--probe-v1" {
		self, e := os.Executable()
		if e != nil {
			return e
		}
		child := exec.CommandContext(ctx, self, "--child-v1")
		raw, _ := json.Marshal(req)
		child.Stdin = strings.NewReader(string(raw))
		child.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=/usr/bin:/bin", "GODEBUG=netdns=go"}
		// Child inherits the parent's sandbox and process group; no ExtraFiles or
		// network fd is handed to it. A timeout is never a denial success.
		out, e := child.Output()
		if e == nil {
			var got result
			if json.Unmarshal(out, &got) == nil {
				res.Inherited = got.Protocol == protocol && got.Nonce == req.Nonce && got.Binding == req.Binding && got.Allowed && got.Other4 && got.Same6 && got.Other6 && got.DNS
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return json.NewEncoder(os.Stdout).Encode(res)
}
