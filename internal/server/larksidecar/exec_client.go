package larksidecar

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const execOutputLimit = 16 << 20

// RunLauncher is the separate PATH shim. The only scope inputs are the numeric
// loopback execution endpoint and independent execution capability. Configuration
// and provider HMAC/token secrets are never accepted from a file, argv or env.
func RunLauncher(ctx context.Context, endpoint, capability string, args []string, stdout, stderr io.Writer) (int, error) {
	reject := func() (int, error) { return 2, errors.New("lark execution unavailable") }
	if ctx == nil || stdout == nil || stderr == nil || ctx.Err() != nil {
		return reject()
	}
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Path != execPath || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return reject()
	}
	host, port, e := net.SplitHostPort(u.Host)
	n, pe := strconv.ParseUint(port, 10, 16)
	key, ke := hex.DecodeString(capability)
	if e != nil || host != "127.0.0.1" || pe != nil || n == 0 || strconv.FormatUint(n, 10) != port || ke != nil || len(key) != 32 || len(capability) != 64 || len(args) == 0 || len(args) > 4096 {
		return reject()
	}
	total := 0
	for _, a := range args {
		if len(a) > execRequestLimit-total {
			return reject()
		}
		total += len(a)
	}
	raw, e := json.Marshal(execRequest{Args: args})
	if e != nil || len(raw) > execRequestLimit {
		return reject()
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if e != nil {
		return reject()
	}
	req.Header.Set("Authorization", "Bearer "+capability)
	req.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 8192, ResponseHeaderTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: execTimeout + 10*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Do(req)
	if e != nil {
		return reject()
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/x-ndjson" {
		return reject()
	}
	scan := bufio.NewScanner(io.LimitReader(res.Body, 32<<20))
	scan.Buffer(make([]byte, 4096), execFrameLimit)
	total = 0
	for scan.Scan() {
		line := scan.Bytes()
		if !uniqueJSONKeys(line) {
			return reject()
		}
		var frame execFrame
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if dec.Decode(&frame) != nil || dec.Decode(new(any)) != io.EOF {
			return reject()
		}
		switch frame.Type {
		case "stdout", "stderr":
			if frame.ExitCode != nil || frame.Error != "" || len(frame.Data) == 0 || len(frame.Data) > 8<<10 || len(frame.Data) > execOutputLimit-total {
				return reject()
			}
			total += len(frame.Data)
			dst := stdout
			if frame.Type == "stderr" {
				dst = stderr
			}
			if _, e := dst.Write(frame.Data); e != nil {
				return reject()
			}
		case "exit":
			if len(frame.Data) != 0 || frame.ExitCode == nil || *frame.ExitCode < 0 || *frame.ExitCode > 255 || frame.Error != "" && frame.Error != "lark_execution_failed" && frame.Error != "lark_execution_rejected" {
				return reject()
			}
			return *frame.ExitCode, nil
		default:
			return reject()
		}
	}
	return reject() // EOF without terminal completion is always unknown/failure.
}
