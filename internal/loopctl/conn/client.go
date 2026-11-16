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

// Response headers.
const (
	HeaderVersion   = "Loop-Now-Version"
	HeaderRequestID = "Loop-Now-Request-Id"
)

// DialFunc opens a network connection; tests can replace it to confirm that certain commands send no request.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// ClientOptions are the parameters for creating a client.
type ClientOptions struct {
	Version string // loopctl version
	GOOS    string
	GOARCH  string
	Dial    DialFunc                                         // when nil, net.Dialer is used
	Sleep   func(ctx context.Context, d time.Duration) error // when nil, really waits
}

// Client is loopctl's HTTP client. It records the version and request ID of the last response and the number of
// attempts.
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

// NewClient creates a client from the connection settings: it does not follow redirects, and when a CA is given
// only that CA is trusted.
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
		TLSClientConfig:     &tls.Config{RootCAs: s.roots}, // when roots is nil, the system root certificates are used
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

// Settings returns the connection settings.
func (c *Client) Settings() *Settings { return c.s }

// Request describes one request. Path holds the path segments after the base address; each segment is escaped
// separately.
type Request struct {
	Method      string
	Path        []string
	Query       url.Values
	Body        []byte
	ContentType string
	Accept      string
	Header      http.Header // other request headers, for example If-Match
	Retry       bool
	// Consume handles a 2xx response body (for example by streaming it into a file); when nil, the body is read as
	// JSON. If it returns an *output.Error the request fails right away; any other error is treated as an
	// interrupted read and may be retried under the retry rules.
	Consume func(resp *http.Response) error
}

// Response is a successful response. Data is the compacted JSON, or null when the response body is empty.
type Response struct {
	Status int
	Header http.Header
	Data   json.RawMessage
}

// Do sends the request. Only requests with Retry set are retried, following the retry parameters, on connection
// failures, timeouts or 502/503/504.
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

// Retryable reports whether the error is a temporary error that may be retried.
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
		return nil, output.Errorf(output.CodeInternalError, "Could not build the request: %v", err)
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
		e := output.Errorf(output.CodeUnexpectedRedirect, "The server returned a redirect (HTTP %d). loopctl does not follow redirects.", resp.StatusCode)
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
			e := output.Errorf(output.CodeUnexpectedResponse, "The server returned HTTP %d, but the response body is not valid JSON.", resp.StatusCode)
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
		e := output.Errorf(output.CodeUnexpectedResponse, "The response exceeds the %s limit (%d bytes).", EnvMaxResponse, c.s.MaxResponse)
		e.HTTPStatus = resp.StatusCode
		if resp.StatusCode >= 300 {
			e.Category = output.CategoryForStatus(resp.StatusCode)
		}
		return nil, e
	}
	return body, nil
}

// serverError turns a non-2xx response into an output error. When the body parses as the common error structure,
// the server's error code, message, details and field errors are kept.
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
		Message:    fmt.Sprintf("The server returned HTTP %d, and the response body is not the common error structure.", status),
		HTTPStatus: status,
	}
	// When the response body is a JSON object, put it into details as is (for example the dependency status /health
	// returns with a 503).
	if t := bytes.TrimSpace(body); len(t) > 0 && t[0] == '{' {
		if d, err := output.Raw(json.RawMessage(t)); err == nil {
			e.Details = d
		}
	}
	return e
}

// transportError classifies network-level errors as timeouts, certificate verification failures or connection
// failures.
func (c *Client) transportError(ctx context.Context, err error) *output.Error {
	var (
		verr  *tls.CertificateVerificationError
		uaerr x509.UnknownAuthorityError
		herr  x509.HostnameError
		cierr x509.CertificateInvalidError
	)
	switch {
	case errors.As(err, &verr), errors.As(err, &uaerr), errors.As(err, &herr), errors.As(err, &cierr):
		return output.Errorf(output.CodeTLSVerifyFailed, "Could not verify the server certificate: %v. If the server uses a self-signed certificate, give its CA with --ca-file or %s.", rootCause(err), EnvCAFile)
	case errors.Is(ctx.Err(), context.DeadlineExceeded), isTimeout(err):
		return output.Errorf(output.CodeTimeout, "The request did not complete within %s.", c.s.Timeout)
	default:
		return output.Errorf(output.CodeConnectionFailed, "Could not connect to the server (%v).", rootCause(err))
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// rootCause strips the outer *url.Error (which repeats the request address).
func rootCause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Meta returns information about the last response: server version, request ID and number of attempts. responded
// is false when no response was received.
func (c *Client) Meta() (serverVersion, requestID string, attempts int, responded bool) {
	return c.serverVersion, c.requestID, c.attempts, c.responded
}

// Warnings returns version warnings based on the version header of the last response.
func (c *Client) Warnings() []output.Warning {
	if !c.responded {
		return nil
	}
	if c.serverVersion == "" {
		return []output.Warning{{Code: output.WarnServerVersionUnknown, Message: "The server response has no " + HeaderVersion + " header, so the versions cannot be compared."}}
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
	msg := fmt.Sprintf("loopctl version %s differs from server version %s. The command runs as usual.", c.opts.Version, c.serverVersion)
	if c.s.Identity == IdentityExternal && c.s.Root != nil {
		details["downloadPath"] = DownloadPath(c.s.Root, c.opts.GOOS, c.opts.GOARCH)
		msg += " Download the loopctl of the same version from the server and verify it against SHA256SUMS."
	} else {
		msg += " The platform will update the loopctl that Agents use."
	}
	return []output.Warning{{Code: output.WarnVersionMismatch, Message: msg, Details: details}}
}

// DownloadPath returns the path on the server of the package for this platform.
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
