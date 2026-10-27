package conn

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"loop-now/internal/loopctl/output"
)

// 响应头。
const (
	HeaderVersion   = "Loop-Now-Version"
	HeaderRequestID = "Loop-Now-Request-Id"
)

// DialFunc 建立网络连接；测试可以替换它，确认某些命令不发出请求。
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// ClientOptions 是创建客户端的参数。
type ClientOptions struct {
	Version string // loopctl 版本
	GOOS    string
	GOARCH  string
	Dial    DialFunc                                         // 为空时使用 net.Dialer
	Sleep   func(ctx context.Context, d time.Duration) error // 为空时真实等待
}

// Client 是 loopctl 的 HTTP 客户端，记录最后一次响应的版本、请求 ID 与请求次数。
type Client struct {
	s     *Settings
	hc    *http.Client
	opts  ClientOptions
	ua    string
	sleep func(ctx context.Context, d time.Duration) error

	responded     bool
	serverVersion string
	requestID     string
	attempts      int
}

// NewClient 按连接参数创建客户端：不跟随重定向；指定 CA 时只信任该 CA。
func NewClient(s *Settings, o ClientOptions) *Client {
	dial := o.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         dial,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{RootCAs: s.roots}, // roots 为空时使用系统根证书
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
	}
	sleep := o.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return &Client{
		s:     s,
		opts:  o,
		ua:    fmt.Sprintf("loopctl/%s (%s/%s)", o.Version, o.GOOS, o.GOARCH),
		sleep: sleep,
		hc: &http.Client{
			Transport:     tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Settings 返回连接参数。
func (c *Client) Settings() *Settings { return c.s }

// Request 描述一个请求。Path 为基址之后的路径段，每段单独转义。
type Request struct {
	Method      string
	Path        []string
	Query       url.Values
	Body        []byte
	ContentType string
	Accept      string
	Header      http.Header // 其他请求头，例如 If-Match
	Retry       bool
	// Consume 处理 2xx 响应体（例如流式写入文件）；为空时按 JSON 读取。
	// 返回 *output.Error 时直接失败；返回其他错误时视为读取中断，可按重试规则重试。
	Consume func(resp *http.Response) error
}

// Response 是成功的响应。Data 为压缩后的 JSON，响应体为空时为 null。
type Response struct {
	Status int
	Header http.Header
	Data   json.RawMessage
}

// Do 发出请求。只有 Retry 为 true 的请求，在连接失败、超时或 502/503/504 时按重试参数重试。
func (c *Client) Do(ctx context.Context, r Request) (*Response, *output.Error) {
	c.attempts = 0
	delay := c.s.Retry.Initial
	for {
		c.attempts++
		resp, e := c.attempt(ctx, r)
		if e == nil {
			return resp, nil
		}
		if !r.Retry || c.attempts > c.s.Retry.Max || !Retryable(e) {
			return nil, e
		}
		if c.sleep(ctx, delay) != nil {
			return nil, e
		}
		delay *= 2
		if delay > c.s.Retry.MaxInterval {
			delay = c.s.Retry.MaxInterval
		}
	}
}

// Retryable 判断错误是否属于可以重试的临时错误。
func Retryable(e *output.Error) bool {
	switch e.Code {
	case output.CodeConnectionFailed, output.CodeTimeout:
		return true
	}
	switch e.HTTPStatus {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func (c *Client) attempt(ctx context.Context, r Request) (*Response, *output.Error) {
	actx, cancel := context.WithTimeout(ctx, c.s.Timeout)
	defer cancel()
	u := c.url(r)
	req, err := http.NewRequestWithContext(actx, r.Method, u.String(), bytes.NewReader(r.Body))
	if err != nil {
		return nil, output.Errorf(output.CodeInternalError, "无法构造请求：%v", err)
	}
	if r.Body == nil {
		req.Body, req.ContentLength = http.NoBody, 0
	}
	req.Header.Set("Authorization", "Bearer "+c.s.token)
	req.Header.Set("User-Agent", c.ua)
	accept := r.Accept
	if accept == "" {
		accept = "application/json"
	}
	req.Header.Set("Accept", accept)
	if r.ContentType != "" {
		req.Header.Set("Content-Type", r.ContentType)
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, c.transportError(actx, err)
	}
	defer resp.Body.Close()
	c.responded = true
	c.serverVersion = resp.Header.Get(HeaderVersion)
	c.requestID = resp.Header.Get(HeaderRequestID)

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		e := output.Errorf(output.CodeUnexpectedRedirect, "服务端返回重定向 HTTP %d，loopctl 不跟随重定向", resp.StatusCode)
		e.HTTPStatus = resp.StatusCode
		return nil, e
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if r.Consume != nil {
			if err := r.Consume(resp); err != nil {
				var oe *output.Error
				if errors.As(err, &oe) {
					return nil, oe
				}
				return nil, c.transportError(actx, err)
			}
			return &Response{Status: resp.StatusCode, Header: resp.Header, Data: json.RawMessage("null")}, nil
		}
		body, e := c.readBody(actx, resp)
		if e != nil {
			return nil, e
		}
		data, err := output.Raw(json.RawMessage(bytes.TrimSpace(body)))
		if err != nil {
			e := output.Errorf(output.CodeUnexpectedResponse, "服务端返回 HTTP %d，但响应体不是合法的 JSON", resp.StatusCode)
			e.HTTPStatus = resp.StatusCode
			return nil, e
		}
		return &Response{Status: resp.StatusCode, Header: resp.Header, Data: data}, nil
	default:
		body, e := c.readBody(actx, resp)
		if e != nil {
			return nil, e
		}
		return nil, serverError(resp.StatusCode, body)
	}
}

func (c *Client) url(r Request) *url.URL {
	u := *c.s.Base
	var raw, plain strings.Builder
	raw.WriteString(c.s.Base.EscapedPath())
	plain.WriteString(c.s.Base.Path)
	for _, seg := range r.Path {
		raw.WriteString("/" + url.PathEscape(seg))
		plain.WriteString("/" + seg)
	}
	u.Path, u.RawPath = plain.String(), raw.String()
	u.RawQuery = r.Query.Encode()
	return &u
}

func (c *Client) readBody(ctx context.Context, resp *http.Response) ([]byte, *output.Error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.s.MaxResponse+1))
	if err != nil {
		return nil, c.transportError(ctx, err)
	}
	if int64(len(body)) > c.s.MaxResponse {
		e := output.Errorf(output.CodeUnexpectedResponse, "响应超过 %s 的上限（%d 字节）", EnvMaxResponse, c.s.MaxResponse)
		e.HTTPStatus = resp.StatusCode
		if resp.StatusCode >= 300 {
			e.Category = output.CategoryForStatus(resp.StatusCode)
		}
		return nil, e
	}
	return body, nil
}

// serverError 把非 2xx 响应转为输出错误：能解析为统一错误结构时保留服务端的错误码、说明、详情与字段错误。
func serverError(status int, body []byte) *output.Error {
	var env struct {
		Error *struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details json.RawMessage `json:"details"`
			Fields  json.RawMessage `json:"fields"`
		} `json:"error"`
	}
	cat := output.CategoryForStatus(status)
	if json.Unmarshal(body, &env) == nil && env.Error != nil && env.Error.Code != "" {
		e := &output.Error{Code: env.Error.Code, Category: cat, Message: env.Error.Message, HTTPStatus: status}
		if d, err := output.Raw(env.Error.Details); err == nil && string(d) != "null" {
			e.Details = d
		}
		if f, err := output.Raw(env.Error.Fields); err == nil && string(f) != "null" {
			e.Fields = f
		}
		return e
	}
	e := &output.Error{
		Code:       output.CodeUnexpectedResponse,
		Category:   cat,
		Message:    fmt.Sprintf("服务端返回 HTTP %d，响应体不是统一的错误结构", status),
		HTTPStatus: status,
	}
	// 响应体是 JSON 对象时原样放进 details（例如 /health 在 503 时返回的依赖状态）。
	if t := bytes.TrimSpace(body); len(t) > 0 && t[0] == '{' {
		if d, err := output.Raw(json.RawMessage(t)); err == nil {
			e.Details = d
		}
	}
	return e
}

// transportError 把网络层错误分为超时、证书校验失败与连接失败。
func (c *Client) transportError(ctx context.Context, err error) *output.Error {
	var (
		verr  *tls.CertificateVerificationError
		uaerr x509.UnknownAuthorityError
		herr  x509.HostnameError
		cierr x509.CertificateInvalidError
	)
	switch {
	case errors.As(err, &verr), errors.As(err, &uaerr), errors.As(err, &herr), errors.As(err, &cierr):
		return output.Errorf(output.CodeTLSVerifyFailed, "服务端证书校验失败：%v；如果服务端使用自签证书，请用 --ca-file 或 %s 指定 CA", rootCause(err), EnvCAFile)
	case errors.Is(ctx.Err(), context.DeadlineExceeded), isTimeout(err):
		return output.Errorf(output.CodeTimeout, "请求在 %s 内没有完成", c.s.Timeout)
	default:
		return output.Errorf(output.CodeConnectionFailed, "无法连接服务端：%v", rootCause(err))
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// rootCause 去掉 *url.Error 的外层（其中重复了请求地址）。
func rootCause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Meta 返回最后一次响应的信息：服务端版本、请求 ID 与请求次数；没有收到响应时 responded 为 false。
func (c *Client) Meta() (serverVersion, requestID string, attempts int, responded bool) {
	return c.serverVersion, c.requestID, c.attempts, c.responded
}

// Warnings 按最后一次响应的版本头给出版本提示。
func (c *Client) Warnings() []output.Warning {
	if !c.responded {
		return nil
	}
	if c.serverVersion == "" {
		return []output.Warning{{Code: output.WarnServerVersionUnknown, Message: "服务端响应中没有 " + HeaderVersion + "，无法比对版本"}}
	}
	if c.serverVersion == c.opts.Version {
		return nil
	}
	details := map[string]any{
		"clientVersion": c.opts.Version,
		"serverVersion": c.serverVersion,
		"os":            c.opts.GOOS,
		"arch":          c.opts.GOARCH,
	}
	msg := fmt.Sprintf("loopctl 版本 %s 与服务端版本 %s 不一致，命令照常执行", c.opts.Version, c.serverVersion)
	if c.s.Identity == IdentityExternal && c.s.Root != nil {
		details["downloadPath"] = DownloadPath(c.s.Root, c.opts.GOOS, c.opts.GOARCH)
		msg += "；请从服务端下载同版本的 loopctl，并按 SHA256SUMS 校验"
	} else {
		msg += "；平台会更新 Agent 使用的 loopctl"
	}
	return []output.Warning{{Code: output.WarnVersionMismatch, Message: msg, Details: details}}
}

// DownloadPath 返回本平台安装包在服务端上的路径。
func DownloadPath(root *url.URL, goos, goarch string) string {
	return root.Path + "/downloads/loopctl/loopctl-" + goos + "-" + goarch
}

func sortStrings(s []string) { sort.Strings(s) }

func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
