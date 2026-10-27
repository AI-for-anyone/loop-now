package modelauth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"

	"loop-now/internal/server/modelcredential"
)

type RuntimeResolver interface {
	Resolve(context.Context, modelcredential.Binding) (modelcredential.Credential, error)
}
type RuntimeBridge struct {
	binding                                        modelcredential.Binding
	resolver                                       RuntimeResolver
	timeout                                        time.Duration
	childRead, childWrite, parentRead, parentWrite *os.File
	ctx                                            context.Context
	cancel                                         context.CancelFunc
	once                                           sync.Once
	writeMu                                        sync.Mutex
	pending                                        chan struct{}
}

func NewRuntimeBridge(binding modelcredential.Binding, resolver RuntimeResolver, timeout time.Duration) (*RuntimeBridge, error) {
	if !binding.Valid() || binding.Provider != "openai-codex" || resolver == nil {
		return nil, ErrCredentialUnavailable
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	childRead, parentWrite, e := os.Pipe()
	if e != nil {
		return nil, ErrHelperUnavailable
	}
	parentRead, childWrite, e := os.Pipe()
	if e != nil {
		childRead.Close()
		parentWrite.Close()
		return nil, ErrHelperUnavailable
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &RuntimeBridge{binding: binding, resolver: resolver, timeout: timeout, childRead: childRead, childWrite: childWrite, parentRead: parentRead, parentWrite: parentWrite, ctx: ctx, cancel: cancel, pending: make(chan struct{}, 32)}
	go b.read()
	return b, nil
}
func (b *RuntimeBridge) ChildFiles() []*os.File { return []*os.File{b.childRead, b.childWrite} }
func (b *RuntimeBridge) ChildStarted()          { b.childRead.Close(); b.childWrite.Close() }
func (b *RuntimeBridge) Close() error {
	b.once.Do(func() {
		b.cancel()
		b.childRead.Close()
		b.childWrite.Close()
		b.parentRead.Close()
		b.parentWrite.Close()
	})
	return nil
}

var refreshID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func (b *RuntimeBridge) read() {
	defer b.Close()
	scan := bufio.NewScanner(b.parentRead)
	scan.Buffer(make([]byte, 4096), MaxFrame+1)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) > MaxFrame || !utf8.Valid(line) {
			return
		}
		var request struct {
			ID       string `json:"id"`
			Method   string `json:"method"`
			Provider string `json:"provider"`
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || !refreshID.MatchString(request.ID) || request.Method != "refresh" || request.Provider != b.binding.Provider {
			return
		}
		select {
		case b.pending <- struct{}{}:
		default:
			return
		}
		go func(id string) { defer func() { <-b.pending }(); b.respond(id) }(request.ID)
	}
}
func (b *RuntimeBridge) respond(id string) {
	ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
	defer cancel()
	credential, e := b.resolver.Resolve(ctx, b.binding)
	response := struct {
		ID         string          `json:"id"`
		OK         bool            `json:"ok"`
		Credential json.RawMessage `json:"credential,omitempty"`
		Code       string          `json:"code,omitempty"`
	}{ID: id}
	var secret []byte
	if e == nil {
		secret, e = credential.Encode()
		if e == nil && credential.Kind() == "oauth" {
			response.OK = true
			response.Credential = secret
		}
	}
	defer clear(secret)
	if !response.OK {
		response.Code = "auth_unavailable"
	}
	data, e := json.Marshal(response)
	if e != nil || len(data) > MaxFrame {
		b.Close()
		return
	}
	defer clear(data)
	data = append(data, '\n')
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if b.ctx.Err() != nil {
		return
	}
	if e = b.parentWrite.SetWriteDeadline(time.Now().Add(b.timeout)); e != nil {
		b.Close()
		return
	}
	if _, e = b.parentWrite.Write(data); e != nil {
		b.Close()
	}
}

var _ io.Closer = (*RuntimeBridge)(nil)
