package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"loop-now/internal/server/cloudflared"
)

var (
	metricsLine    = regexp.MustCompile(`Starting metrics server on (127\.0\.0\.1:\d+)/metrics`)
	registeredLine = regexp.MustCompile(`(?i)\bRegistered tunnel connection\b`)
	// rejectedLine is the edge refusing the tunnel: it was recycled, or its token is stale (T3 Code reads the same).
	rejectedLine = regexp.MustCompile(`(?i)Register tunnel error from server side.*error="(?:Unauthorized:[^"]*|Failed to get tunnel|Tunnel not found|Record for tunnel not found|Invalid tunnel secret)"`)
	problemLine  = regexp.MustCompile(`\b(?:ERR|FTL|PNC)\b`)
	// abortedLine is a browser closing its request mid-stream; the tunnel itself is fine.
	abortedLine = regexp.MustCompile(`Incoming request ended abruptly`)
)

// Args are cloudflared's arguments; the run token goes in the TUNNEL_TOKEN environment variable, never on the
// command line, where other users of the computer could read it.
var Args = []string{"tunnel", "--no-autoupdate", "--loglevel", "info", "--metrics", "127.0.0.1:0", "run"}

// childEnv is this server's environment without cloudflared's own TUNNEL_ settings, plus the run token.
func childEnv(token string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "TUNNEL_") {
			env = append(env, kv)
		}
	}
	return append(env, "TUNNEL_TOKEN="+token)
}

// watch holds what cloudflared's output said.
type watch struct {
	mu         sync.Mutex
	metrics    string
	problem    string // the last error line
	rejections int
	registered chan struct{}
	rejected   chan struct{}
}

func (w *watch) line(m *Manager, s string) {
	m.log().Debug("cloudflared", "line", s)
	w.mu.Lock()
	defer w.mu.Unlock()
	if sub := metricsLine.FindStringSubmatch(s); sub != nil && w.metrics == "" {
		w.metrics = sub[1]
	}
	switch {
	case registeredLine.MatchString(s):
		w.rejections = 0
		select {
		case w.registered <- struct{}{}:
		default:
		}
	case rejectedLine.MatchString(s):
		w.problem = s
		w.rejections++
		if w.rejections >= rejectionsBeforeRestart {
			select {
			case w.rejected <- struct{}{}:
			default:
			}
		}
	case problemLine.MatchString(s) && !abortedLine.MatchString(s):
		w.problem = s
	}
}

func (w *watch) get() (metrics, problem string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.metrics, w.problem
}

// ready asks cloudflared's metrics server how many connections to Cloudflare it has.
func ready(ctx context.Context, addr string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ready", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var body struct {
		ReadyConnections int `json:"readyConnections"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body); err != nil {
		return 0, err
	}
	return body.ReadyConnections, nil
}

func terminate(p *os.Process) {
	if err := p.Signal(syscall.SIGTERM); err != nil {
		_ = p.Kill()
	}
}

// runCloudflared runs cloudflared until it exits, the loop stops, a restart is asked for, or it has had no
// connection to Cloudflare for too long.
func (m *Manager) runCloudflared(ctx context.Context, r *run, bin cloudflared.Binary, token string) failure {
	cmd := exec.Command(bin.Path, Args...)
	cmd.Env = childEnv(token)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return failure{code: ErrCloudflaredFailed, msg: err.Error(), retry: true}
	}
	cmd.Stderr = cmd.Stdout // cloudflared logs to stderr; one reader for both
	if err := cmd.Start(); err != nil {
		return failure{code: ErrCloudflaredFailed, msg: fmt.Sprintf("Could not start cloudflared (%s): %v", bin.Path, err), retry: true}
	}
	started := time.Now()
	m.log().Info("started cloudflared", "path", bin.Path, "version", bin.Version, "pid", cmd.Process.Pid)
	w := &watch{registered: make(chan struct{}, 1), rejected: make(chan struct{}, 1)}
	lines := make(chan struct{})
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			w.line(m, sc.Text())
		}
	}()
	exited := make(chan error, 1)
	go func() {
		<-lines // Wait closes the pipe; read it to the end first
		exited <- cmd.Wait()
	}()

	interval := orDefault(m.ReadyInterval, DefaultReadyInterval)
	notReady := orDefault(m.NotReadyRestart, DefaultNotReadyRestart)
	lastReady := started
	online := time.Time{}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	stopWith := func(f failure) failure {
		terminate(cmd.Process)
		select {
		case <-exited:
		case <-time.After(stopGrace):
			_ = cmd.Process.Kill()
			<-exited
		}
		if !online.IsZero() && time.Since(online) >= orDefault(m.StableUptime, DefaultStableUptime) {
			f.stable = true
		}
		return f
	}
	check := func() {
		addr, _ := w.get()
		if addr == "" {
			return
		}
		n, err := ready(ctx, addr)
		if err == nil && n > 0 {
			lastReady = time.Now()
			if online.IsZero() {
				online = lastReady
				m.log().Info("the tunnel is online")
			}
			m.set(r, func(s *status) {
				if s.state != StateOnline {
					*s = status{state: StateOnline, cloudflared: s.cloudflared}
				}
			})
			return
		}
		m.set(r, func(s *status) {
			if s.state == StateOnline {
				*s = status{state: StateConnecting, cloudflared: s.cloudflared}
			}
		})
	}
	for {
		select {
		case <-ctx.Done():
			stopWith(failure{})
			m.log().Info("stopped cloudflared")
			return failure{}
		case <-r.restart:
			return stopWith(failure{code: ErrCloudflaredFailed, msg: "The remote door moved to another port; bringing the tunnel up again.", retry: true, now: true})
		case <-w.rejected:
			_, problem := w.get()
			return stopWith(failure{code: ErrCloudflaredFailed, msg: "Cloudflare refused the tunnel; bringing it up again. " + problem, retry: true, now: true})
		case err := <-exited:
			_, problem := w.get()
			msg := fmt.Sprintf("cloudflared exited (%v).", err)
			if err == nil {
				msg = "cloudflared exited."
			}
			if problem != "" {
				msg += " Last error: " + problem
			}
			f := failure{code: ErrCloudflaredFailed, msg: msg, retry: true}
			if !online.IsZero() && time.Since(online) >= orDefault(m.StableUptime, DefaultStableUptime) {
				f.stable = true
			}
			return f
		case <-w.registered:
			check()
		case <-tick.C:
			check()
			if time.Since(lastReady) >= notReady {
				_, problem := w.get()
				return stopWith(failure{code: ErrCloudflaredFailed, msg: fmt.Sprintf("cloudflared had no connection to Cloudflare for %v; restarting it. %s", notReady, problem), retry: true})
			}
		}
	}
}
